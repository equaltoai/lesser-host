package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// portalSoulRosterResponse is the owner-scoped fleet roster shown at
// /portal/souls. It intentionally exposes only read-only dashboard data:
// host-owned soul registry identity/reputation rows plus safe public agent
// metadata read from the managed Lesser instance.
type portalSoulRosterResponse struct {
	Souls []portalSoulRosterItem `json:"souls"`
	Count int                    `json:"count"`
}

type portalSoulRosterItem struct {
	Agent           models.SoulAgentIdentity     `json:"agent"`
	Reputation      *models.SoulAgentReputation  `json:"reputation,omitempty"`
	Instance        portalSoulRosterInstance     `json:"instance"`
	LesserAgent     *portalSoulRosterLesserAgent `json:"lesser_agent,omitempty"`
	Tips            portalSoulRosterTips         `json:"tips"`
	AnchorAssurance *soulAnchorAssuranceView     `json:"anchor_assurance,omitempty"`
}

type portalSoulRosterInstance struct {
	Slug   string `json:"slug"`
	Domain string `json:"domain,omitempty"`
}

type portalSoulRosterLesserAgent struct {
	Username     string `json:"username,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	AgentType    string `json:"agent_type,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	Status       string `json:"status"`
	Source       string `json:"source"`
}

type portalSoulRosterTips struct {
	Received int64  `json:"received"`
	Period   string `json:"period"`
	Label    string `json:"label"`
	Source   string `json:"source"`
}

type portalSoulDomainCandidate struct {
	agentID  string
	domain   string
	localID  string
	instance *models.Instance
}

// lesserAgentMetadata is the safe subset of Lesser's GET /api/v1/agents/{username}
// response used to populate the fleet roster model column.
type lesserAgentMetadata struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	AgentType    string `json:"agent_type"`
	AgentVersion string `json:"agent_version"`
}

func (s *Server) handlePortalSoulRoster(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	instances, appErr := s.listOwnedInstances(ctx.Context(), username)
	if appErr != nil {
		return nil, appErr
	}
	domainOwners, appErr := s.listSoulRosterDomainOwners(ctx.Context(), instances)
	if appErr != nil {
		return nil, appErr
	}
	candidates, appErr := s.listSoulRosterCandidatesForDomains(ctx, domainOwners)
	if appErr != nil {
		return nil, appErr
	}
	out, appErr := s.loadPortalSoulRosterItems(ctx, candidates)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, portalSoulRosterResponse{Souls: out, Count: len(out)})
}

func (s *Server) listSoulRosterDomainOwners(ctx context.Context, instances []*models.Instance) (map[string]*models.Instance, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	domainOwners := make(map[string]*models.Instance)
	for _, inst := range instances {
		if inst == nil || strings.TrimSpace(inst.Slug) == "" {
			continue
		}
		domains, listErr := s.listVerifiedDomainsForInstance(ctx, inst)
		if listErr != nil {
			return nil, listErr
		}
		for _, domain := range domains {
			addSoulRosterDomainOwner(domainOwners, domain, inst)
		}
		addSoulRosterDomainOwner(domainOwners, managedInstanceStageDomain(s.cfg.Stage, strings.TrimSpace(inst.HostedBaseDomain)), inst)
	}
	return domainOwners, nil
}

func addSoulRosterDomainOwner(domainOwners map[string]*models.Instance, domain string, inst *models.Instance) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || inst == nil {
		return
	}
	if _, ok := domainOwners[domain]; !ok {
		domainOwners[domain] = inst
	}
}

