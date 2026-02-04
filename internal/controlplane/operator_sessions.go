package controlplane

import (
	"context"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const operatorSessionDuration = 24 * time.Hour

func (s *Server) createOperatorSession(ctx context.Context, username, role, method string) (token string, expiresAt time.Time, err error) {
	token, err = newToken(32)
	if err != nil {
		return "", time.Time{}, err
	}

	now := time.Now().UTC()
	expiresAt = now.Add(operatorSessionDuration)

	session := &models.OperatorSession{
		ID:        token,
		Username:  username,
		Role:      role,
		Method:    method,
		IssuedAt:  now,
		ExpiresAt: expiresAt,
	}
	if err := session.UpdateKeys(); err != nil {
		return "", time.Time{}, err
	}

	if err := s.store.DB.WithContext(ctx).Model(session).Create(); err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}

