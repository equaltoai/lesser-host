package controlplane

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// soulRegistrationV3TxHook matches the minimal transaction surface needed by callers.
type soulRegistrationV3TxHook interface {
	Create(model any, cond ...core.TransactCondition) core.TransactionBuilder
}

func (s *Server) publishSoulAgentRegistrationV3(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regV3 *soul.RegistrationFileV3,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
) (versionNumber int, appErr *apptheory.AppTheoryError) {
	return s.publishSoulAgentRegistrationV3WithExtraWrites(
		ctx,
		agentIDHex,
		identity,
		regV3,
		regBytes,
		regSHA256,
		selfSig,
		changeSummary,
		capsNorm,
		claimLevels,
		expectedVersion,
		now,
		nil,
	)
}

func (s *Server) publishSoulAgentRegistrationV3WithExtraWrites(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regV3 *soul.RegistrationFileV3,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
	extraWrites func(tx soulRegistrationV3TxHook) error,
) (versionNumber int, appErr *apptheory.AppTheoryError) {
	return publishSoulAgentRegistrationTyped(
		s,
		ctx,
		agentIDHex,
		identity,
		regV3,
		regBytes,
		regSHA256,
		selfSig,
		changeSummary,
		capsNorm,
		claimLevels,
		expectedVersion,
		now,
		extraWrites,
		(*Server).deriveSoulRegistrationV3NextVersion,
		(*Server).validateSoulRegistrationPreviousVersionURIv3,
	)
}

func (s *Server) deriveSoulRegistrationV3NextVersion(agentIDHex string, regV3 *soul.RegistrationFileV3) (prev int, next int, appErr *apptheory.AppTheoryError) {
	if s == nil || regV3 == nil {
		return 0, 0, newAppTheoryError("app.internal", "internal error")
	}
	return deriveSoulRegistrationNextVersion(agentIDHex, regV3.PreviousVersionURI, s.cfg.SoulPackBucketName)
}

func (s *Server) validateSoulRegistrationPreviousVersionURIv3(reg *soul.RegistrationFileV3, agentIDHex string, nextVersion int) *apptheory.AppTheoryError {
	if s == nil || reg == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if nextVersion <= 1 {
		// First version: previousVersionUri must be empty/null.
		if reg.PreviousVersionURI != nil && strings.TrimSpace(*reg.PreviousVersionURI) != "" {
			log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_set_on_first", agentIDHex, nextVersion)
			return newAppTheoryError("app.bad_request", "previousVersionUri must be null for the first version")
		}
		return nil
	}

	prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
	expected := fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	if reg.PreviousVersionURI == nil || strings.TrimSpace(*reg.PreviousVersionURI) == "" {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=missing_prev_uri", agentIDHex, nextVersion)
		return newAppTheoryError("app.bad_request", "previousVersionUri is required for subsequent versions")
	}
	if strings.TrimSpace(*reg.PreviousVersionURI) != expected {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_mismatch expected=%s got=%s", agentIDHex, nextVersion, expected, strings.TrimSpace(*reg.PreviousVersionURI))
		return newAppTheoryError("app.bad_request", "previousVersionUri does not match the expected previous version")
	}
	return nil
}
