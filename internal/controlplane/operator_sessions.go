package controlplane

import (
	"context"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const operatorSessionDuration = 24 * time.Hour

func buildOperatorSessionModel(username, role, method string, now time.Time) (token string, session *models.OperatorSession, expiresAt time.Time, err error) {
	token, err = newToken(32)
	if err != nil {
		return "", nil, time.Time{}, err
	}

	now = now.UTC()
	expiresAt = now.Add(operatorSessionDuration)
	storedID := sha256HexTrimmed(token)

	session = &models.OperatorSession{
		ID:        storedID,
		Username:  strings.TrimSpace(username),
		Role:      strings.TrimSpace(role),
		Method:    strings.TrimSpace(method),
		IssuedAt:  now,
		ExpiresAt: expiresAt,
	}
	if err := session.UpdateKeys(); err != nil {
		return "", nil, time.Time{}, err
	}
	return token, session, expiresAt, nil
}

func (s *Server) createOperatorSession(ctx context.Context, username, role, method string) (token string, expiresAt time.Time, err error) {
	token, session, expiresAt, err := buildOperatorSessionModel(username, role, method, time.Now().UTC())
	if err != nil {
		return "", time.Time{}, err
	}
	if err := s.store.DB.WithContext(ctx).Model(session).Create(); err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}
