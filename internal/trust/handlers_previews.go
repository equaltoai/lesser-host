package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type linkPreviewRequest struct {
	URL          string `json:"url"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
}

type linkPreviewResponse struct {
	Status string `json:"status"` // ok | blocked | error
	Cached bool   `json:"cached"`

	ID            string `json:"id"`
	PolicyVersion string `json:"policy_version"`

	NormalizedURL string   `json:"normalized_url"`
	ResolvedURL   string   `json:"resolved_url,omitempty"`
	RedirectChain []string `json:"redirect_chain,omitempty"`

	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	ImageID  string `json:"image_id,omitempty"`
	ImageURL string `json:"image_url,omitempty"`

	Render *renderArtifactResponse `json:"render,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	FetchedAt time.Time `json:"fetched_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func requireLinkPreviewAuth(s *Server, ctx *apptheory.Context) (string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	return instanceSlug, nil
}

func parseLinkPreviewRequestInput(ctx *apptheory.Context) (linkPreviewRequest, error) {
	var req linkPreviewRequest
	if err := parseJSON(ctx, &req); err != nil {
		return linkPreviewRequest{}, err
	}
	return req, nil
}

func parseLinkPreviewNormalizedURL(raw string) (string, *apptheory.AppError) {
	normalized, _, err := normalizeLinkURL(raw)
	if err == nil {
		return normalized, nil
	}

	if appErr, ok := linkPreviewBadRequestError(err).(*apptheory.AppError); ok {
		return "", appErr
	}
	return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid url"}
}

func (s *Server) maybeServeCachedLinkPreview(
	ctx *apptheory.Context,
	instanceSlug string,
	instCfg instanceTrustConfig,
	previewID string,
	normalizedURL string,
	forceRefresh bool,
) (*linkPreviewResponse, bool, *apptheory.AppError) {
	if forceRefresh {
		return nil, false, nil
	}

	item, ok, err := s.getFreshLinkPreview(ctx.Context(), previewID, time.Now().UTC())
	if err != nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !ok || item == nil {
		return nil, false, nil
	}

	resp := linkPreviewResponseFromModel(ctx, item, true)
	if instCfg.RendersEnabled {
		s.maybeAttachPreviewRender(ctx, instanceSlug, instCfg.RenderPolicy, instCfg.OveragePolicy, normalizedURL, &resp)
	}

	return &resp, true, nil
}

func (s *Server) fetchAndStoreLinkPreview(
	ctx *apptheory.Context,
	instanceSlug string,
	previewID string,
	normalizedURL string,
) (*models.LinkPreview, *apptheory.AppError) {
	fetched, fetchErr := fetchLinkPreview(ctx.Context(), nil, normalizedURL)

	now := time.Now().UTC()
	item := &models.LinkPreview{
		ID:            previewID,
		PolicyVersion: linkPreviewPolicyVersion,
		NormalizedURL: strings.TrimSpace(normalizedURL),
		ResolvedURL:   strings.TrimSpace(fetched.ResolvedURL),
		RedirectChain: append([]string(nil), fetched.RedirectChain...),
		Title:         strings.TrimSpace(fetched.Title),
		Description:   strings.TrimSpace(fetched.Description),
		FetchedAt:     now,
		ExpiresAt:     now.Add(linkPreviewCacheTTL),
		StoredAt:      now,
		StoredBy:      instanceSlug,
		RequestID:     strings.TrimSpace(ctx.RequestID),
		SourceType:    "trust-api",
	}

	if fetchErr != nil {
		applyLinkPreviewFetchError(item, fetchErr)
	} else if strings.TrimSpace(fetched.ImageURL) != "" && s.artifacts != nil {
		// Fetch and store image if available.
		imageID, objectKey := s.tryStorePreviewImage(ctx.Context(), fetched.ImageURL)
		if imageID != "" && objectKey != "" {
			item.ImageID = imageID
			item.ImageObjectKey = objectKey
		}
	}

	if err := s.putLinkPreviewWithAudit(ctx.Context(), instanceSlug, item, previewID, now, strings.TrimSpace(ctx.RequestID)); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store preview"}
	}

	return item, nil
}

