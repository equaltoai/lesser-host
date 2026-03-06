package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/soulsearch"
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

type soulSearchPrimaryIndex string

const (
	soulSearchPrimaryCapability soulSearchPrimaryIndex = "capability"
	soulSearchPrimaryBoundary   soulSearchPrimaryIndex = "boundary"
	soulSearchPrimaryDomain     soulSearchPrimaryIndex = "domain"
	soulSearchPrimaryChannel    soulSearchPrimaryIndex = "channel"
)

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
	Domain     string
	LocalID    string
	LocalExact bool
	Capability string
	ClaimLevel string
	Boundary   string
	Channels   []string
	ENSName    string
	Status     string
	Cursor     string
	Limit      int
}

func parseSoulPublicSearchParams(ctx *apptheory.Context) (soulPublicSearchParams, *apptheory.AppError) {
	if ctx == nil {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	q := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "q"))
	domainRaw := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "domain"))
	cap := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "capability")))
	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	claimLevel := parseSoulSearchClaimLevel(ctx)
	ensName := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "ens"))
	boundary, appErr := parseSoulSearchBoundary(httpx.FirstQueryValue(ctx.Request.Query, "boundary"))
	if appErr != nil {
		return soulPublicSearchParams{}, appErr
	}
	channels, appErr := parseSoulSearchChannels(soulAllQueryValues(ctx.Request.Query, "channel"))
	if appErr != nil {
		return soulPublicSearchParams{}, appErr
	}
	status, appErr := parseSoulSearchStatus(httpx.FirstQueryValue(ctx.Request.Query, "status"))
	if appErr != nil {
		return soulPublicSearchParams{}, appErr
	}

	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	domain, localID, localExact, appErr := parseSoulSearchDomainAndLocal(q, domainRaw)
	if appErr != nil {
		return soulPublicSearchParams{}, appErr
	}

	if domain == "" && cap == "" && boundary == "" && strings.TrimSpace(ensName) == "" && len(channels) == 0 {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.bad_request", Message: "domain, capability, boundary, ens, or channel is required"}
	}

	if claimLevel != "" && cap == "" {
		return soulPublicSearchParams{}, &apptheory.AppError{Code: "app.bad_request", Message: "claimLevel requires capability"}
	}

	return soulPublicSearchParams{
		Domain:     domain,
		LocalID:    localID,
		LocalExact: localExact,
		Capability: cap,
		ClaimLevel: claimLevel,
		Boundary:   boundary,
		Channels:   channels,
		ENSName:    strings.TrimSpace(ensName),
		Status:     status,
		Cursor:     cursor,
		Limit:      limit,
	}, nil
}

func parseSoulSearchClaimLevel(ctx *apptheory.Context) string {
	claimLevel := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "claimLevel")))
	if claimLevel != "" {
		return claimLevel
	}
	return strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "claim_level")))
}

func parseSoulSearchBoundary(raw string) (string, *apptheory.AppError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	keyword, ok := soulsearch.NormalizeBoundaryKeyword(raw)
	if !ok {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "boundary must be a single keyword"}
	}
	return keyword, nil
}

func parseSoulSearchChannels(channelsRaw []string) ([]string, *apptheory.AppError) {
	channels := make([]string, 0, len(channelsRaw))
	seen := map[string]struct{}{}
	for _, raw := range channelsRaw {
		channelType := strings.ToLower(strings.TrimSpace(raw))
		if channelType == "" {
			continue
		}
		switch channelType {
		case models.SoulChannelTypeEmail, models.SoulChannelTypePhone:
		default:
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "channel must be email or phone"}
		}
		if _, ok := seen[channelType]; ok {
			continue
		}
		seen[channelType] = struct{}{}
		channels = append(channels, channelType)
	}
	sort.Strings(channels)
	return channels, nil
}

