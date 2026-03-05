package controlplane

import (
	"context"
	"log"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/soulsearch"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) tryWriteSoulBoundaryKeywordIndexForBoundary(ctx context.Context, identity *models.SoulAgentIdentity, boundary *models.SoulAgentBoundary) {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil || boundary == nil {
		return
	}

	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	domain := strings.ToLower(strings.TrimSpace(identity.Domain))
	localID := strings.TrimSpace(identity.LocalID)
	if agentID == "" || domain == "" || localID == "" {
		return
	}

	keywords := soulsearch.ExtractBoundaryKeywords(boundary.Category, boundary.Statement, boundary.Rationale)
	for _, kw := range keywords {
		if strings.TrimSpace(kw) == "" {
			continue
		}

		item := &models.SoulBoundaryKeywordAgentIndex{
			Keyword: kw,
			Domain:  domain,
			LocalID: localID,
			AgentID: agentID,
		}
		_ = item.UpdateKeys()

		if err := s.store.DB.WithContext(ctx).Model(item).IfNotExists().Create(); err != nil {
			if theoryErrors.IsConditionFailed(err) {
				continue
			}
			log.Printf("controlplane: soul_search boundary_keyword_index_write_failed agent=%s keyword=%s err=%v", agentID, kw, err)
		}
	}
}
