package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulPublicAgentResponse struct {
	Version    string                      `json:"version"`
	Agent      models.SoulAgentIdentity    `json:"agent"`
	Reputation *models.SoulAgentReputation `json:"reputation,omitempty"`
}

func (s *Server) handleSoulPublicGetAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	rep, _ := s.getSoulAgentReputation(ctx.Context(), agentIDHex)

	resp, err := apptheory.JSON(http.StatusOK, soulPublicAgentResponse{
		Version:    "1",
		Agent:      *identity,
		Reputation: rep,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func (s *Server) getSoulAgentReputation(ctx context.Context, agentIDHex string) (*models.SoulAgentReputation, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, errors.New("agent id is required")
	}

	var item models.SoulAgentReputation
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentReputation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "=", "REPUTATION").
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) handleSoulPublicGetRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	key := soulRegistrationS3Key(agentIDHex)
	body, contentType, etag, err := s.soulPacks.GetObject(ctx.Context(), key, 512*1024)
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to fetch registration"}
	}

	resp := &apptheory.Response{Status: http.StatusOK, Body: body}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	resp.Headers["content-type"] = []string{contentType}
	if strings.TrimSpace(etag) != "" {
		resp.Headers["etag"] = []string{strings.TrimSpace(etag)}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=300")
	return resp, nil
}

type soulPublicReputationResponse struct {
	Version    string                     `json:"version"`
	Reputation models.SoulAgentReputation `json:"reputation"`
}

func (s *Server) handleSoulPublicGetReputation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	rep, err := s.getSoulAgentReputation(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicReputationResponse{Version: "1", Reputation: *rep})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

type soulPublicValidationsResponse struct {
	Version     string                             `json:"version"`
	Validations []models.SoulAgentValidationRecord `json:"validations"`
	Count       int                                `json:"count"`
	HasMore     bool                               `json:"has_more"`
	NextCursor  string                             `json:"next_cursor,omitempty"`
}