func parseSoulSearchStatus(raw string) (string, *apptheory.AppError) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return "", nil
	}
	switch status {
	case models.SoulAgentStatusActive,
		models.SoulAgentStatusSuspended,
		models.SoulAgentStatusSelfSuspended,
		models.SoulAgentStatusArchived,
		models.SoulAgentStatusSucceeded,
		models.SoulAgentStatusBurned,
		models.SoulAgentStatusPending:
		return status, nil
	default:
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid status"}
	}
}

func soulAllQueryValues(query map[string][]string, key string) []string {
	if query == nil {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if values := query[key]; len(values) > 0 {
		return values
	}
	if lower := strings.ToLower(key); lower != key {
		if values := query[lower]; len(values) > 0 {
			return values
		}
	}
	for k, values := range query {
		if strings.EqualFold(strings.TrimSpace(k), key) && len(values) > 0 {
			return values
		}
	}
	return nil
}

func (s *Server) querySoulSearchIndexEntries(ctx context.Context, primary soulSearchPrimaryIndex, params soulPublicSearchParams, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	switch primary {
	case soulSearchPrimaryCapability:
		return s.querySoulSearchByCapability(ctx, params.Capability, params.Domain, params.LocalID, params.LocalExact, cursor, limit)
	case soulSearchPrimaryBoundary:
		return s.querySoulSearchByBoundaryKeyword(ctx, params.Boundary, params.Domain, params.LocalID, params.LocalExact, cursor, limit)
	case soulSearchPrimaryDomain:
		return s.querySoulSearchByDomain(ctx, params.Domain, params.LocalID, params.LocalExact, cursor, limit)
	case soulSearchPrimaryChannel:
		return s.querySoulSearchByChannels(ctx, params.Channels, params.Domain, params.LocalID, params.LocalExact, cursor, limit)
	default:
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "invalid search index"}
	}
}