func (s *Server) handleLinkPreview(ctx *apptheory.Context) (*apptheory.Response, error) {
	instanceSlug, appErr := requireLinkPreviewAuth(s, ctx)
	if appErr != nil {
		return nil, appErr
	}

	req, err := parseLinkPreviewRequestInput(ctx)
	if err != nil {
		return nil, err
	}

	normalized, appErr := parseLinkPreviewNormalizedURL(req.URL)
	if appErr != nil {
		return nil, appErr
	}

	previewID := linkPreviewID(linkPreviewPolicyVersion, normalized)

	// Respect per-instance config toggle (default: enabled).
	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	if !instCfg.HostedPreviewsEnabled {
		return apptheory.JSON(http.StatusOK, linkPreviewResponseDisabled(previewID, normalized))
	}

	cachedResp, ok, cacheErr := s.maybeServeCachedLinkPreview(ctx, instanceSlug, instCfg, previewID, normalized, req.ForceRefresh)
	if cacheErr != nil {
		return nil, cacheErr
	}
	if ok {
		return apptheory.JSON(http.StatusOK, *cachedResp)
	}

	item, appErr := s.fetchAndStoreLinkPreview(ctx, instanceSlug, previewID, normalized)
	if appErr != nil {
		return nil, appErr
	}

	resp := linkPreviewResponseFromModel(ctx, item, false)
	if instCfg.RendersEnabled {
		s.maybeAttachPreviewRender(ctx, instanceSlug, instCfg.RenderPolicy, instCfg.OveragePolicy, normalized, &resp)
	}
	return apptheory.JSON(http.StatusOK, resp)
}

func linkPreviewBadRequestError(err error) error {
	if pe, ok := err.(*linkPreviewError); ok && pe.Code == "invalid_url" {
		return &apptheory.AppError{Code: "app.bad_request", Message: pe.Message}
	}
	return &apptheory.AppError{Code: "app.bad_request", Message: "invalid url"}
}

func linkPreviewResponseDisabled(previewID, normalizedURL string) linkPreviewResponse {
	now := time.Now().UTC()
	return linkPreviewResponse{
		Status:        "disabled",
		Cached:        false,
		ID:            strings.TrimSpace(previewID),
		PolicyVersion: linkPreviewPolicyVersion,
		NormalizedURL: strings.TrimSpace(normalizedURL),
		ErrorCode:     "disabled",
		ErrorMessage:  "hosted previews disabled for instance",
		FetchedAt:     now,
		ExpiresAt:     now.Add(5 * time.Minute),
	}
}

