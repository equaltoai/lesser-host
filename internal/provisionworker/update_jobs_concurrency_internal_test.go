package provisionworker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type recordingTransactionBuilder struct {
	putCount               int
	updateWithBuilderCalls []recordingUpdateWithBuilderCall
}

type recordingUpdateWithBuilderCall struct {
	model      any
	conditions []core.TransactCondition
}

const updateJobUpdatedAtField = "UpdatedAt"

func (r *recordingTransactionBuilder) Put(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	r.putCount++
	return r
}

func (r *recordingTransactionBuilder) Create(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return r
}

func (r *recordingTransactionBuilder) Update(_ any, _ []string, _ ...core.TransactCondition) core.TransactionBuilder {
	return r
}

func (r *recordingTransactionBuilder) UpdateWithBuilder(model any, _ func(core.UpdateBuilder) error, conditions ...core.TransactCondition) core.TransactionBuilder {
	r.updateWithBuilderCalls = append(r.updateWithBuilderCalls, recordingUpdateWithBuilderCall{
		model:      model,
		conditions: append([]core.TransactCondition(nil), conditions...),
	})
	return r
}

func (r *recordingTransactionBuilder) Delete(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return r
}

func (r *recordingTransactionBuilder) ConditionCheck(_ any, _ ...core.TransactCondition) core.TransactionBuilder {
	return r
}

func (r *recordingTransactionBuilder) WithContext(_ context.Context) core.TransactionBuilder {
	return r
}

func (r *recordingTransactionBuilder) Execute() error {
	return nil
}

func (r *recordingTransactionBuilder) ExecuteWithContext(_ context.Context) error {
	return nil
}

func TestPersistUpdateJobAndInstance_UsesConditionalUpdateInsteadOfPut(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	builder := &recordingTransactionBuilder{}
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		fn := testutil.RequireMockArg[func(core.TransactionBuilder) error](t, args, 1)
		require.NoError(t, fn(builder))
	})

	st := store.New(db)
	srv := NewServer(config.Config{}, st, nil, nil, nil, nil, nil, nil)

	expectedUpdatedAt := time.Unix(100, 0).UTC()
	now := expectedUpdatedAt.Add(time.Minute)
	job := &models.UpdateJob{
		ID:           "job1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepInstanceConfig,
		CreatedAt:    expectedUpdatedAt.Add(-time.Minute),
		UpdatedAt:    expectedUpdatedAt,
		ExpiresAt:    expectedUpdatedAt.Add(time.Hour),
		MaxAttempts:  10,
	}

	require.NoError(t, srv.persistUpdateJobAndInstance(context.Background(), job, "req", now, nil))
	require.Zero(t, builder.putCount)
	require.NotEmpty(t, builder.updateWithBuilderCalls)

	first := builder.updateWithBuilderCalls[0]
	key, ok := first.model.(*models.UpdateJob)
	require.True(t, ok)
	require.Equal(t, "job1", key.ID)
	require.Condition(t, func() bool {
		for _, cond := range first.conditions {
			if cond.Kind == core.TransactConditionKindField && cond.Field == updateJobUpdatedAtField && cond.Operator == "=" {
				got, ok := cond.Value.(time.Time)
				return ok && got.Equal(expectedUpdatedAt)
			}
		}
		return false
	}, "expected optimistic UpdatedAt condition on update job write")
}

func TestProcessUpdateJob_IgnoresConditionFailureFromStaleWriter(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{
			ID:           "job1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusQueued,
			Step:         updateStepQueued,
			CreatedAt:    time.Unix(100, 0).UTC(),
			UpdatedAt:    time.Unix(101, 0).UTC(),
			ExpiresAt:    time.Unix(200, 0).UTC(),
			MaxAttempts:  10,
		}
	})
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

	st := store.New(db)
	srv := NewServer(config.Config{
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifacts",
	}, st, nil, nil, nil, nil, nil, nil)

	require.NoError(t, srv.processUpdateJob(context.Background(), "req", "job1"))
}

