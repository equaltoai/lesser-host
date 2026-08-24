package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulMineAgentItem struct {
	Agent      models.SoulAgentIdentity    `json:"agent"`
	Reputation *models.SoulAgentReputation `json:"reputation,omitempty"`
}

type soulMineAgentsResponse struct {
	Agents []soulMineAgentItem `json:"agents"`
	Count  int                 `json:"count"`
}

func (s *Server) handleSoulListMyAgents(ctx *apptheory.Context) (*apptheory.Response, error) {
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
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	instances, appErr := s.listOwnedInstances(ctx.Context(), username)
	if appErr != nil {
		return nil, appErr
	}
	domainSet, appErr := s.listDomainsForInstances(ctx.Context(), instances)
	if appErr != nil {
		return nil, appErr
	}
	agentIDs, appErr := s.listAgentIDsForDomains(ctx.Context(), domainSet)
	if appErr != nil {
		return nil, appErr
	}
	out, appErr := s.loadSoulMineAgentItems(ctx.Context(), agentIDs)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulMineAgentsResponse{Agents: out, Count: len(out)})
}

func (s *Server) listOwnedInstances(ctx context.Context, username string) ([]*models.Instance, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, nil
	}

	var instances []*models.Instance
	err := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("OWNER#%s", username)).
		All(&instances)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.internal", "failed to list instances")
	}

	out := make([]*models.Instance, 0, len(instances))
	seen := make(map[string]struct{}, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		slug := strings.ToLower(strings.TrimSpace(inst.Slug))
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(strings.TrimSpace(out[i].Slug)) < strings.ToLower(strings.TrimSpace(out[j].Slug))
	})
	return out, nil
}

func (s *Server) listDomainsForInstances(ctx context.Context, instances []*models.Instance) (map[string]struct{}, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	domainSet := map[string]struct{}{}
	for _, inst := range instances {
		if inst == nil || strings.TrimSpace(inst.Slug) == "" {
			continue
		}
		domains, listErr := s.listVerifiedDomainsForInstance(ctx, inst)
		if listErr != nil {
			return nil, listErr
		}
		addStringsToSet(domainSet, domains...)
		addStringsToSet(domainSet, managedInstanceStageDomain(s.cfg.Stage, strings.TrimSpace(inst.HostedBaseDomain)))
	}

	return domainSet, nil
}

func (s *Server) listVerifiedDomainsForInstance(ctx context.Context, inst *models.Instance) ([]string, *apptheory.AppTheoryError) {
	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	var domains []*models.Domain
	err := s.store.DB.WithContext(ctx).
		Model(&models.Domain{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("INSTANCE_DOMAINS#%s", slug)).
		All(&domains)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.internal", "failed to list domains")
	}

	out := make([]string, 0, len(domains))
	for _, d := range domains {
		domain := verifiedDomainName(d)
		if domain != "" {
			out = append(out, domain)
		}
	}
	return out, nil
}

func verifiedDomainName(d *models.Domain) string {
	if d == nil || !domainIsVerifiedOrActive(d.Status) {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(d.Domain))
}

func addStringsToSet(set map[string]struct{}, values ...string) {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = struct{}{}
		}
	}
}

func (s *Server) listAgentIDsForDomains(ctx context.Context, domainSet map[string]struct{}) ([]string, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	agentSet := map[string]struct{}{}
	for domain := range domainSet {
		var idxItems []*models.SoulDomainAgentIndex
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulDomainAgentIndex{}).
			Where("PK", "=", fmt.Sprintf("SOUL#DOMAIN#%s", domain)).
			All(&idxItems)
		if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, newAppTheoryError("app.internal", "failed to list agents")
		}
		for _, idx := range idxItems {
			if idx == nil {
				continue
			}
			agentID := strings.ToLower(strings.TrimSpace(idx.AgentID))
			if agentID == "" {
				continue
			}
			agentSet[agentID] = struct{}{}
		}
	}

	agentIDs := make([]string, 0, len(agentSet))
	for agentID := range agentSet {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	return agentIDs, nil
}

func (s *Server) loadSoulMineAgentItems(ctx context.Context, agentIDs []string) ([]soulMineAgentItem, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	out := make([]soulMineAgentItem, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		identity, err := s.getSoulAgentIdentity(ctx, agentID)
		if theoryErrors.IsNotFound(err) || identity == nil {
			continue
		}
		if err != nil {
			return nil, newAppTheoryError("app.internal", "failed to load agent identity")
		}

		rep, repErr := s.getSoulAgentReputation(ctx, agentID)
		if theoryErrors.IsNotFound(repErr) {
			rep = nil
		} else if repErr != nil {
			return nil, newAppTheoryError("app.internal", "failed to load agent reputation")
		}

		out = append(out, soulMineAgentItem{Agent: *identity, Reputation: rep})
	}

	return out, nil
}