func (s *Server) getFreshLinkPreview(ctx context.Context, id string, now time.Time) (*models.LinkPreview, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, false, fmt.Errorf("store not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}

	item, err := s.store.GetLinkPreview(ctx, id)
	if err == nil && item != nil && !item.ExpiresAt.IsZero() && item.ExpiresAt.After(now) {
		return item, true, nil
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, false, err
	}
	return nil, false, nil
}

func applyLinkPreviewFetchError(item *models.LinkPreview, fetchErr error) {
	if item == nil {
		return
	}
	if pe, ok := fetchErr.(*linkPreviewError); ok {
		item.ErrorCode = pe.Code
		item.ErrorMessage = pe.Message
		return
	}
	item.ErrorCode = "fetch_failed"
	item.ErrorMessage = "fetch failed"
}

func (s *Server) putLinkPreviewWithAudit(ctx context.Context, instanceSlug string, item *models.LinkPreview, previewID string, now time.Time, requestID string) error {
	if s == nil || s.store == nil || s.store.DB == nil || item == nil {
		return fmt.Errorf("store not configured")
	}
	if err := item.UpdateKeys(); err != nil {
		return err
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(instanceSlug),
		Action:    "link_preview.fetch",
		Target:    "link_preview:" + strings.TrimSpace(previewID),
		RequestID: strings.TrimSpace(requestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(item)
		tx.Put(audit)
		return nil
	})
}

func (s *Server) handleGetLinkPreview(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	item, err := s.store.GetLinkPreview(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "preview not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	renderPolicy := instCfg.RenderPolicy

	resp := linkPreviewResponseFromModel(ctx, item, true)
	if instCfg.RendersEnabled {
		s.maybeAttachPreviewRender(ctx, instanceSlug, renderPolicy, instCfg.OveragePolicy, strings.TrimSpace(item.NormalizedURL), &resp)
	}
	return apptheory.JSON(http.StatusOK, resp)
}

var imageIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleGetLinkPreviewImage(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.artifacts == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	imageID := strings.TrimSpace(ctx.Param("imageId"))
	if !imageIDRE.MatchString(imageID) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid image id"}
	}

	key := linkPreviewImageObjectKey(imageID)
	body, contentType, etag, err := s.artifacts.GetObject(ctx.Context(), key, linkPreviewMaxImageBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "image not found"}
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

func (s *Server) tryStorePreviewImage(ctx context.Context, rawImageURL string) (string, string) {
	rawImageURL = strings.TrimSpace(rawImageURL)
	if rawImageURL == "" || s == nil || s.artifacts == nil {
		return "", ""
	}

	imgNormalized, imgURL, err := normalizeLinkURL(rawImageURL)
	if err != nil {
		return "", ""
	}
	if validateErr := validateOutboundURL(ctx, nil, imgURL); validateErr != nil {
		return "", ""
	}

	client := newPreviewHTTPClient(linkPreviewFetchTimeout)
	_, _, body, contentType, err := fetchWithRedirects(ctx, nil, client, imgURL, linkPreviewMaxRedirects, linkPreviewMaxImageBytes)
	if err != nil {
		return "", ""
	}

	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return "", ""
	}

	imageID := imageIDFromNormalizedURL(imgNormalized)
	key := linkPreviewImageObjectKey(imageID)
	if err := s.artifacts.PutObject(ctx, key, body, contentType, "public, max-age=86400, immutable"); err != nil {
		return "", ""
	}
	return imageID, key
}

func normalizePreviewRenderPolicy(renderPolicy string) string {
	renderPolicy = strings.ToLower(strings.TrimSpace(renderPolicy))
	switch renderPolicy {
	case "always", "suspicious":
		return renderPolicy
	default:
		return "suspicious"
	}
}

func previewRenderEligible(s *Server, ctx *apptheory.Context, normalizedURL string, resp *linkPreviewResponse) bool {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || resp == nil {
		return false
	}
	if strings.TrimSpace(normalizedURL) == "" {
		return false
	}
	return strings.TrimSpace(resp.Status) == "ok"
}

func (s *Server) attachCachedPreviewRender(ctx *apptheory.Context, instanceSlug string, renderID string, resp *linkPreviewResponse) bool {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || resp == nil {
		return false
	}
	if strings.TrimSpace(renderID) == "" {
		return false
	}

	existing, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
	if err != nil || existing == nil {
		return false
	}

	hitNow := time.Now().UTC()
	hitMonth := hitNow.Format("2006-01")
	hit := &models.UsageLedgerEntry{
		ID:                   billing.UsageLedgerEntryID(instanceSlug, hitMonth, strings.TrimSpace(ctx.RequestID), "link_preview_render", renderID, 0),
		InstanceSlug:         instanceSlug,
		Month:                hitMonth,
		Module:               "link_preview_render",
		Target:               renderID,
		Cached:               true,
		Reason:               "cache_hit",
		RequestID:            strings.TrimSpace(ctx.RequestID),
		RequestedCredits:     linkRenderCreditCost,
		ListCredits:          linkRenderCreditCost,
		PricingMultiplierBps: 10000,
		DebitedCredits:       0,
		BillingType:          models.BillingTypeNone,
		CreatedAt:            hitNow,
	}
	_ = hit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(hit).IfNotExists().Create()

	r := renderArtifactResponseFromModel(ctx, existing, true)
	resp.Render = &r
	return true
}

func previewRenderQueueReady(s *Server) bool {
	if s == nil || s.queues == nil {
		return false
	}
	return strings.TrimSpace(s.cfg.PreviewQueueURL) != ""
}

func (s *Server) debitBudgetForPreviewRender(ctx *apptheory.Context, instanceSlug string, overagePolicy string, renderID string, now time.Time) bool {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return false
	}
	if strings.TrimSpace(instanceSlug) == "" || strings.TrimSpace(renderID) == "" {
		return false
	}

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
	if err != nil {
		return false
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	allowOverage := strings.ToLower(strings.TrimSpace(overagePolicy)) == "allow"
	if remaining < linkRenderCreditCost && !allowOverage {
		return false
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
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "link_preview_render", renderID, linkRenderCreditCost),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "link_preview_render",
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

	budgetUpdateConditions := []core.TransactCondition{tabletheory.IfExists()}
	if !allowOverage {
		budgetUpdateConditions = append(budgetUpdateConditions,
			tabletheory.ConditionExpression(
				"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
				map[string]any{
					":zero":  int64(0),
					":delta": linkRenderCreditCost,
				},
			),
		)
	}

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", linkRenderCreditCost)
			ub.Set("UpdatedAt", now)
			return nil
		}, budgetUpdateConditions...)
		tx.Put(ledger)
		tx.Put(auditBudget)
		return nil
	})
	return err == nil
}

