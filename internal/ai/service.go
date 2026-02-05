package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// BudgetDecision describes the outcome of a credit budget check and debit.
type BudgetDecision struct {
	Allowed    bool   `json:"allowed"`
	OverBudget bool   `json:"over_budget"`
	Reason     string `json:"reason,omitempty"`

	Month string `json:"month,omitempty"`

	IncludedCredits  int64 `json:"included_credits,omitempty"`
	UsedCredits      int64 `json:"used_credits,omitempty"`
	RemainingCredits int64 `json:"remaining_credits,omitempty"`

	RequestedCredits int64 `json:"requested_credits"`
	DebitedCredits   int64 `json:"debited_credits"`
}

// Request describes an AI module request for caching and queueing.
type Request struct {
	InstanceSlug string
	RequestID    string

	Module        string
	PolicyVersion string
	ModelSet      string

	CacheScope CacheScope

	Inputs   any
	Evidence []models.AIEvidenceRef

	// BaseCredits is the un-multiplied credit cost for a cache miss.
	BaseCredits int64

	// PricingMultiplierBps is applied to BaseCredits when debiting.
	PricingMultiplierBps int64

	// AllowOverage controls whether the budget debit is allowed to exceed IncludedCredits.
	AllowOverage bool

	// JobTTL controls how long queued jobs remain claimable before expiring.
	JobTTL time.Duration
}

// Response is the output of an AI request, either cached, queued, or error.
type Response struct {
	Status JobStatus `json:"status"`
	Cached bool      `json:"cached"`

	JobID    string `json:"job_id,omitempty"`
	ResultID string `json:"result_id,omitempty"`

	Result *models.AIResult `json:"result,omitempty"`
	Budget BudgetDecision   `json:"budget"`
}

// Service provides caching, budgeting, and queueing for AI modules.
type Service struct {
	store *store.Store
}

// NewService constructs a new Service backed by the provided store.
func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

type getOrQueuePrepared struct {
	InstanceSlug string
	JobID        string
	ResultID     string

	Module        string
	PolicyVersion string
	ModelSet      string
	CacheScope    CacheScope
	ScopeKey      string

	InputsJSON string
	InputsHash string
	Evidence   []models.AIEvidenceRef

	Now              time.Time
	Month            string
	CreditsRequested int64
}

// GetOrQueue returns a cached AIResult when available, otherwise queues an AIJob (budgeted) and returns queued.
// It never performs provider/tool calls.
func (s *Service) GetOrQueue(ctx context.Context, req Request) (Response, error) {
	prepared, resp, err := s.prepareGetOrQueue(req)
	if err != nil {
		return resp, err
	}

	if cached := s.getCachedResult(ctx, prepared); cached != nil {
		return cacheHitResponse(prepared.JobID, prepared.ResultID, cached, prepared.CreditsRequested), nil
	}

	if s.jobExists(ctx, prepared.JobID) {
		return alreadyQueuedResponse(prepared.JobID, prepared.ResultID, prepared.CreditsRequested), nil
	}

	if prepared.CreditsRequested <= 0 {
		return s.queueNoChargeJob(ctx, prepared, req)
	}

	budget, ok, err := s.loadBudget(ctx, prepared.InstanceSlug, prepared.Month)
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
	}
	if !ok {
		return budgetNotConfiguredResponse(prepared.JobID, prepared.ResultID, prepared.Month, prepared.CreditsRequested), nil
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < prepared.CreditsRequested && !req.AllowOverage {
		return budgetExceededResponse(prepared.JobID, prepared.ResultID, prepared.Month, budget, remaining, prepared.CreditsRequested), nil
	}

	return s.queueWithDebit(ctx, prepared, req, budget)
}