func (s *Server) handleSoulPublicGetValidations(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.SoulAgentValidationRecord
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentValidationRecord{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "VALIDATION#").
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list validations"}
	}

	out := make([]models.SoulAgentValidationRecord, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}

	resp, err := apptheory.JSON(http.StatusOK, soulPublicValidationsResponse{
		Version:     "1",
		Validations: out,
		Count:       len(out),
		HasMore:     hasMore,
		NextCursor:  nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

type soulSearchResult struct {
	AgentID string `json:"agent_id"`
	Domain  string `json:"domain"`
	LocalID string `json:"local_id"`
}

type soulSearchResponse struct {
	Version    string             `json:"version"`
	Results    []soulSearchResult `json:"results"`
	Count      int                `json:"count"`
	HasMore    bool               `json:"has_more"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type soulSearchIndexEntry struct {
	AgentID    string `json:"agent_id"`
	Domain     string `json:"domain"`
	LocalID    string `json:"local_id"`
	ClaimLevel string `json:"claim_level,omitempty"`
}

func soulSearchResultFromEntry(entry soulSearchIndexEntry) soulSearchResult {
	return soulSearchResult{
		AgentID: entry.AgentID,
		Domain:  entry.Domain,
		LocalID: entry.LocalID,
	}
}

func (s *Server) handleSoulPublicSearch(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	params, appErr := parseSoulPublicSearchParams(ctx)
	if appErr != nil {
		return nil, appErr
	}

	results, hasMore, nextCursor, appErr := s.searchSoulAgents(ctx.Context(), params)
	if appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulSearchResponse{
		Version:    "1",
		Results:    results,
		Count:      len(results),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=30")
	return resp, nil
}

type soulPublicSearchParams struct {
	Query      string
	Capability string
	ClaimLevel string
	Boundary   string
	Status     string
	Cursor     string
	Limit      int
}

func parseSoulPublicSearchParams(ctx *apptheory.Context) (soulPublicSearchParams, *apptheory.AppError) {
	if ctx == nil {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	q := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "q"))
	cap := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "capability")))
	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	claimLevel := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "claimLevel")))
	if claimLevel == "" {
		claimLevel = strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "claim_level")))
	}
	boundary := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "boundary")))
	status := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "status")))

	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	if q == "" && cap == "" {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.bad_request", Message: "q or capability is required"}
	}

	if claimLevel != "" && cap == "" {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.bad_request", Message: "claimLevel requires capability"}
	}

	return soulPublicSearchParams{
		Query:      q,
		Capability: cap,
		ClaimLevel: claimLevel,
		Boundary:   boundary,
		Status:     status,
		Cursor:     cursor,
		Limit:      limit,
	}, nil
}

func (s *Server) querySoulSearchIndexEntries(ctx context.Context, q string, cap string, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(cap) != "" {
		return s.querySoulSearchByCapability(ctx, q, cap, cursor, limit)
	}
	return s.querySoulSearchByDomain(ctx, q, cursor, limit)
}

func (s *Server) querySoulSearchByCapability(ctx context.Context, q string, cap string, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	capNorm := normalizeSoulCapabilitiesLoose([]string{cap})
	if len(capNorm) == 0 {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid capability"}
	}
	cap = capNorm[0]

	skPrefix := ""
	if strings.TrimSpace(q) != "" {
		domain, local, appErr := parseSoulSearchQuery(q)
		if appErr != nil {
			return nil, false, "", appErr
		}
		if domain == "" {
			return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "domain is required when filtering by local id"}
		}
		skPrefix = fmt.Sprintf("DOMAIN#%s#", domain)
		if local != "" {
			skPrefix = fmt.Sprintf("DOMAIN#%s#LOCAL#%s#", domain, local)
		}
	}

	var items []*models.SoulCapabilityAgentIndex
	qb := s.store.DB.WithContext(ctx).
		Model(&models.SoulCapabilityAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#CAP#%s", cap)).
		OrderBy("SK", "ASC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}
	if skPrefix != "" {
		qb = qb.Where("SK", "BEGINS_WITH", skPrefix)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to search"}
	}

	out := make([]soulSearchIndexEntry, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, soulSearchIndexEntry{AgentID: item.AgentID, Domain: item.Domain, LocalID: item.LocalID, ClaimLevel: item.ClaimLevel})
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}
	return out, hasMore, nextCursor, nil
}

func (s *Server) querySoulSearchByDomain(ctx context.Context, q string, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	domain, local, appErr := parseSoulSearchQuery(q)
	if appErr != nil {
		return nil, false, "", appErr
	}
	if domain == "" {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "q must include a domain"}
	}

	var items []*models.SoulDomainAgentIndex
	qb := s.store.DB.WithContext(ctx).
		Model(&models.SoulDomainAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#DOMAIN#%s", domain)).
		OrderBy("SK", "ASC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}
	if local != "" {
		qb = qb.Where("SK", "BEGINS_WITH", fmt.Sprintf("LOCAL#%s#", local))
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to search"}
	}

	out := make([]soulSearchIndexEntry, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, soulSearchIndexEntry{AgentID: item.AgentID, Domain: item.Domain, LocalID: item.LocalID})
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}
	return out, hasMore, nextCursor, nil
}

func (s *Server) searchSoulAgents(ctx context.Context, params soulPublicSearchParams) ([]soulSearchResult, bool, string, *apptheory.AppError) {
	cursor := strings.TrimSpace(params.Cursor)
	remaining := params.Limit
	results := make([]soulSearchResult, 0, params.Limit)

	hasMore := false
	nextCursor := ""

	for remaining > 0 {
		entries, pageHasMore, pageNextCursor, appErr := s.querySoulSearchIndexEntries(ctx, params.Query, params.Capability, cursor, remaining)
		if appErr != nil {
			return nil, false, "", appErr
		}

		for _, entry := range entries {
			pass, err := s.soulSearchEntryPassesFilters(ctx, entry, params)
			if err != nil || !pass {
				continue
			}
			results = append(results, soulSearchResultFromEntry(entry))
			remaining = params.Limit - len(results)
			if remaining <= 0 {
				break
			}
		}

		hasMore = pageHasMore
		nextCursor = strings.TrimSpace(pageNextCursor)
		if remaining <= 0 || !hasMore || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return results, hasMore, nextCursor, nil
}

func (s *Server) filterActiveSoulSearchEntries(ctx context.Context, entries []soulSearchIndexEntry, limit int) []soulSearchResult {
	return s.filterSoulSearchEntries(ctx, entries, "", limit)
}

func (s *Server) filterSoulSearchEntries(ctx context.Context, entries []soulSearchIndexEntry, statusFilter string, limit int) []soulSearchResult {
	results := make([]soulSearchResult, 0, limit)
	for _, entry := range entries {
		identity, err := s.getSoulAgentIdentity(ctx, entry.AgentID)
		if err != nil || identity == nil {
			continue
		}
		agentStatus := strings.TrimSpace(identity.LifecycleStatus)
		if agentStatus == "" {
			agentStatus = strings.TrimSpace(identity.Status)
		}

		// If no status filter, default to active-only.
		if statusFilter == "" {
			if agentStatus != models.SoulAgentStatusActive {
				continue
			}
		} else if statusFilter != agentStatus {
			continue
		}

		results = append(results, soulSearchResultFromEntry(entry))
	}
	return results
}

func (s *Server) soulSearchEntryPassesFilters(ctx context.Context, entry soulSearchIndexEntry, params soulPublicSearchParams) (bool, error) {
	if params.ClaimLevel != "" && params.Capability != "" {
		if strings.ToLower(strings.TrimSpace(entry.ClaimLevel)) != params.ClaimLevel {
			return false, nil
		}
	}

	identity, err := s.getSoulAgentIdentity(ctx, entry.AgentID)
	if err != nil || identity == nil {
		return false, err
	}

	agentStatus := strings.TrimSpace(identity.LifecycleStatus)
	if agentStatus == "" {
		agentStatus = strings.TrimSpace(identity.Status)
	}
	if params.Status == "" {
		if agentStatus != models.SoulAgentStatusActive {
			return false, nil
		}
	} else if params.Status != agentStatus {
		return false, nil
	}

	if params.Boundary != "" {
		ok, err := s.agentHasBoundaryKeyword(ctx, entry.AgentID, params.Boundary)
		if err != nil || !ok {
			return false, err
		}
	}

	return true, nil
}

func (s *Server) agentHasBoundaryKeyword(ctx context.Context, agentIDHex string, keyword string) (bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return false, errors.New("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if agentIDHex == "" || keyword == "" {
		return false, nil
	}

	var items []*models.SoulAgentBoundary
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		All(&items); err != nil {
		return false, err
	}

	for _, b := range items {
		if b == nil {
			continue
		}
		if strings.Contains(strings.ToLower(b.Statement), keyword) ||
			strings.Contains(strings.ToLower(b.Rationale), keyword) ||
			strings.Contains(strings.ToLower(b.Category), keyword) {
			return true, nil
		}
	}

	return false, nil
}

func parseSoulSearchQuery(q string) (string, string, *apptheory.AppError) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", "", nil
	}

	if strings.Contains(q, "/") {
		parts := strings.SplitN(q, "/", 2)
		domainRaw := strings.TrimSpace(parts[0])
		localRaw := strings.TrimSpace(parts[1])

		domain, err := domains.NormalizeDomain(domainRaw)
		if err != nil {
			return "", "", &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		local, err := soul.NormalizeLocalAgentID(localRaw)
		if err != nil {
			return "", "", &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		return domain, local, nil
	}

	domain, err := domains.NormalizeDomain(q)
	if err == nil {
		return domain, "", nil
	}

	// Local-only searches require a scan; fail closed.
	return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "q must include a domain"}
}

func envInt64PositiveFromString(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (s *Server) setSoulPublicHeaders(ctx *apptheory.Context, resp *apptheory.Response, cacheControl string) {
	if resp == nil {
		return
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	cacheControl = strings.TrimSpace(cacheControl)
	if cacheControl == "" {
		cacheControl = "no-store"
	}
	resp.Headers["cache-control"] = []string{cacheControl}

	allowedOrigins := []string(nil)
	if s != nil {
		allowedOrigins = s.cfg.SoulPublicCORSOrigins
	}
	for _, allowed := range allowedOrigins {
		if strings.TrimSpace(allowed) == "*" {
			resp.Headers["access-control-allow-origin"] = []string{"*"}
			return
		}
	}
	if len(allowedOrigins) == 0 {
		resp.Headers["access-control-allow-origin"] = []string{"*"}
		return
	}

	reqOrigin := ""
	if ctx != nil {
		reqOrigin = strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "origin"))
	}
	if reqOrigin == "" {
		return
	}
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), reqOrigin) {
			resp.Headers["access-control-allow-origin"] = []string{reqOrigin}
			if vary := resp.Headers["vary"]; len(vary) == 0 {
				resp.Headers["vary"] = []string{"origin"}
			} else {
				hasOrigin := false
				for _, v := range vary {
					if strings.EqualFold(strings.TrimSpace(v), "origin") {
						hasOrigin = true
						break
					}
				}
				if !hasOrigin {
					resp.Headers["vary"] = append(vary, "origin")
				}
			}
			return
		}
	}
}