func TestClaimUpdateRunnerStart_UsesOptimisticUpdatedAtCondition(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	builder := &recordingTransactionBuilder{}
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		fn := testutil.RequireMockArg[func(core.TransactionBuilder) error](t, args, 1)
		require.NoError(t, fn(builder))
	})

	st := store.New(db)
	srv := NewServer(config.Config{}, st, nil, nil, nil, nil, nil, nil)

	now := time.Unix(200, 0).UTC()
	expectedUpdatedAt := now.Add(-time.Minute)
	job := &models.UpdateJob{
		ID:           "job1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepBodyDeployStart,
		CreatedAt:    expectedUpdatedAt.Add(-time.Minute),
		UpdatedAt:    expectedUpdatedAt,
		ExpiresAt:    expectedUpdatedAt.Add(time.Hour),
		MaxAttempts:  10,
	}

	claimed, err := srv.claimUpdateRunnerStart(context.Background(), job, "req", now, updatePhaseBody, updateStepBodyDeployStart, updateStepBodyDeployClaimed, "claimed")
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEmpty(t, builder.updateWithBuilderCalls)

	first := builder.updateWithBuilderCalls[0]
	require.Condition(t, func() bool {
		for _, cond := range first.conditions {
			if cond.Kind == core.TransactConditionKindField && cond.Field == updateJobUpdatedAtField && cond.Operator == "=" {
				got, ok := cond.Value.(time.Time)
				return ok && got.Equal(expectedUpdatedAt)
			}
		}
		return false
	}, "expected optimistic UpdatedAt condition on runner claim")
}

func TestAcquireUpdateJobProcessingLease_UsesLeaseAndUpdatedAtConditions(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	builder := &recordingTransactionBuilder{}
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		fn := testutil.RequireMockArg[func(core.TransactionBuilder) error](t, args, 1)
		require.NoError(t, fn(builder))
	})

	st := store.New(db)
	srv := NewServer(config.Config{}, st, nil, nil, nil, nil, nil, nil)

	now := time.Unix(300, 0).UTC()
	expectedUpdatedAt := now.Add(-time.Minute)
	job := &models.UpdateJob{
		ID:           "job1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepBodyDeployStart,
		CreatedAt:    expectedUpdatedAt.Add(-time.Minute),
		UpdatedAt:    expectedUpdatedAt,
		ExpiresAt:    expectedUpdatedAt.Add(time.Hour),
		MaxAttempts:  10,
	}

	leased, err := srv.acquireUpdateJobProcessingLease(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.True(t, leased)
	require.NotEmpty(t, builder.updateWithBuilderCalls)

	first := builder.updateWithBuilderCalls[0]
	require.Condition(t, func() bool {
		for _, cond := range first.conditions {
			if cond.Kind == core.TransactConditionKindField && cond.Field == updateJobUpdatedAtField && cond.Operator == "=" {
				got, ok := cond.Value.(time.Time)
				return ok && got.Equal(expectedUpdatedAt)
			}
		}
		return false
	}, "expected optimistic UpdatedAt condition on lease acquire")
	require.Condition(t, func() bool {
		for _, cond := range first.conditions {
			if cond.Kind == core.TransactConditionKindExpression && strings.Contains(cond.Expression, "processingLeaseUntil") {
				return true
			}
		}
		return false
	}, "expected processing lease condition on lease acquire")
}

func TestReleaseUpdateJobProcessingLease_RequiresLeaseOwner(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	builder := &recordingTransactionBuilder{}
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		fn := testutil.RequireMockArg[func(core.TransactionBuilder) error](t, args, 1)
		require.NoError(t, fn(builder))
	})

	st := store.New(db)
	srv := NewServer(config.Config{}, st, nil, nil, nil, nil, nil, nil)

	job := &models.UpdateJob{
		ID:                   "job1",
		InstanceSlug:         "slug",
		ProcessingLeaseOwner: "req",
		ProcessingLeaseUntil: time.Unix(500, 0).UTC(),
	}

	srv.releaseUpdateJobProcessingLease(context.Background(), job, "req")
	require.NotEmpty(t, builder.updateWithBuilderCalls)

	first := builder.updateWithBuilderCalls[0]
	require.Condition(t, func() bool {
		for _, cond := range first.conditions {
			if cond.Kind == core.TransactConditionKindExpression && strings.Contains(cond.Expression, "processingLeaseOwner") {
				return true
			}
		}
		return false
	}, "expected owner guard on lease release")
	require.Equal(t, "", job.ProcessingLeaseOwner)
	require.True(t, job.ProcessingLeaseUntil.IsZero())
}