func (s *Service) prepareGetOrQueue(req Request) (getOrQueuePrepared, Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, fmt.Errorf("ai service not initialized")
	}

	instanceSlug := strings.TrimSpace(req.InstanceSlug)
	if instanceSlug == "" {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid instance"}}, fmt.Errorf("instanceSlug is required")
	}

	module := strings.ToLower(strings.TrimSpace(req.Module))
	policyVersion := strings.TrimSpace(req.PolicyVersion)
	modelSet := strings.TrimSpace(req.ModelSet)
	if module == "" || policyVersion == "" || modelSet == "" {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid module"}}, fmt.Errorf("module, policyVersion, and modelSet are required")
	}

	scope := req.CacheScope
	if strings.TrimSpace(string(scope)) == "" {
		scope = CacheScopeInstance
	}

	inputsJSON, err := CanonicalJSON(req.Inputs)
	if err != nil {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid inputs"}}, err
	}
	inputsHash, err := InputsHash(req.Inputs)
	if err != nil {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid inputs"}}, err
	}

	scopeKey := instanceSlug
	if strings.EqualFold(string(scope), string(CacheScopeGlobal)) {
		scopeKey = ""
	}

	resultID, err := CacheKey(scope, scopeKey, module, policyVersion, modelSet, inputsHash)
	if err != nil {
		return getOrQueuePrepared{}, Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid cache key"}}, err
	}

	now := time.Now().UTC()
	month := now.Format("2006-01")
	creditsRequested := billing.PricedCredits(req.BaseCredits, req.PricingMultiplierBps)

	return getOrQueuePrepared{
		InstanceSlug: instanceSlug,
		JobID:        resultID,
		ResultID:     resultID,

		Module:        module,
		PolicyVersion: policyVersion,
		ModelSet:      modelSet,
		CacheScope:    scope,
		ScopeKey:      scopeKey,

		InputsJSON: inputsJSON,
		InputsHash: inputsHash,
		Evidence:   append([]models.AIEvidenceRef(nil), req.Evidence...),

		Now:              now,
		Month:            month,
		CreditsRequested: creditsRequested,
	}, Response{}, nil
}

func (s *Service) getCachedResult(ctx context.Context, prepared getOrQueuePrepared) *models.AIResult {
	if s == nil || s.store == nil {
		return nil
	}

	cached, err := s.store.GetAIResult(ctx, prepared.ResultID)
	if err != nil || cached == nil {
		return nil
	}
	if !cached.ExpiresAt.IsZero() && cached.ExpiresAt.Before(prepared.Now) {
		return nil
	}
	return cached
}

func (s *Service) jobExists(ctx context.Context, jobID string) bool {
	if s == nil || s.store == nil {
		return false
	}
	job, err := s.store.GetAIJob(ctx, jobID)
	return err == nil && job != nil
}

func cacheHitResponse(jobID string, resultID string, cached *models.AIResult, creditsRequested int64) Response {
	return Response{
		Status:   JobStatusOK,
		Cached:   true,
		JobID:    jobID,
		ResultID: resultID,
		Result:   cached,
		Budget: BudgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "cache_hit",
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
	}
}

func alreadyQueuedResponse(jobID string, resultID string, creditsRequested int64) Response {
	return Response{
		Status:   JobStatusQueued,
		Cached:   false,
		JobID:    jobID,
		ResultID: resultID,
		Budget: BudgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "already_queued",
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
	}
}

func (s *Service) queueNoChargeJob(ctx context.Context, prepared getOrQueuePrepared, req Request) (Response, error) {
	ttl := req.JobTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	job := &models.AIJob{
		ID:            prepared.JobID,
		InstanceSlug:  prepared.InstanceSlug,
		Module:        prepared.Module,
		PolicyVersion: prepared.PolicyVersion,
		ModelSet:      prepared.ModelSet,
		CacheScope:    string(prepared.CacheScope),
		ScopeKey:      prepared.ScopeKey,
		InputsHash:    prepared.InputsHash,
		InputsJSON:    prepared.InputsJSON,
		Evidence:      append([]models.AIEvidenceRef(nil), prepared.Evidence...),
		Status:        models.AIJobStatusQueued,
		CreatedAt:     prepared.Now,
		UpdatedAt:     prepared.Now,
		ExpiresAt:     prepared.Now.Add(ttl),
		RequestID:     strings.TrimSpace(req.RequestID),
	}
	_ = job.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(job).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return alreadyQueuedResponse(prepared.JobID, prepared.ResultID, prepared.CreditsRequested), nil
		}
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
	}

	return Response{
		Status:   JobStatusQueued,
		Cached:   false,
		JobID:    prepared.JobID,
		ResultID: prepared.ResultID,
		Budget: BudgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "queued_no_charge",
			RequestedCredits: 0,
			DebitedCredits:   0,
		},
	}, nil
}

