package trust

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type createRenderRequest struct {
	URL            string `json:"url"`
	RetentionClass string `json:"retention_class,omitempty"` // benign|evidence
	RetentionDays  int    `json:"retention_days,omitempty"`
}

type renderArtifactResponse struct {
	Status string `json:"status"` // ok | queued | error
	Cached bool   `json:"cached"`

	RenderID       string `json:"render_id"`
	PolicyVersion  string `json:"policy_version"`
	NormalizedURL  string `json:"normalized_url"`
	ResolvedURL    string `json:"resolved_url,omitempty"`
	RetentionClass string `json:"retention_class"`

	ThumbnailURL  string `json:"thumbnail_url,omitempty"`
	SnapshotURL   string `json:"snapshot_url,omitempty"`
	TextPreview   string `json:"text_preview,omitempty"`
	ThumbnailType string `json:"thumbnail_content_type,omitempty"`
	SnapshotType  string `json:"snapshot_content_type,omitempty"`

	SummaryPolicyVersion string    `json:"summary_policy_version,omitempty"`
	Summary              string    `json:"summary,omitempty"`
	SummarizedAt         time.Time `json:"summarized_at,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	CreatedAt  time.Time `json:"created_at,omitempty"`
	RenderedAt time.Time `json:"rendered_at,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleCreateRender(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	if !instCfg.RendersEnabled {
		return apptheory.JSON(http.StatusOK, renderArtifactResponse{
			Status:         "error",
			Cached:         false,
			RenderID:       "",
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  "",
			RetentionClass: "",
			ErrorCode:      "disabled",
			ErrorMessage:   "renders disabled for instance",
			CreatedAt:      time.Now().UTC(),
			ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
		})
	}

	var req createRenderRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	normalized, _, err := normalizeLinkURL(req.URL)
	if err != nil {
		return nil, linkPreviewBadRequestError(err)
	}

	now := time.Now().UTC()

	// Resolve retention request (defaults when omitted).
	classDays, classOut := rendering.RetentionForClass(req.RetentionClass)
	retentionDays := req.RetentionDays
	if retentionDays <= 0 {
		retentionDays = classDays
	}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)

	// Cache hit: return existing (best-effort retention upgrade, no budget debit).
	if existing, err := s.store.GetRenderArtifact(ctx.Context(), renderID); err == nil && existing != nil {
		desiredExpiresAt := rendering.ExpiresAtForRetention(now, retentionDays)
		updated := false
		if existing.ExpiresAt.Before(desiredExpiresAt) {
			existing.ExpiresAt = desiredExpiresAt
			updated = true
		}
		if existing.RetentionClass != models.RenderRetentionClassEvidence && classOut == models.RenderRetentionClassEvidence {
			existing.RetentionClass = classOut
			updated = true
		}
		if updated {
			existing.RequestID = strings.TrimSpace(ctx.RequestID)
			existing.RequestedBy = strings.TrimSpace(ctx.AuthIdentity)
			_ = existing.UpdateKeys()
			_ = s.store.PutRenderArtifact(ctx.Context(), existing)
		}

		audit := &models.AuditLogEntry{
			Actor:     strings.TrimSpace(ctx.AuthIdentity),
			Action:    "render.request",
			Target:    fmt.Sprintf("render:%s", renderID),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

		hit := &models.UsageLedgerEntry{
			ID:                   billing.UsageLedgerEntryID(instanceSlug, now.Format("2006-01"), strings.TrimSpace(ctx.RequestID), "render.request", renderID, 0),
			InstanceSlug:         instanceSlug,
			Month:                now.Format("2006-01"),
			Module:               "render.request",
			Target:               renderID,
			Cached:               true,
			Reason:               "cache_hit",
			RequestID:            strings.TrimSpace(ctx.RequestID),
			RequestedCredits:     linkRenderCreditCost,
			ListCredits:          linkRenderCreditCost,
			PricingMultiplierBps: 10000,
			DebitedCredits:       0,
			BillingType:          models.BillingTypeNone,
			CreatedAt:            now,
		}
		_ = hit.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(hit).IfNotExists().Create()

		return apptheory.JSON(http.StatusOK, renderArtifactResponseFromModel(ctx, existing, true))
	}

	// Budget check (charge only on cache miss render requests).
	if strings.TrimSpace(s.cfg.PreviewQueueURL) == "" || s.queues == nil {
		return apptheory.JSON(http.StatusOK, renderArtifactResponse{
			Status:         "error",
			Cached:         false,
			RenderID:       renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalized,
			RetentionClass: classOut,
			ErrorCode:      "queue_not_configured",
			ErrorMessage:   "render queue not configured",
			CreatedAt:      now,
			ExpiresAt:      now.Add(5 * time.Minute),
		})
	}

	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	if resp, err := s.debitBudgetForCreateRender(ctx, instanceSlug, now, allowOverage, renderID, normalized, classOut); resp != nil || err != nil {
		return resp, err
	}

	artifact, queued, err := s.queueRender(ctx, normalized, classOut, retentionDays)
	if err != nil {
		return nil, err
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "render.request",
		Target:    fmt.Sprintf("render:%s", renderID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	if queued && artifact != nil {
		auditQueue := &models.AuditLogEntry{
			Actor:     strings.TrimSpace(ctx.AuthIdentity),
			Action:    "render.queue",
			Target:    fmt.Sprintf("render:%s", strings.TrimSpace(artifact.ID)),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: now,
		}
		_ = auditQueue.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(auditQueue).Create()
	}

	return apptheory.JSON(http.StatusOK, renderArtifactResponseFromModel(ctx, artifact, !queued))
}

func (s *Server) debitBudgetForCreateRender(
	ctx *apptheory.Context,
	instanceSlug string,
	now time.Time,
	allowOverage bool,
	renderID string,
	normalizedURL string,
	retentionClass string,
) (*apptheory.Response, error) {
	month := now.Format("2006-01")
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		return apptheory.JSON(http.StatusOK, renderArtifactResponse{
			Status:         "error",
			Cached:         false,
			RenderID:       renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalizedURL,
			RetentionClass: retentionClass,
			ErrorCode:      "not_checked_budget",
			ErrorMessage:   "budget not configured",
			CreatedAt:      now,
			ExpiresAt:      now.Add(5 * time.Minute),
		})
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < linkRenderCreditCost && !allowOverage {
		return apptheory.JSON(http.StatusOK, renderArtifactResponse{
			Status:         "error",
			Cached:         false,
			RenderID:       renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalizedURL,
			RetentionClass: retentionClass,
			ErrorCode:      "not_checked_budget",
			ErrorMessage:   "budget exceeded",
			CreatedAt:      now,
			ExpiresAt:      now.Add(5 * time.Minute),
		})
	}

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, linkRenderCreditCost)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)
	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "render.request", renderID, linkRenderCreditCost),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "render.request",
		Target:                 renderID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(ctx.RequestID),
		RequestedCredits:       linkRenderCreditCost,
		ListCredits:            linkRenderCreditCost,
		PricingMultiplierBps:   10000,
		DebitedCredits:         linkRenderCreditCost,
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
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = auditBudget.UpdateKeys()

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		if allowOverage {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", linkRenderCreditCost)
				ub.Set("UpdatedAt", now)
				return nil
			}, tabletheory.IfExists())
		} else {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", linkRenderCreditCost)
				ub.Set("UpdatedAt", now)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.ConditionExpression(
					"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
					map[string]any{
						":zero":  int64(0),
						":delta": linkRenderCreditCost,
					},
				),
			)
		}
		tx.Put(ledger)
		tx.Put(auditBudget)
		return nil
	})
	if err != nil {
		return apptheory.JSON(http.StatusOK, renderArtifactResponse{
			Status:         "error",
			Cached:         false,
			RenderID:       renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalizedURL,
			RetentionClass: retentionClass,
			ErrorCode:      "not_checked_budget",
			ErrorMessage:   "budget exceeded",
			CreatedAt:      now,
			ExpiresAt:      now.Add(5 * time.Minute),
		})
	}

	return nil, nil
}

var renderIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleGetRender(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	id := strings.TrimSpace(ctx.Param("renderId"))
	if !renderIDRE.MatchString(id) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid render id"}
	}

	item, err := s.store.GetRenderArtifact(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "render not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, renderArtifactResponseFromModel(ctx, item, true))
}

func (s *Server) handleGetRenderThumbnail(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.artifacts == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id := strings.TrimSpace(ctx.Param("renderId"))
	if !renderIDRE.MatchString(id) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid render id"}
	}

	item, err := s.store.GetRenderArtifact(ctx.Context(), id)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "thumbnail not found"}
	}
	key := strings.TrimSpace(item.ThumbnailObjectKey)
	if key == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "thumbnail not found"}
	}

	body, contentType, etag, err := s.artifacts.GetObject(ctx.Context(), key, linkPreviewMaxImageBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "thumbnail not found"}
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(body)
	}

	resp := apptheory.Binary(http.StatusOK, body, contentType)
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"public, max-age=86400, immutable"}
	if strings.TrimSpace(etag) != "" {
		resp.Headers["etag"] = []string{etag}
	}
	return resp, nil
}

func (s *Server) handleGetRenderSnapshot(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.artifacts == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	id := strings.TrimSpace(ctx.Param("renderId"))
	if !renderIDRE.MatchString(id) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid render id"}
	}

	item, err := s.store.GetRenderArtifact(ctx.Context(), id)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "snapshot not found"}
	}
	key := strings.TrimSpace(item.SnapshotObjectKey)
	if key == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "snapshot not found"}
	}

	body, contentType, etag, err := s.artifacts.GetObject(ctx.Context(), key, 512*1024)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "snapshot not found"}
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "text/plain; charset=utf-8"
	}

	resp := &apptheory.Response{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"content-type":  {contentType},
			"cache-control": {"private, max-age=600"},
		},
		Body: body,
	}
	if strings.TrimSpace(etag) != "" {
		resp.Headers["etag"] = []string{etag}
	}
	return resp, nil
}

