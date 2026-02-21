package controlplane

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var instances []*models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("OWNER#%s", username)).
		All(&instances)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list instances"}
	}

	domainSet := map[string]struct{}{}
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		slug := strings.ToLower(strings.TrimSpace(inst.Slug))
		if slug == "" {
			continue
		}

		var domains []*models.Domain
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.Domain{}).
			Index("gsi1").
			Where("gsi1PK", "=", fmt.Sprintf("INSTANCE_DOMAINS#%s", slug)).
			All(&domains)
		if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list domains"}
		}

		for _, d := range domains {
			if d == nil {
				continue
			}
			domain := strings.ToLower(strings.TrimSpace(d.Domain))
			if domain == "" {
				continue
			}
			domainSet[domain] = struct{}{}
		}
	}

	agentSet := map[string]struct{}{}
	for domain := range domainSet {
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
			agentSet[agentID] = struct{}{}
		}
	}

	agentIDs := make([]string, 0, len(agentSet))
	for agentID := range agentSet {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	out := make([]soulMineAgentItem, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		identity, err := s.getSoulAgentIdentity(ctx.Context(), agentID)
		if theoryErrors.IsNotFound(err) || identity == nil {
			continue
		}
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load agent identity"}
		}

		rep, repErr := s.getSoulAgentReputation(ctx.Context(), agentID)
		if theoryErrors.IsNotFound(repErr) {
			rep = nil
		} else if repErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load agent reputation"}
		}

		out = append(out, soulMineAgentItem{Agent: *identity, Reputation: rep})
	}

	return apptheory.JSON(http.StatusOK, soulMineAgentsResponse{Agents: out, Count: len(out)})
}