func (s *Service) loadBudget(ctx context.Context, instanceSlug string, month string) (*models.InstanceBudgetMonth, bool, error) {
	pk := fmt.Sprintf("INSTANCE#%s", strings.TrimSpace(instanceSlug))
	sk := fmt.Sprintf("BUDGET#%s", strings.TrimSpace(month))

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &budget, true, nil
}

func budgetNotConfiguredResponse(jobID string, resultID string, month string, creditsRequested int64) Response {
	return Response{
		Status:   JobStatusNotCheckedBudget,
		Cached:   false,
		JobID:    jobID,
		ResultID: resultID,
		Budget: BudgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget not configured",
			Month:            month,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
	}
}

func budgetExceededResponse(jobID string, resultID string, month string, budget *models.InstanceBudgetMonth, remaining int64, creditsRequested int64) Response {
	if budget == nil {
		return Response{
			Status:   JobStatusNotCheckedBudget,
			Cached:   false,
			JobID:    jobID,
			ResultID: resultID,
			Budget: BudgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget exceeded",
				Month:            month,
				RequestedCredits: creditsRequested,
				DebitedCredits:   0,
			},
		}
	}

	return Response{
		Status:   JobStatusNotCheckedBudget,
		Cached:   false,
		JobID:    jobID,
		ResultID: resultID,
		Budget: BudgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget exceeded",
			Month:            month,
			IncludedCredits:  budget.IncludedCredits,
			UsedCredits:      budget.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
	}
}

func (s *Service) queueWithDebit(ctx context.Context, prepared getOrQueuePrepared, req Request, budget *models.InstanceBudgetMonth) (Response, error) {
	ttl := req.JobTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	job := &models.AIJob{
		ID:            prepared.JobID,
		InstanceSlug:  prepared.InstanceSlug,
		Module:        prepared.Module,
		PolicyVersion: prepared.PolicyVersion,
		ModelSet:      prepared.ModelSet,
		CacheScope:    string(prepared.CacheScope),
		ScopeKey:      prepared.ScopeKey,
		InputsHash:    prepared.InputsHash,
		InputsJSON:    prepared.InputsJSON,
		Evidence:      append([]models.AIEvidenceRef(nil), prepared.Evidence...),
		Status:        models.AIJobStatusQueued,
		Attempts:      0,
		MaxAttempts:   3,
		CreatedAt:     prepared.Now,
		UpdatedAt:     prepared.Now,
		ExpiresAt:     prepared.Now.Add(ttl),
		RequestID:     strings.TrimSpace(req.RequestID),
	}
	_ = job.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: prepared.InstanceSlug,
		Month:        prepared.Month,
		UpdatedAt:    prepared.Now,
	}
	_ = updateBudget.UpdateKeys()

	budgetIncluded := int64(0)
	budgetUsed := int64(0)
	if budget != nil {
		budgetIncluded = budget.IncludedCredits
		budgetUsed = budget.UsedCredits
	}

	includedDebited, overageDebited := billing.PartsForDebit(budgetIncluded, budgetUsed, prepared.CreditsRequested)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(prepared.InstanceSlug, prepared.Month, strings.TrimSpace(req.RequestID), prepared.Module, prepared.JobID, prepared.CreditsRequested),
		InstanceSlug:           prepared.InstanceSlug,
		Month:                  prepared.Month,
		Module:                 prepared.Module,
		Target:                 prepared.JobID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(req.RequestID),
		RequestedCredits:       prepared.CreditsRequested,
		DebitedCredits:         prepared.CreditsRequested,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		CreatedAt:              prepared.Now,
	}
	_ = ledger.UpdateKeys()

	auditBudget := &models.AuditLogEntry{
		Actor:     prepared.InstanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", prepared.InstanceSlug, prepared.Month),
		RequestID: strings.TrimSpace(req.RequestID),
		CreatedAt: prepared.Now,
	}
	_ = auditBudget.UpdateKeys()

	auditJob := &models.AuditLogEntry{
		Actor:     prepared.InstanceSlug,
		Action:    "ai.job.queue",
		Target:    fmt.Sprintf("ai_job:%s", prepared.JobID),
		RequestID: strings.TrimSpace(req.RequestID),
		CreatedAt: prepared.Now,
	}
	_ = auditJob.UpdateKeys()

	pk := fmt.Sprintf("INSTANCE#%s", prepared.InstanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", prepared.Month)

	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Create(job)
		tx.Put(ledger)
		tx.Put(auditBudget)
		tx.Put(auditJob)
		return s.applyBudgetDebit(tx, updateBudget, prepared.Now, prepared.CreditsRequested, req.AllowOverage)
	})
	if theoryErrors.IsConditionFailed(err) {
		return s.handleDebitConditionFailed(ctx, prepared, pk, sk, prepared.CreditsRequested)
	}
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
	}

	latest, _ := s.refreshBudget(ctx, pk, sk)
	return queuedWithDebitResponse(prepared, latest, overageDebited), nil
}