func (s *Server) listSoulRosterCandidatesForDomains(ctx *apptheory.Context, domainOwners map[string]*models.Instance) ([]portalSoulDomainCandidate, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	domains := make([]string, 0, len(domainOwners))
	for domain := range domainOwners {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	candidates := make([]portalSoulDomainCandidate, 0)
	seenAgents := map[string]struct{}{}
	for _, domain := range domains {
		var idxItems []*models.SoulDomainAgentIndex
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.SoulDomainAgentIndex{}).
			Where("PK", "=", fmt.Sprintf("SOUL#DOMAIN#%s", domain)).
			All(&idxItems)
		if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list agents"}
		}
		for _, idx := range idxItems {
			if idx == nil {
				continue
			}
			agentID := strings.ToLower(strings.TrimSpace(idx.AgentID))
			if agentID == "" {
				continue
			}
			if _, ok := seenAgents[agentID]; ok {
				continue
			}
			seenAgents[agentID] = struct{}{}
			candidates = append(candidates, portalSoulDomainCandidate{
				agentID:  agentID,
				domain:   domain,
				localID:  strings.TrimSpace(idx.LocalID),
				instance: domainOwners[domain],
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return portalSoulRosterCandidateSortKey(candidates[i]) < portalSoulRosterCandidateSortKey(candidates[j])
	})
	return candidates, nil
}

func portalSoulRosterCandidateSortKey(c portalSoulDomainCandidate) string {
	slug := ""
	if c.instance != nil {
		slug = strings.ToLower(strings.TrimSpace(c.instance.Slug))
	}
	return slug + "\x00" + strings.ToLower(strings.TrimSpace(c.domain)) + "\x00" + strings.ToLower(strings.TrimSpace(c.localID)) + "\x00" + c.agentID
}

func (s *Server) loadPortalSoulRosterItems(ctx *apptheory.Context, candidates []portalSoulDomainCandidate) ([]portalSoulRosterItem, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	out := make([]portalSoulRosterItem, 0, len(candidates))
	for _, candidate := range candidates {
		identity, err := s.getSoulAgentIdentity(ctx.Context(), candidate.agentID)
		if theoryErrors.IsNotFound(err) || identity == nil {
			continue
		}
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load agent identity"}
		}

		rep, repErr := s.getSoulAgentReputation(ctx.Context(), candidate.agentID)
		if theoryErrors.IsNotFound(repErr) {
			rep = nil
		} else if repErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load agent reputation"}
		}

		assurance := buildSoulAnchorAssuranceFromIdentity(identity, s.cfg.SoulChainID)

		item := portalSoulRosterItem{
			Agent:      *identity,
			Reputation: rep,
			Instance: portalSoulRosterInstance{
				Slug:   portalSoulRosterInstanceSlug(candidate.instance),
				Domain: strings.ToLower(strings.TrimSpace(candidate.domain)),
			},
			LesserAgent:     s.fetchPortalSoulLesserAgent(ctx, candidate.instance, portalSoulRosterAgentUsername(identity, candidate)),
			Tips:            portalSoulRosterTipsFromReputation(rep),
			AnchorAssurance: &assurance,
		}
		out = append(out, item)
	}

	return out, nil
}

func portalSoulRosterAgentUsername(identity *models.SoulAgentIdentity, candidate portalSoulDomainCandidate) string {
	if identity != nil && strings.TrimSpace(identity.LocalID) != "" {
		return strings.TrimSpace(identity.LocalID)
	}
	return strings.TrimSpace(candidate.localID)
}

func portalSoulRosterInstanceSlug(inst *models.Instance) string {
	if inst == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(inst.Slug))
}

func portalSoulRosterTipsFromReputation(rep *models.SoulAgentReputation) portalSoulRosterTips {
	tips := portalSoulRosterTips{
		Period: "all_time",
		Label:  "Tip events · all time",
		Source: "lesser-host:soul_agent_reputation",
	}
	if rep != nil {
		tips.Received = rep.TipsReceived
	}
	return tips
}

func (s *Server) fetchPortalSoulLesserAgent(ctx *apptheory.Context, inst *models.Instance, username string) *portalSoulRosterLesserAgent {
	username = strings.TrimSpace(username)
	if username == "" {
		return &portalSoulRosterLesserAgent{Status: "not_configured", Source: "lesser:/api/v1/agents/{username}"}
	}

	baseURL, err := s.resolvePortalCostMetricsBaseURL(inst)
	if err != nil {
		return &portalSoulRosterLesserAgent{Username: username, Status: "not_configured", Source: "lesser:/api/v1/agents/{username}"}
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/agents/" + url.PathEscape(username)
	req, err := http.NewRequestWithContext(ctx.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return &portalSoulRosterLesserAgent{Username: username, Status: "unavailable", Source: "lesser:/api/v1/agents/{username}"}
	}
	req.Header.Set("Accept", "application/json")

	client := s.portalCostHTTPClient
	if client == nil {
		client = &http.Client{Timeout: instanceMetricsTimeout}
	}

	resp, err := client.Do(req) //nolint:gosec // URL is derived from managed instance metadata or an injected test seam, not browser input.
	if err != nil {
		return &portalSoulRosterLesserAgent{Username: username, Status: "unavailable", Source: "lesser:/api/v1/agents/{username}"}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		status := "unavailable"
		if resp.StatusCode == http.StatusNotFound {
			status = "not_found"
		}
		return &portalSoulRosterLesserAgent{Username: username, Status: status, Source: "lesser:/api/v1/agents/{username}"}
	}

	var decoded lesserAgentMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return &portalSoulRosterLesserAgent{Username: username, Status: "unavailable", Source: "lesser:/api/v1/agents/{username}"}
	}

	return &portalSoulRosterLesserAgent{
		Username:     strings.TrimSpace(decoded.Username),
		DisplayName:  strings.TrimSpace(decoded.DisplayName),
		AgentType:    strings.TrimSpace(decoded.AgentType),
		AgentVersion: strings.TrimSpace(decoded.AgentVersion),
		Status:       "loaded",
		Source:       "lesser:/api/v1/agents/{username}",
	}
}
