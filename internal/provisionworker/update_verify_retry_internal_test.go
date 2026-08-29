package provisionworker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestVerifyInvocationBudgetRemaining(t *testing.T) {
	t.Parallel()

	remaining, ok := verifyInvocationBudgetRemaining(context.Background())
	require.False(t, ok)
	require.Zero(t, remaining)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	t.Cleanup(cancel)
	remaining, ok = verifyInvocationBudgetRemaining(ctx)
	require.True(t, ok)
	require.Greater(t, remaining, time.Duration(0))
	require.LessOrEqual(t, remaining, 30*time.Second)
}

func TestClassifyVerifyFetchError(t *testing.T) {
	t.Parallel()

	t.Run("timeout class is transient", func(t *testing.T) {
		t.Parallel()
		cases := []error{
			&url.Error{Op: "Get", URL: "https://instance.example", Err: context.DeadlineExceeded},
			&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ETIMEDOUT},
			&net.DNSError{Err: "no such host", Name: "instance.example"},
			&os.SyscallError{Syscall: "connect", Err: errors.New("connection refused")},
			io.EOF,
			io.ErrUnexpectedEOF,
			context.DeadlineExceeded,
		}
		for _, err := range cases {
			require.True(t, classifyVerifyFetchError(err), "expected %v to be transient", err)
		}
	})

	t.Run("http 5xx is transient", func(t *testing.T) {
		t.Parallel()
		require.True(t, classifyVerifyFetchError(&instanceConfigHTTPStatusError{StatusCode: http.StatusServiceUnavailable}))
		require.True(t, classifyVerifyFetchError(&instanceConfigHTTPStatusError{StatusCode: http.StatusBadGateway}))
	})

	t.Run("http 4xx is a real answer", func(t *testing.T) {
		t.Parallel()
		require.False(t, classifyVerifyFetchError(&instanceConfigHTTPStatusError{StatusCode: http.StatusNotFound}))
		require.False(t, classifyVerifyFetchError(&instanceConfigHTTPStatusError{StatusCode: http.StatusUnauthorized}))
	})

	t.Run("content mismatches are not transient", func(t *testing.T) {
		t.Parallel()
		require.False(t, classifyVerifyFetchError(errors.New("expected true, got false")))
		require.False(t, classifyVerifyFetchError(errors.New("disabled")))
		require.False(t, classifyVerifyFetchError(errors.New("unexpected EOF")))
		require.False(t, classifyVerifyFetchError(nil))
	})
}

// smallVerifyRetryPolicy shrinks the bounded window and backoff so tests
// exercise retry behavior without real 60-90s waits.
func smallVerifyRetryPolicy() verifyFetchRetryPolicy {
	return verifyFetchRetryPolicy{
		window:    300 * time.Millisecond,
		baseDelay: 10 * time.Millisecond,
		maxDelay:  40 * time.Millisecond,
	}
}

func instanceConfigBody() string {
	return `{"configuration":{"translation":{"enabled":false},"trust":{"enabled":true,"base_url":"https://lab.example.com"},"tips":{"enabled":false,"chain_id":0,"contract_address":""}}}`
}

func TestFetchInstanceConfigV2WithRetry_TimeoutThenSuccess(t *testing.T) {
	t.Parallel()

	var warmUntil atomic.Int64
	warmUntil.Store(time.Now().Add(120 * time.Millisecond).UnixMilli())
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		if time.Now().UnixMilli() < warmUntil.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceConfigBody()))
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	res := fetchInstanceConfigV2WithRetryPolicy(ctx, ts.Client(), ts.URL, smallVerifyRetryPolicy())
	require.Equal(t, verifyFetchOK, res.outcome)
	require.NoError(t, res.err)
	require.Greater(t, res.attempts, int64(1), "endpoint that misses early probes must retry into success")
	require.True(t, res.cfg.Configuration.Trust.Enabled)
}

func TestFetchInstanceConfigV2WithRetry_PersistentTimeoutFailsAfterWindow(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	start := time.Now()
	res := fetchInstanceConfigV2WithRetryPolicy(context.Background(), ts.Client(), ts.URL, smallVerifyRetryPolicy())
	elapsed := time.Since(start)

	require.Equal(t, verifyFetchTransient, res.outcome)
	require.Error(t, res.err)
	require.Contains(t, res.err.Error(), "endpoint not answering yet after")
	require.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "persistent miss must be retried across the bounded window")
}

func TestFetchInstanceConfigV2WithRetry_NonTransientFailsImmediately(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	res := fetchInstanceConfigV2WithRetryPolicy(context.Background(), ts.Client(), ts.URL, smallVerifyRetryPolicy())
	require.Equal(t, verifyFetchNonTransient, res.outcome)
	require.ErrorContains(t, res.err, "HTTP 404")
	require.Equal(t, int64(1), res.attempts, "a 4xx answer must fail closed without retrying")
	require.Equal(t, int64(1), requests.Load())
}

