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

type Response struct {
	Status JobStatus `json:"status"`
	Cached bool      `json:"cached"`

	JobID    string `json:"job_id,omitempty"`
	ResultID string `json:"result_id,omitempty"`

	Result *models.AIResult `json:"result,omitempty"`
	Budget BudgetDecision   `json:"budget"`
}

type Service struct {
	store *store.Store
}

func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

// GetOrQueue returns a cached AIResult when available, otherwise queues an AIJob (budgeted) and returns queued.
// It never performs provider/tool calls.
func (s *Service) GetOrQueue(ctx context.Context, req Request) (Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, fmt.Errorf("ai service not initialized")
	}

	instanceSlug := strings.TrimSpace(req.InstanceSlug)
	if instanceSlug == "" {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid instance"}}, fmt.Errorf("instanceSlug is required")
	}

	module := strings.ToLower(strings.TrimSpace(req.Module))
	policyVersion := strings.TrimSpace(req.PolicyVersion)
	modelSet := strings.TrimSpace(req.ModelSet)
	if module == "" || policyVersion == "" || modelSet == "" {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid module"}}, fmt.Errorf("module, policyVersion, and modelSet are required")
	}

	scope := req.CacheScope
	if strings.TrimSpace(string(scope)) == "" {
		scope = CacheScopeInstance
	}

	inputsJSON, err := CanonicalJSON(req.Inputs)
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid inputs"}}, err
	}
	inputsHash, err := InputsHash(req.Inputs)
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid inputs"}}, err
	}

	scopeKey := instanceSlug
	if strings.EqualFold(string(scope), string(CacheScopeGlobal)) {
		scopeKey = ""
	}

	resultID, err := CacheKey(scope, scopeKey, module, policyVersion, modelSet, inputsHash)
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "invalid cache key"}}, err
	}
	jobID := resultID

	now := time.Now().UTC()
	month := now.Format("2006-01")

	creditsRequested := billing.PricedCredits(req.BaseCredits, req.PricingMultiplierBps)

	// Cache hit path (no debit, no job enqueue).
	if cached, err := s.store.GetAIResult(ctx, resultID); err == nil && cached != nil {
		if !cached.ExpiresAt.IsZero() && cached.ExpiresAt.Before(now) {
			// Treat expired rows as cache miss (sweeper may not have deleted yet).
		} else {
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
			}, nil
		}
	}

	// Already queued path (no debit).
	if existingJob, err := s.store.GetAIJob(ctx, jobID); err == nil && existingJob != nil {
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
		}, nil
	}

	// No-op jobs: create a queued job without debiting.
	if creditsRequested <= 0 {
		ttl := req.JobTTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		job := &models.AIJob{
			ID:            jobID,
			InstanceSlug:  instanceSlug,
			Module:        module,
			PolicyVersion: policyVersion,
			ModelSet:      modelSet,
			CacheScope:    string(scope),
			ScopeKey:      scopeKey,
			InputsHash:    inputsHash,
			InputsJSON:    inputsJSON,
			Evidence:      append([]models.AIEvidenceRef(nil), req.Evidence...),
			Status:        models.AIJobStatusQueued,
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(ttl),
			RequestID:     strings.TrimSpace(req.RequestID),
		}
		_ = job.UpdateKeys()

		if err := s.store.DB.WithContext(ctx).Model(job).IfNotExists().Create(); err != nil {
			// Best-effort: if it already exists, treat as queued.
			if theoryErrors.IsConditionFailed(err) {
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
				}, nil
			}
			return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
		}

		return Response{
			Status:   JobStatusQueued,
			Cached:   false,
			JobID:    jobID,
			ResultID: resultID,
			Budget: BudgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "queued_no_charge",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}, nil
	}

	// Budget pre-check to avoid ambiguous transaction condition failures.
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)

	var budget models.InstanceBudgetMonth
	err = s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
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
		}, nil
	}
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < creditsRequested && !req.AllowOverage {
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
		}, nil
	}

	ttl := req.JobTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	job := &models.AIJob{
		ID:            jobID,
		InstanceSlug:  instanceSlug,
		Module:        module,
		PolicyVersion: policyVersion,
		ModelSet:      modelSet,
		CacheScope:    string(scope),
		ScopeKey:      scopeKey,
		InputsHash:    inputsHash,
		InputsJSON:    inputsJSON,
		Evidence:      append([]models.AIEvidenceRef(nil), req.Evidence...),
		Status:        models.AIJobStatusQueued,
		Attempts:      0,
		MaxAttempts:   3,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(ttl),
		RequestID:     strings.TrimSpace(req.RequestID),
	}
	_ = job.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = updateBudget.UpdateKeys()

	includedDebited, overageDebited := billing.BillingPartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsRequested)
	billingType := billing.BillingTypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(req.RequestID), module, jobID, creditsRequested),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 module,
		Target:                 jobID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(req.RequestID),
		RequestedCredits:       creditsRequested,
		DebitedCredits:         creditsRequested,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		CreatedAt:              now,
	}
	_ = ledger.UpdateKeys()

	auditBudget := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", instanceSlug, month),
		RequestID: strings.TrimSpace(req.RequestID),
		CreatedAt: now,
	}
	_ = auditBudget.UpdateKeys()

	auditJob := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "ai.job.queue",
		Target:    fmt.Sprintf("ai_job:%s", jobID),
		RequestID: strings.TrimSpace(req.RequestID),
		CreatedAt: now,
	}
	_ = auditJob.UpdateKeys()

	err = s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if req.AllowOverage {
			tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", creditsRequested)
				ub.Set("UpdatedAt", now)
				return nil
			}, tabletheory.IfExists())
		} else {
			tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
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
		}
		tx.Create(job)
		tx.Put(ledger)
		tx.Put(auditBudget)
		tx.Put(auditJob)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		// If the result already exists, treat it as cache hit (no debit happened due to txn rollback).
		if cached, err2 := s.store.GetAIResult(ctx, resultID); err2 == nil && cached != nil {
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
					Month:            month,
					RequestedCredits: creditsRequested,
					DebitedCredits:   0,
				},
			}, nil
		}

		// If the job exists, treat it as already queued.
		if _, err2 := s.store.GetAIJob(ctx, jobID); err2 == nil {
			return Response{
				Status:   JobStatusQueued,
				Cached:   false,
				JobID:    jobID,
				ResultID: resultID,
				Budget: BudgetDecision{
					Allowed:          true,
					OverBudget:       false,
					Reason:           "already_queued",
					Month:            month,
					RequestedCredits: creditsRequested,
					DebitedCredits:   0,
				},
			}, nil
		}

		// Otherwise, assume we raced with another debit and are now over budget.
		var latest models.InstanceBudgetMonth
		if err3 := s.store.DB.WithContext(ctx).
			Model(&models.InstanceBudgetMonth{}).
			Where("PK", "=", pk).
			Where("SK", "=", sk).
			ConsistentRead().
			First(&latest); err3 == nil {
			remaining = latest.IncludedCredits - latest.UsedCredits
			reason := "budget exceeded"
			if remaining >= creditsRequested {
				reason = "budget conflict"
			}
			return Response{
				Status:   JobStatusNotCheckedBudget,
				Cached:   false,
				JobID:    jobID,
				ResultID: resultID,
				Budget: BudgetDecision{
					Allowed:          false,
					OverBudget:       true,
					Reason:           reason,
					Month:            month,
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
		}, nil
	}
	if err != nil {
		return Response{Status: JobStatusError, Budget: BudgetDecision{Allowed: true, Reason: "internal error"}}, err
	}

	// Best-effort refreshed budget for reporting.
	var latest models.InstanceBudgetMonth
	_ = s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest)

	remaining = latest.IncludedCredits - latest.UsedCredits
	overBudget := remaining < 0 || overageDebited > 0
	reason := "debited"
	if overageDebited > 0 {
		reason = "overage"
	}

	return Response{
		Status:   JobStatusQueued,
		Cached:   false,
		JobID:    jobID,
		ResultID: resultID,
		Budget: BudgetDecision{
			Allowed:          true,
			OverBudget:       overBudget,
			Reason:           reason,
			Month:            month,
			IncludedCredits:  latest.IncludedCredits,
			UsedCredits:      latest.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: creditsRequested,
			DebitedCredits:   creditsRequested,
		},
	}, nil
}