func (s *Server) auditPreviewRenderQueuedBestEffort(ctx *apptheory.Context, instanceSlug string, artifact *models.RenderArtifact, now time.Time) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || artifact == nil {
		return
	}
	audit := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "render.queue",
		Target:    fmt.Sprintf("render:%s", strings.TrimSpace(artifact.ID)),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()
}

func (s *Server) maybeAttachPreviewRender(ctx *apptheory.Context, instanceSlug string, renderPolicy string, overagePolicy string, normalizedURL string, resp *linkPreviewResponse) {
	if !previewRenderEligible(s, ctx, normalizedURL, resp) {
		return
	}

	renderPolicy = normalizePreviewRenderPolicy(renderPolicy)

	analysis := analyzeLinkSafetyBasic(ctx.Context(), nil, normalizedURL)
	risk := strings.ToLower(strings.TrimSpace(analysis.Risk))
	if !shouldRenderLink(renderPolicy, risk) {
		return
	}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalizedURL)

	if s.attachCachedPreviewRender(ctx, instanceSlug, renderID, resp) {
		return
	}

	if !previewRenderQueueReady(s) {
		return
	}

	now := time.Now().UTC()
	if !s.debitBudgetForPreviewRender(ctx, instanceSlug, overagePolicy, renderID, now) {
		return
	}

	retentionClass := retentionClassForRisk(risk)
	artifact, queued, err := s.queueRender(ctx, normalizedURL, retentionClass, 0)
	if err != nil || artifact == nil {
		return
	}

	if queued {
		s.auditPreviewRenderQueuedBestEffort(ctx, instanceSlug, artifact, now)
	}

	r := renderArtifactResponseFromModel(ctx, artifact, !queued)
	resp.Render = &r
}

func linkPreviewResponseFromModel(ctx *apptheory.Context, item *models.LinkPreview, cached bool) linkPreviewResponse {
	resp := linkPreviewResponse{
		Cached: cached,

		ID:            strings.TrimSpace(item.ID),
		PolicyVersion: strings.TrimSpace(item.PolicyVersion),
		NormalizedURL: strings.TrimSpace(item.NormalizedURL),
		ResolvedURL:   strings.TrimSpace(item.ResolvedURL),
		RedirectChain: append([]string(nil), item.RedirectChain...),
		Title:         strings.TrimSpace(item.Title),
		Description:   strings.TrimSpace(item.Description),
		ImageID:       strings.TrimSpace(item.ImageID),
		ErrorCode:     strings.TrimSpace(item.ErrorCode),
		ErrorMessage:  strings.TrimSpace(item.ErrorMessage),
		FetchedAt:     item.FetchedAt,
		ExpiresAt:     item.ExpiresAt,
	}

	switch resp.ErrorCode {
	case "":
		resp.Status = "ok"
	case "blocked_ssrf":
		resp.Status = "blocked"
	case "disabled":
		resp.Status = "disabled"
	default:
		resp.Status = "error"
	}

	if resp.ImageID != "" {
		base := requestBaseURL(ctx)
		path := "/api/v1/previews/images/" + resp.ImageID
		if base != "" {
			resp.ImageURL = base + path
		} else {
			resp.ImageURL = path
		}
	}
	return resp
}

func requestBaseURL(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	host := strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "x-forwarded-host"))
	if host == "" {
		host = strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "host"))
	}
	if host == "" {
		return ""
	}
	proto := strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "x-forwarded-proto"))
	if proto == "" {
		proto = "https"
	}
	return proto + "://" + host
}

func linkPreviewImageObjectKey(imageID string) string {
	return "link-previews/images/" + strings.TrimSpace(imageID)
}

func imageIDFromNormalizedURL(normalized string) string {
	sum := sha256.Sum256([]byte("img:" + normalized))
	return hex.EncodeToString(sum[:])
}