func (s *Server) querySoulSearchByCapability(ctx context.Context, cap string, domain string, localID string, localExact bool, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	capNorm := normalizeSoulCapabilitiesLoose([]string{cap})
	if len(capNorm) == 0 {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid capability"}
	}
	cap = capNorm[0]

	skPrefix := ""
	if strings.TrimSpace(domain) != "" {
		skPrefix = fmt.Sprintf("DOMAIN#%s#", domain)
		if strings.TrimSpace(localID) != "" {
			skPrefix = fmt.Sprintf("DOMAIN#%s#LOCAL#%s", domain, localID)
			if localExact {
				skPrefix += "#"
			}
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

func (s *Server) querySoulSearchByBoundaryKeyword(ctx context.Context, keyword string, domain string, localID string, localExact bool, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "boundary is required"}
	}

	skPrefix := ""
	if strings.TrimSpace(domain) != "" {
		skPrefix = fmt.Sprintf("DOMAIN#%s#", domain)
		if strings.TrimSpace(localID) != "" {
			skPrefix = fmt.Sprintf("DOMAIN#%s#LOCAL#%s", domain, localID)
			if localExact {
				skPrefix += "#"
			}
		}
	}

	var items []*models.SoulBoundaryKeywordAgentIndex
	qb := s.store.DB.WithContext(ctx).
		Model(&models.SoulBoundaryKeywordAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#BOUNDARY#%s", keyword)).
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

func (s *Server) querySoulSearchByDomain(ctx context.Context, domain string, localID string, localExact bool, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "domain is required"}
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
	if strings.TrimSpace(localID) != "" {
		prefix := fmt.Sprintf("LOCAL#%s", localID)
		if localExact {
			prefix += "#"
		}
		qb = qb.Where("SK", "BEGINS_WITH", prefix)
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

func encodeSoulChannelSearchCursor(channelIndex int, innerCursor string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(innerCursor)))
	return fmt.Sprintf("ch:%d:%s", channelIndex, encoded)
}

func decodeSoulChannelSearchCursor(raw string) (channelIndex int, innerCursor string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", true
	}

	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "ch" {
		return 0, "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || n < 0 {
		return 0, "", false
	}

	inner := strings.TrimSpace(parts[2])
	if inner == "" {
		return n, "", true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(inner)
	if err != nil {
		return 0, "", false
	}
	return n, string(decoded), true
}

func (s *Server) querySoulSearchByChannels(ctx context.Context, channelTypes []string, domain string, localID string, localExact bool, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	if len(channelTypes) == 0 {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "channel is required"}
	}

	channelIndex, innerCursor, ok := decodeSoulChannelSearchCursor(cursor)
	if !ok || channelIndex < 0 || channelIndex >= len(channelTypes) {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid cursor"}
	}

	results := make([]soulSearchIndexEntry, 0, limit)
	remaining := limit
	for channelIndex < len(channelTypes) && remaining > 0 {
		entries, hasMore, nextCursor, appErr := s.querySoulSearchByChannelType(ctx, channelTypes[channelIndex], domain, localID, localExact, innerCursor, remaining)
		if appErr != nil {
			return nil, false, "", appErr
		}
		results = append(results, entries...)
		remaining = limit - len(results)
		if remaining <= 0 {
			if hasMore && strings.TrimSpace(nextCursor) != "" {
				return results, true, encodeSoulChannelSearchCursor(channelIndex, nextCursor), nil
			}
			if channelIndex+1 < len(channelTypes) {
				return results, true, encodeSoulChannelSearchCursor(channelIndex+1, ""), nil
			}
			return results, false, "", nil
		}

		if hasMore && strings.TrimSpace(nextCursor) != "" {
			return results, true, encodeSoulChannelSearchCursor(channelIndex, nextCursor), nil
		}

		channelIndex++
		innerCursor = ""
	}

	return results, false, "", nil
}

func (s *Server) querySoulSearchByChannelType(ctx context.Context, channelType string, domain string, localID string, localExact bool, cursor string, limit int) ([]soulSearchIndexEntry, bool, string, *apptheory.AppError) {
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType != models.SoulChannelTypeEmail && channelType != models.SoulChannelTypePhone {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid channel"}
	}

	skPrefix := ""
	if strings.TrimSpace(domain) != "" {
		skPrefix = fmt.Sprintf("DOMAIN#%s#", domain)
		if strings.TrimSpace(localID) != "" {
			skPrefix = fmt.Sprintf("DOMAIN#%s#LOCAL#%s", domain, localID)
			if localExact {
				skPrefix += "#"
			}
		}
	}

	var items []*models.SoulChannelAgentIndex
	qb := s.store.DB.WithContext(ctx).
		Model(&models.SoulChannelAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#CHANNEL#%s", channelType)).
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

func (s *Server) searchSoulAgentsByENS(ctx context.Context, params soulPublicSearchParams) ([]soulSearchResult, bool, string, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	ensName := strings.TrimSpace(params.ENSName)
	if ensName == "" {
		return nil, false, "", &apptheory.AppError{Code: "app.bad_request", Message: "ens is required"}
	}

	key := &models.SoulAgentENSResolution{ENSName: ensName}
	_ = key.UpdateKeys()

	var res models.SoulAgentENSResolution
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", key.PK).
		Where("SK", "=", "RESOLUTION").
		First(&res)
	if theoryErrors.IsNotFound(err) {
		return []soulSearchResult{}, false, "", nil
	}
	if err != nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to search"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(res.AgentID))
	if agentIDHex == "" {
		return []soulSearchResult{}, false, "", nil
	}

	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return []soulSearchResult{}, false, "", nil
	}
	if err != nil || identity == nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to search"}
	}
	pass, err := s.agentPassesSearchParams(ctx, agentIDHex, identity, params)
	if err != nil {
		return nil, false, "", &apptheory.AppError{Code: "app.internal", Message: "failed to search"}
	}
	if !pass {
		return []soulSearchResult{}, false, "", nil
	}

	return []soulSearchResult{{
		AgentID: agentIDHex,
		Domain:  strings.TrimSpace(identity.Domain),
		LocalID: strings.TrimSpace(identity.LocalID),
	}}, false, "", nil
}

func (s *Server) searchSoulAgents(ctx context.Context, params soulPublicSearchParams) ([]soulSearchResult, bool, string, *apptheory.AppError) {
	if strings.TrimSpace(params.ENSName) != "" {
		return s.searchSoulAgentsByENS(ctx, params)
	}

	cursor := strings.TrimSpace(params.Cursor)
	remaining := params.Limit
	results := make([]soulSearchResult, 0, params.Limit)

	primary := soulSearchPrimaryDomain
	switch {
	case strings.TrimSpace(params.Capability) != "":
		primary = soulSearchPrimaryCapability
	case strings.TrimSpace(params.Boundary) != "":
		primary = soulSearchPrimaryBoundary
	case strings.TrimSpace(params.Domain) != "":
		primary = soulSearchPrimaryDomain
	case len(params.Channels) > 0:
		primary = soulSearchPrimaryChannel
	}

	hasMore := false
	nextCursor := ""

	for remaining > 0 {
		entries, pageHasMore, pageNextCursor, appErr := s.querySoulSearchIndexEntries(ctx, primary, params, cursor, remaining)
		if appErr != nil {
			return nil, false, "", appErr
		}

		for _, entry := range entries {
			pass, err := s.soulSearchEntryPassesFilters(ctx, entry, params, primary)
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

func (s *Server) soulSearchEntryPassesFilters(ctx context.Context, entry soulSearchIndexEntry, params soulPublicSearchParams, primary soulSearchPrimaryIndex) (bool, error) {
	if params.ClaimLevel != "" && params.Capability != "" {
		if strings.ToLower(strings.TrimSpace(entry.ClaimLevel)) != params.ClaimLevel {
			return false, nil
		}
	}

	identity, err := s.getSoulAgentIdentity(ctx, entry.AgentID)
	if err != nil || identity == nil {
		return false, err
	}
	return s.entryPassesSoulSearchFilters(ctx, entry, identity, params, primary)
}

func (s *Server) agentPassesSearchParams(ctx context.Context, agentIDHex string, identity *models.SoulAgentIdentity, params soulPublicSearchParams) (bool, error) {
	if !soulSearchStatusMatches(params.Status, soulIdentityStatus(identity)) {
		return false, nil
	}
	if params.Boundary != "" {
		ok, err := s.agentHasBoundaryKeywordIndex(ctx, agentIDHex, identity.Domain, identity.LocalID, params.Boundary)
		if err != nil || !ok {
			return false, err
		}
	}
	if len(params.Channels) == 0 {
		return true, nil
	}
	return s.agentHasAnySearchChannel(ctx, agentIDHex, identity.Domain, identity.LocalID, params.Channels)
}

func (s *Server) entryPassesSoulSearchFilters(
	ctx context.Context,
	entry soulSearchIndexEntry,
	identity *models.SoulAgentIdentity,
	params soulPublicSearchParams,
	primary soulSearchPrimaryIndex,
) (bool, error) {
	if !soulSearchStatusMatches(params.Status, soulIdentityStatus(identity)) {
		return false, nil
	}
	if params.Boundary != "" && primary != soulSearchPrimaryBoundary {
		ok, err := s.agentHasBoundaryKeywordIndex(ctx, entry.AgentID, entry.Domain, entry.LocalID, params.Boundary)
		if err != nil || !ok {
			return false, err
		}
	}
	if len(params.Channels) == 0 || primary == soulSearchPrimaryChannel {
		return true, nil
	}
	return s.agentHasAnySearchChannel(ctx, entry.AgentID, entry.Domain, entry.LocalID, params.Channels)
}

func (s *Server) agentHasAnySearchChannel(ctx context.Context, agentIDHex string, domain string, localID string, channelTypes []string) (bool, error) {
	for _, channelType := range channelTypes {
		ok, err := s.agentHasChannelIndex(ctx, agentIDHex, domain, localID, channelType)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func soulIdentityStatus(identity *models.SoulAgentIdentity) string {
	if identity == nil {
		return ""
	}
	if status := strings.TrimSpace(identity.LifecycleStatus); status != "" {
		return status
	}
	return strings.TrimSpace(identity.Status)
}

func soulSearchStatusMatches(filter string, status string) bool {
	if filter == "" {
		return status == models.SoulAgentStatusActive
	}
	return filter == status
}

func (s *Server) agentHasBoundaryKeywordIndex(ctx context.Context, agentIDHex string, domain string, localID string, keyword string) (bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return false, errors.New("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	domain = strings.ToLower(strings.TrimSpace(domain))
	localID = strings.ToLower(strings.TrimSpace(localID))
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if agentIDHex == "" || domain == "" || localID == "" || keyword == "" {
		return false, nil
	}

	var item models.SoulBoundaryKeywordAgentIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulBoundaryKeywordAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#BOUNDARY#%s", keyword)).
		Where("SK", "=", fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", domain, localID, agentIDHex)).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(item.AgentID) != "", nil
}

func (s *Server) agentHasChannelIndex(ctx context.Context, agentIDHex string, domain string, localID string, channelType string) (bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return false, errors.New("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	domain = strings.ToLower(strings.TrimSpace(domain))
	localID = strings.ToLower(strings.TrimSpace(localID))
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if agentIDHex == "" || domain == "" || localID == "" || channelType == "" {
		return false, nil
	}

	switch channelType {
	case models.SoulChannelTypeEmail, models.SoulChannelTypePhone:
	default:
		return false, nil
	}

	var item models.SoulChannelAgentIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulChannelAgentIndex{}).
		Where("PK", "=", fmt.Sprintf("SOUL#CHANNEL#%s", channelType)).
		Where("SK", "=", fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", domain, localID, agentIDHex)).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(item.AgentID) != "", nil
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

func parseSoulSearchDomainAndLocal(q string, domainRaw string) (domain string, localID string, localExact bool, appErr *apptheory.AppError) {
	q = strings.TrimSpace(q)
	domainRaw = strings.TrimSpace(domainRaw)

	if domainRaw != "" {
		norm, err := domains.NormalizeDomain(domainRaw)
		if err != nil {
			return "", "", false, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
		}
		domain = norm
	}

	if q == "" {
		return domain, "", false, nil
	}

	if strings.Contains(q, "/") {
		dFromQ, localFromQ, parseErr := parseSoulSearchQuery(q)
		if parseErr != nil {
			return "", "", false, parseErr
		}
		if domain != "" && dFromQ != "" && domain != dFromQ {
			return "", "", false, &apptheory.AppError{Code: "app.bad_request", Message: "q domain does not match domain parameter"}
		}
		return dFromQ, localFromQ, true, nil
	}

	if dFromQ, err := domains.NormalizeDomain(q); err == nil {
		if domain != "" && domain != dFromQ {
			return "", "", false, &apptheory.AppError{Code: "app.bad_request", Message: "q domain does not match domain parameter"}
		}
		return dFromQ, "", false, nil
	}

	if domain == "" {
		return "", "", false, &apptheory.AppError{Code: "app.bad_request", Message: "q must include a domain (or provide domain=)"}
	}

	localID = normalizeSoulSearchLocalQuery(q)
	return domain, localID, false, nil
}

func normalizeSoulSearchLocalQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "@")
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.ToLower(raw)
	return raw
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

	allowedOrigins := soulPublicAllowedOrigins(s)
	if soulPublicAllowsAnyOrigin(allowedOrigins) {
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
			appendSoulPublicVaryOrigin(resp)
			return
		}
	}
}

func soulPublicAllowedOrigins(s *Server) []string {
	if s == nil {
		return nil
	}
	return s.cfg.SoulPublicCORSOrigins
}

func soulPublicAllowsAnyOrigin(origins []string) bool {
	if len(origins) == 0 {
		return true
	}
	for _, allowed := range origins {
		if strings.TrimSpace(allowed) == "*" {
			return true
		}
	}
	return false
}

func appendSoulPublicVaryOrigin(resp *apptheory.Response) {
	for _, varyValue := range resp.Headers["vary"] {
		if strings.EqualFold(strings.TrimSpace(varyValue), "origin") {
			return
		}
	}
	resp.Headers["vary"] = append(resp.Headers["vary"], "origin")
}
