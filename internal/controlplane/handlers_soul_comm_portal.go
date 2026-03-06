package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentCommActivityResponse struct {
	Version    string                          `json:"version"`
	Activities []*models.SoulAgentCommActivity `json:"activities"`
	Count      int                             `json:"count"`
}

type soulAgentCommQueueResponse struct {
	Version string                       `json:"version"`
	Items   []*models.SoulAgentCommQueue `json:"items"`
	Count   int                          `json:"count"`
}

type soulAgentCommListContext struct {
	AgentIDHex string
	Limit      int
}

func (s *Server) requireSoulAgentWithDomainAccess(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}
	return identity, nil
}

func (s *Server) loadSoulAgentCommListContext(ctx *apptheory.Context) (soulAgentCommListContext, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	if _, appErr := s.requireSoulAgentWithDomainAccess(ctx, agentIDHex); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	return soulAgentCommListContext{
		AgentIDHex: agentIDHex,
		Limit:      parseLimit(queryFirst(ctx, "limit"), 50, 1, 200),
	}, nil
}

func (s *Server) listSoulAgentCommActivities(ctx *apptheory.Context, agentIDHex string, limit int) ([]*models.SoulAgentCommActivity, *apptheory.AppError) {
	var items []*models.SoulAgentCommActivity
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentCommActivity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "COMM#").
		OrderBy("SK", "DESC").
		Limit(limit).
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list communication activity"}
	}
	return items, nil
}

func (s *Server) listSoulAgentCommQueueItems(ctx *apptheory.Context, agentIDHex string, limit int) ([]*models.SoulAgentCommQueue, *apptheory.AppError) {
	var items []*models.SoulAgentCommQueue
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentCommQueue{}).
		Where("PK", "=", fmt.Sprintf("COMM#QUEUE#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "MSG#").
		OrderBy("SK", "ASC").
		Limit(limit).
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list queued messages"}
	}
	return items, nil
}

func (s *Server) handleSoulAgentCommActivity(ctx *apptheory.Context) (*apptheory.Response, error) {
	listCtx, appErr := s.loadSoulAgentCommListContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listSoulAgentCommActivities(ctx, listCtx.AgentIDHex, listCtx.Limit)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulAgentCommActivityResponse{
		Version:    "1",
		Activities: items,
		Count:      len(items),
	})
}

func (s *Server) handleSoulAgentCommQueue(ctx *apptheory.Context) (*apptheory.Response, error) {
	listCtx, appErr := s.loadSoulAgentCommListContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listSoulAgentCommQueueItems(ctx, listCtx.AgentIDHex, listCtx.Limit)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulAgentCommQueueResponse{
		Version: "1",
		Items:   items,
		Count:   len(items),
	})
}

func (s *Server) handleSoulAgentCommStatus(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	if _, appErr := s.requireSoulAgentWithDomainAccess(ctx, agentIDHex); appErr != nil {
		return nil, appErr
	}

	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" || len(messageID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "messageId is invalid"}
	}

	rec := &models.SoulCommMessageStatus{MessageID: messageID}
	_ = rec.UpdateKeys()
	var item models.SoulCommMessageStatus
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMessageStatus{}).
		Where("PK", "=", rec.PK).
		Where("SK", "=", rec.SK).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "message not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.ToLower(strings.TrimSpace(item.AgentID)) != agentIDHex {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "message not found"}
	}

	out := soulCommStatusResponse{
		MessageID:         strings.TrimSpace(item.MessageID),
		Status:            strings.TrimSpace(item.Status),
		Channel:           strings.TrimSpace(item.ChannelType),
		AgentID:           strings.ToLower(strings.TrimSpace(item.AgentID)),
		To:                strings.TrimSpace(item.To),
		Provider:          strings.TrimSpace(item.Provider),
		ProviderMessageID: strings.TrimSpace(item.ProviderMessageID),
		ErrorCode:         strings.TrimSpace(item.ErrorCode),
		ErrorMessage:      strings.TrimSpace(item.ErrorMessage),
		CreatedAt:         item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !item.UpdatedAt.IsZero() {
		out.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, nil
}