func TestFetchInstanceConfigV2WithRetry_BudgetExhaustedRequeues(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
	t.Cleanup(cancel)

	res := fetchInstanceConfigV2WithRetryPolicy(ctx, ts.Client(), ts.URL, smallVerifyRetryPolicy())
	require.Equal(t, verifyFetchBudgetExhausted, res.outcome, "a near-dead invocation must stop retrying and requeue")
	require.GreaterOrEqual(t, res.attempts, int64(1))
}

func TestAdvanceUpdateVerify_BudgetGuardDefersInsteadOfFailing(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	db.On("WithLambdaTimeout", mock.Anything).Return(db).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()
	srv := &Server{store: store.New(db)}

	job := &models.UpdateJob{
		ID:           "j1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepVerify,
		MaxAttempts:  10,
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
	t.Cleanup(cancel)

	delay, done, err := srv.advanceUpdateVerify(ctx, job, "req", time.Now().UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultShortRetryDelay, delay)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status, "budget deferral must requeue, not fail the job")
	require.Equal(t, updateStepVerify, job.Step)
	require.Contains(t, job.Note, "insufficient worker budget")
	require.Nil(t, job.VerifyTranslationOK, "no lane may run without the budget to finish verification")
}

func TestAdvanceUpdateVerify_PersistentTimeoutFailsClosed(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	db := ttmocks.NewMockExtendedDB()
	db.On("WithLambdaTimeout", mock.Anything).Return(db).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()
	policy := smallVerifyRetryPolicy()
	srv := &Server{
		cfg:                    config.Config{Stage: "live"},
		store:                  store.New(db),
		httpClient:             ts.Client(),
		verifyFetchRetryPolicy: &policy,
	}

	job := &models.UpdateJob{
		ID:                 "j1",
		InstanceSlug:       "slug",
		Status:             models.UpdateJobStatusRunning,
		Step:               updateStepVerify,
		BaseDomain:         tsHost(ts),
		MaxAttempts:        10,
		TranslationEnabled: false,
		TipEnabled:         false,
	}
	start := time.Now()
	delay, done, err := srv.advanceUpdateVerify(context.Background(), job, "req", time.Now().UTC())
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.True(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "verification_failed", job.ErrorCode)
	require.Contains(t, job.ErrorMessage, "endpoint not answering yet after")
	require.Contains(t, job.ErrorMessage, "translation:")
	require.Contains(t, job.ErrorMessage, "trust:")
	require.Contains(t, job.ErrorMessage, "tips:")
	require.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "verify must retry across the bounded window before failing")
}

func TestAdvanceUpdateVerify_WrongContentFailsClosedImmediately(t *testing.T) {
	t.Parallel()

	// Endpoint answers immediately but with trust disabled: a genuine
	// mismatch. Verify must fail closed on the first probe — no retry window.
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configuration":{"translation":{"enabled":false},"trust":{"enabled":false,"base_url":""},"tips":{"enabled":false}}}`))
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	db := ttmocks.NewMockExtendedDB()
	db.On("WithLambdaTimeout", mock.Anything).Return(db).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()
	policy := verifyFetchRetryPolicy{window: 5 * time.Second, baseDelay: 10 * time.Millisecond, maxDelay: 40 * time.Millisecond}
	srv := &Server{
		cfg:                    config.Config{Stage: "live"},
		store:                  store.New(db),
		httpClient:             ts.Client(),
		verifyFetchRetryPolicy: &policy,
	}

	job := &models.UpdateJob{
		ID:                        "j1",
		InstanceSlug:              "slug",
		Status:                    models.UpdateJobStatusRunning,
		Step:                      updateStepVerify,
		BaseDomain:                tsHost(ts),
		MaxAttempts:               10,
		TranslationEnabled:        false,
		TipEnabled:                false,
		LesserHostAttestationsURL: "https://lab.example.com",
	}
	start := time.Now()
	_, done, err := srv.advanceUpdateVerify(context.Background(), job, "req", time.Now().UTC())
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "verification_failed", job.ErrorCode)
	require.Contains(t, job.ErrorMessage, "trust: disabled")
	require.Less(t, elapsed, 5*time.Second, "wrong content must fail closed without waiting out the retry window")
}