func renderArtifactResponseFromModel(ctx *apptheory.Context, item *models.RenderArtifact, cached bool) renderArtifactResponse {
	out := renderArtifactResponse{
		Cached: cached,

		RenderID:       strings.TrimSpace(item.ID),
		PolicyVersion:  strings.TrimSpace(item.PolicyVersion),
		NormalizedURL:  strings.TrimSpace(item.NormalizedURL),
		ResolvedURL:    strings.TrimSpace(item.ResolvedURL),
		RetentionClass: strings.TrimSpace(item.RetentionClass),

		TextPreview:   strings.TrimSpace(item.TextPreview),
		ThumbnailType: strings.TrimSpace(item.ThumbnailContentType),
		SnapshotType:  strings.TrimSpace(item.SnapshotContentType),

		SummaryPolicyVersion: strings.TrimSpace(item.SummaryPolicyVersion),
		Summary:              strings.TrimSpace(item.Summary),
		SummarizedAt:         item.SummarizedAt,

		ErrorCode:    strings.TrimSpace(item.ErrorCode),
		ErrorMessage: strings.TrimSpace(item.ErrorMessage),

		CreatedAt:  item.CreatedAt,
		RenderedAt: item.RenderedAt,
		ExpiresAt:  item.ExpiresAt,
	}

	if out.ErrorCode != "" {
		out.Status = "error"
	} else if strings.TrimSpace(item.ThumbnailObjectKey) != "" || strings.TrimSpace(item.SnapshotObjectKey) != "" {
		out.Status = "ok"
	} else {
		out.Status = "queued"
	}

	base := requestBaseURL(ctx)
	if out.RenderID != "" {
		thumbPath := "/api/v1/renders/" + out.RenderID + "/thumbnail"
		snapPath := "/api/v1/renders/" + out.RenderID + "/snapshot"
		if base != "" {
			out.ThumbnailURL = base + thumbPath
			out.SnapshotURL = base + snapPath
		} else {
			out.ThumbnailURL = thumbPath
			out.SnapshotURL = snapPath
		}
	}

	return out
}