func (s *Service) applyBudgetDebit(tx core.TransactionBuilder, budget *models.InstanceBudgetMonth, now time.Time, creditsRequested int64, allowOverage bool) error {
	if tx == nil || budget == nil {
		return nil
	}

	if allowOverage {
		tx.UpdateWithBuilder(budget, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", creditsRequested)
			ub.Set("UpdatedAt", now)
			return nil
		}, tabletheory.IfExists())
		return nil
	}

	tx.UpdateWithBuilder(budget, func(ub core.UpdateBuilder) error {
		ub.Add("UsedCredits", creditsRequested)
		ub.Set("UpdatedAt", now)
		return nil
	},
		tabletheory.IfExists(),
		tabletheory.ConditionExpression(
			"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
			map[string]any{
				":zero":  int64(0),
				":delta": creditsRequested,
			},
		),
	)
	return nil
}

func (s *Service) handleDebitConditionFailed(ctx context.Context, prepared getOrQueuePrepared, pk string, sk string, creditsRequested int64) (Response, error) {
	if cached := s.getCachedResult(ctx, prepared); cached != nil {
		return cacheHitResponse(prepared.JobID, prepared.ResultID, cached, creditsRequested), nil
	}

	if s.jobExists(ctx, prepared.JobID) {
		return alreadyQueuedResponse(prepared.JobID, prepared.ResultID, creditsRequested), nil
	}

	latest, err := s.refreshBudget(ctx, pk, sk)
	if err == nil && latest != nil {
		remaining := latest.IncludedCredits - latest.UsedCredits
		reason := "budget exceeded"
		if remaining >= creditsRequested {
			reason = "budget conflict"
		}
		return Response{
			Status:   JobStatusNotCheckedBudget,
			Cached:   false,
			JobID:    prepared.JobID,
			ResultID: prepared.ResultID,
			Budget: BudgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           reason,
				Month:            prepared.Month,
				IncludedCredits:  latest.IncludedCredits,
				UsedCredits:      latest.UsedCredits,
				RemainingCredits: remaining,
				RequestedCredits: creditsRequested,
				DebitedCredits:   0,
			},
		}, nil
	}

	return Response{
		Status:   JobStatusNotCheckedBudget,
		Cached:   false,
		JobID:    prepared.JobID,
		ResultID: prepared.ResultID,
		Budget: BudgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget exceeded",
			Month:            prepared.Month,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		},
	}, nil
}

func (s *Service) refreshBudget(ctx context.Context, pk string, sk string) (*models.InstanceBudgetMonth, error) {
	var latest models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", strings.TrimSpace(pk)).
		Where("SK", "=", strings.TrimSpace(sk)).
		ConsistentRead().
		First(&latest)
	if err != nil {
		return nil, err
	}
	return &latest, nil
}

func queuedWithDebitResponse(prepared getOrQueuePrepared, latest *models.InstanceBudgetMonth, overageDebited int64) Response {
	included := int64(0)
	used := int64(0)
	if latest != nil {
		included = latest.IncludedCredits
		used = latest.UsedCredits
	}

	remaining := included - used
	overBudget := remaining < 0 || overageDebited > 0
	reason := "debited"
	if overageDebited > 0 {
		reason = "overage"
	}

	return Response{
		Status:   JobStatusQueued,
		Cached:   false,
		JobID:    prepared.JobID,
		ResultID: prepared.ResultID,
		Budget: BudgetDecision{
			Allowed:          true,
			OverBudget:       overBudget,
			Reason:           reason,
			Month:            prepared.Month,
			IncludedCredits:  included,
			UsedCredits:      used,
			RemainingCredits: remaining,
			RequestedCredits: prepared.CreditsRequested,
			DebitedCredits:   prepared.CreditsRequested,
		},
	}
}