func TestAdvanceUpdateVerify_RetryThenSuccessGreen(t *testing.T) {
	t.Parallel()

	var warmUntil atomic.Int64
	warmUntil.Store(time.Now().Add(120 * time.Millisecond).UnixMilli())
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		if time.Now().UnixMilli() < warmUntil.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(instanceConfigBody()))
	})
	handler.HandleFunc("/api/v1/trust/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	db := ttmocks.NewMockExtendedDB()
	db.On("WithLambdaTimeout", mock.Anything).Return(db).Maybe()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	qKey := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	policy := verifyFetchRetryPolicy{window: 600 * time.Millisecond, baseDelay: 20 * time.Millisecond, maxDelay: 60 * time.Millisecond}
	srv := &Server{
		cfg:                    config.Config{Stage: "live"},
		store:                  store.New(db),
		httpClient:             ts.Client(),
		verifyFetchRetryPolicy: &policy,
	}

	job := &models.UpdateJob{
		ID:                        "j1",
		InstanceSlug:              "slug",
		Status:                    models.UpdateJobStatusRunning,
		Step:                      updateStepVerify,
		AccountID:                 "123456789012",
		AccountRoleName:           "lesser-host-instance",
		Region:                    "us-east-1",
		BaseDomain:                tsHost(ts),
		LesserVersion:             "v1.6.24",
		LesserHostBaseURL:         "https://lab.example.com",
		LesserHostAttestationsURL: "https://lab.example.com",
		ReceiptJSON:               validManagedInstanceKeyReceipt(),
		MaxAttempts:               10,
	}

	delay, done, err := srv.advanceUpdateVerify(context.Background(), job, "req", time.Now().UTC())
	require.NoError(t, err)
	require.True(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusOK, job.Status)
	require.Equal(t, updateStepDone, job.Step)
	require.Contains(t, job.Note, "verification attempts", "retried-then-passed must be visible to the operator")
	require.NotNil(t, job.VerifyTranslationOK)
	require.True(t, *job.VerifyTranslationOK)
	require.NotNil(t, job.VerifyTrustOK)
	require.True(t, *job.VerifyTrustOK)
	require.NotNil(t, job.VerifyTipsOK)
	require.True(t, *job.VerifyTipsOK)
	require.NotNil(t, job.VerifyAIOK)
	require.True(t, *job.VerifyAIOK)
}

func TestUpdateDeployReceiptInstanceUpdate_StampsDeployedVersion(t *testing.T) {
	t.Parallel()

	t.Run("full update stamps lesser version", func(t *testing.T) {
		t.Parallel()
		job := &models.UpdateJob{LesserVersion: "v1.6.24"}
		fn := updateDeployReceiptInstanceUpdate(job)
		ub := new(ttmocks.MockUpdateBuilder)
		ub.On("Set", "LesserVersion", "v1.6.24").Return(ub).Maybe()
		tx := new(ttmocks.MockTransactionBuilder)
		tx.UpdateBuilder = ub
		tx.UpdateWithBuilder(&models.Instance{}, fn)
		require.NoError(t, tx.Execute())
		ub.AssertCalled(t, "Set", "LesserVersion", "v1.6.24")
	})

	t.Run("body-only update never stamps lesser version", func(t *testing.T) {
		t.Parallel()
		job := &models.UpdateJob{BodyOnly: true, LesserVersion: "v1.6.24"}
		fn := updateDeployReceiptInstanceUpdate(job)
		ub := new(ttmocks.MockUpdateBuilder)
		tx := new(ttmocks.MockTransactionBuilder)
		tx.UpdateBuilder = ub
		tx.UpdateWithBuilder(&models.Instance{}, fn)
		require.NoError(t, tx.Execute())
		ub.AssertNotCalled(t, "Set", "LesserVersion", mock.Anything)
	})

	t.Run("empty version never stamps", func(t *testing.T) {
		t.Parallel()
		job := &models.UpdateJob{LesserVersion: "  "}
		fn := updateDeployReceiptInstanceUpdate(job)
		ub := new(ttmocks.MockUpdateBuilder)
		tx := new(ttmocks.MockTransactionBuilder)
		tx.UpdateBuilder = ub
		tx.UpdateWithBuilder(&models.Instance{}, fn)
		require.NoError(t, tx.Execute())
		ub.AssertNotCalled(t, "Set", "LesserVersion", mock.Anything)
	})
}

// tsHost returns the bare host:port of an httptest TLS server so it can be
// used as an update job BaseDomain (verify is exercised with stage=live, which
// applies no stage prefix).
func tsHost(ts *httptest.Server) string {
	return strings.TrimPrefix(ts.URL, "https://")
}

// validManagedInstanceKeyReceipt builds a deploy receipt carrying a
// managed-instance-key proof that validates against the update job binding
// (slug/account/region/stage) used by the verification tests.
func validManagedInstanceKeyReceipt() string {
	return `{
		"version": 1,
		"app": "lesser",
		"base_domain": "slug.example.com",
		"account_id": "123456789012",
		"region": "us-east-1",
		"managed_instance_key": {
			"version": 1,
			"source": "deploy-runner-managed-profile",
			"secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:live/slug/instance-key-Ab12Cd",
			"key_id": "5b0a2f9f8a3f0d3c8a3b1e0f2c4d6e8f0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			"instance_slug": "slug",
			"stage": "live",
			"verified_at": "2026-08-26T00:00:00Z"
		}
	}`
}
