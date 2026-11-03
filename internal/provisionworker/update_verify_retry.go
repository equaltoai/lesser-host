package provisionworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// verifyFetchOutcome classifies why the bounded instance-config fetch retry
// loop stopped, so advanceUpdateVerify can decide between "run the lanes",
// "fail closed", and "requeue for a fresh invocation".
type verifyFetchOutcome int

const (
	// verifyFetchOK means the endpoint answered with a 2xx payload.
	verifyFetchOK verifyFetchOutcome = iota
	// verifyFetchTransient means the endpoint never answered within the
	// bounded window (timeout/connection/5xx): the job must fail closed with a
	// "not answering yet" message, never a bare transport error.
	verifyFetchTransient
	// verifyFetchNonTransient means the endpoint answered but with an
	// error/wrong content (4xx, decode failure): fail closed immediately.
	verifyFetchNonTransient
	// verifyFetchBudgetExhausted means the worker invocation ran out of budget
	// mid-retry: requeue for a fresh invocation instead of failing the job.
	verifyFetchBudgetExhausted
)

// verifyFetchRetryPolicy configures the bounded retry window for the shared
// instance-config fetch. The zero value falls back to the package defaults;
// tests shrink the window and backoff so the behavior is exercised without
// real 60-90s waits.
type verifyFetchRetryPolicy struct {
	window    time.Duration
	baseDelay time.Duration
	maxDelay  time.Duration
}

var defaultVerifyFetchRetryPolicy = verifyFetchRetryPolicy{
	window:    updateVerifyTransientWindow,
	baseDelay: updateVerifyRetryBaseDelay,
	maxDelay:  updateVerifyRetryMaxDelay,
}

// verifyFetchResult carries the outcome of the bounded fetch retry loop.
type verifyFetchResult struct {
	cfg      instanceV2Response
	err      error
	outcome  verifyFetchOutcome
	attempts int64
}

// instanceConfigHTTPStatusError marks a non-2xx answer from the instance
// endpoint. The status code lets the retry classifier treat 5xx as transient
// ("not ready yet") and 4xx as a real answer to fail closed on.
type instanceConfigHTTPStatusError struct {
	StatusCode int
}

func (e *instanceConfigHTTPStatusError) Error() string {
	return fmt.Sprintf("instance config request failed (HTTP %d)", e.StatusCode)
}

// verifyInvocationBudgetRemaining reports how much worker-lambda time remains
// in the current invocation, and whether the context carries a deadline at
// all. Non-Lambda contexts (tests, local tooling) carry no deadline and are
// treated as having unbounded budget, mirroring the store's
// applyLambdaTimeoutGuard convention.
func verifyInvocationBudgetRemaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

// classifyVerifyFetchError reports whether an instance-config fetch failure is
// transient (the endpoint is not answering yet and may still be warming up)
// versus a real answer that verify must fail closed on. Transient covers
// timeout/connection/DNS/EOF transport failures and HTTP 5xx (gateway still
// coming up after deploy); non-transient covers HTTP 4xx, decode failures, and
// every content-level mismatch surfaced by the lanes themselves.
func classifyVerifyFetchError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var syscallErr *os.SyscallError
	if errors.As(err, &syscallErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var statusErr *instanceConfigHTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode >= 500
	}
	return false
}

// fetchInstanceConfigV2WithRetry retries the shared instance-config fetch
// inside a bounded window before any verification lane gives up on it, using
// the worker's standard jitteredBackoff between attempts. A just-replaced
// endpoint routinely misses the very first post-deploy probe while it warms
// (measured ~30-60s on live theory, issue #1060), and every instance-side
// lane consumes this single fetch's result, so a one-shot miss hard-failing
// the whole job is a false negative.
//
// The loop stops early when the invocation itself is about to run out of
// budget (verifyFetchBudgetExhausted) so advanceUpdateVerify can requeue for a
// fresh invocation instead of dying mid-verify.
func (s *Server) fetchInstanceConfigV2WithRetry(ctx context.Context, client *http.Client, baseDomain string) verifyFetchResult {
	policy := defaultVerifyFetchRetryPolicy
	if s != nil && s.verifyFetchRetryPolicy != nil {
		policy = *s.verifyFetchRetryPolicy
	}
	return fetchInstanceConfigV2WithRetryPolicy(ctx, client, baseDomain, policy)
}

func fetchInstanceConfigV2WithRetryPolicy(ctx context.Context, client *http.Client, baseDomain string, policy verifyFetchRetryPolicy) verifyFetchResult {
	window := policy.window
	if window <= 0 {
		window = updateVerifyTransientWindow
	}
	baseDelay := policy.baseDelay
	if baseDelay <= 0 {
		baseDelay = updateVerifyRetryBaseDelay
	}
	maxDelay := policy.maxDelay
	if maxDelay <= 0 {
		maxDelay = updateVerifyRetryMaxDelay
	}
	if maxDelay < baseDelay {
		maxDelay = baseDelay
	}

	start := time.Now()
	var lastErr error
	attempts := int64(0)
	for {
		if attempts > 0 {
			if time.Since(start) >= window {
				return verifyFetchResult{
					err:      fmt.Errorf("endpoint not answering yet after %v: %w", window, lastErr),
					outcome:  verifyFetchTransient,
					attempts: attempts,
				}
			}
			if remaining, ok := verifyInvocationBudgetRemaining(ctx); ok && remaining < updateVerifyMinBudgetToRetry {
				return verifyFetchResult{err: lastErr, outcome: verifyFetchBudgetExhausted, attempts: attempts}
			}
		}
		cfg, err := fetchInstanceConfigV2(ctx, client, baseDomain)
		attempts++
		if err == nil {
			return verifyFetchResult{cfg: cfg, outcome: verifyFetchOK, attempts: attempts}
		}
		lastErr = err
		if !classifyVerifyFetchError(err) {
			return verifyFetchResult{err: err, outcome: verifyFetchNonTransient, attempts: attempts}
		}
		delay := jitteredBackoff(attempts, baseDelay, maxDelay)
		select {
		case <-ctx.Done():
			return verifyFetchResult{err: lastErr, outcome: verifyFetchBudgetExhausted, attempts: attempts}
		case <-time.After(delay):
		}
	}
}

// deferUpdateVerifyForBudget requeues the update job with the standard short
// delay instead of running verification, because the current worker invocation
// has too little remaining budget to finish the lanes. Each requeue lands on a
// fresh invocation (ProvisionWorker runs at 120s), so verify then runs with a
// full budget rather than dying mid-phase with "lambda timeout imminent"
// (2026-08-25 failure mode).
func (s *Server) deferUpdateVerifyForBudget(ctx context.Context, job *models.UpdateJob, requestID string, now time.Time) (time.Duration, bool, error) {
	if job != nil {
		job.Note = strings.TrimSpace(noteVerifyingDeployment + " (deferred: insufficient worker budget)")
	}
	_ = s.persistUpdateJobAndInstance(ctx, job, requestID, now, nil)
	return provisionDefaultShortRetryDelay, false, nil
}
