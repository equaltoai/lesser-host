package controlplane

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
) (versionNumber int, appErr *apptheory.AppError) {
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
) (versionNumber int, appErr *apptheory.AppError) {
	if s == nil || identity == nil || regV3 == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return 0, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if s.store == nil || s.store.DB == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(regSHA256) == "" || len(strings.TrimSpace(regSHA256)) != 64 {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration sha256"}
	}

	prevVersionFromURI, nextVersion, appErr := s.deriveSoulRegistrationV3NextVersion(agentIDHex, regV3)
	if appErr != nil {
		return 0, appErr
	}
	if expectedVersion != nil && *expectedVersion != prevVersionFromURI {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version does not match previousVersionUri"}
	}
	if appErr := s.validateSoulRegistrationPreviousVersionURIv3(regV3, agentIDHex, nextVersion); appErr != nil {
		return 0, appErr
	}

	// optimistic concurrency: do not allow publishing an older version if the identity has moved ahead.
	if identity.SelfDescriptionVersion > nextVersion {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "agent has advanced beyond this version"}
	}

	// Pre-validate claimLevel transitions to avoid publishing artifacts we can't index.
	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)
	if appErr := s.validateCapabilityClaimLevelTransitions(ctx, identity, newCaps, claimLevels); appErr != nil {
		return 0, appErr
	}

	// Idempotency: if a version record already exists for this version number, ensure it matches
	// and then repair any missing S3/current/identity updates.
	if existing, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, nextVersion); err == nil && existing != nil {
		existingSHA := strings.ToLower(strings.TrimSpace(existing.RegistrationSHA256))
		if existingSHA != strings.ToLower(strings.TrimSpace(regSHA256)) {
			log.Printf("controlplane: soul_integrity version_sha_mismatch agent=%s version=%d expected_sha=%s got_sha=%s", agentIDHex, nextVersion, existingSHA, regSHA256)
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "version already exists with different content"}
		}

		if appErr := s.ensureSoulRegistrationS3Artifacts(ctx, agentIDHex, nextVersion, regBytes, regSHA256); appErr != nil {
			return 0, appErr
		}
		if appErr := s.finalizeSoulAgentRegistrationV2Identity(ctx, identity, capsNorm, claimLevels, nextVersion, now); appErr != nil {
			return 0, appErr
		}
		return nextVersion, nil
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to read version history"}
	}

	// New publish: enforce expected previous version via identity state.
	if identity.SelfDescriptionVersion != prevVersionFromURI {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	prevRegSHA256 := ""
	if prevVersionFromURI > 0 {
		prevRec, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, prevVersionFromURI)
		if theoryErrors.IsNotFound(err) {
			if s.cfg.SoulV2StrictIntegrity {
				log.Printf("controlplane: soul_integrity missing_previous_version_record agent=%s prev_version=%d", agentIDHex, prevVersionFromURI)
				return 0, &apptheory.AppError{Code: "app.conflict", Message: "missing previous version history; repair is required"}
			}
		} else if err != nil {
			return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to read version history"}
		} else if prevRec != nil {
			prevRegSHA256 = strings.TrimSpace(prevRec.RegistrationSHA256)
		}
	}

	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
	versionRecord := &models.SoulAgentVersion{
		AgentID:                    agentIDHex,
		VersionNumber:              nextVersion,
		RegistrationUri:            fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), versionedKey),
		RegistrationSHA256:         regSHA256,
		PreviousRegistrationSHA256: strings.TrimSpace(prevRegSHA256),
		ChangeSummary:              strings.TrimSpace(changeSummary),
		SelfAttestation:            strings.TrimSpace(selfSig),
		CreatedAt:                  now,
	}
	if err := versionRecord.UpdateKeys(); err != nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}

	// Create version record guarded by the expected identity version.
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.ConditionCheck(identity, soulIdentitySelfDescriptionVersionCondition(prevVersionFromURI)...)
		tx.Create(versionRecord)
		if extraWrites != nil {
			return extraWrites(tx)
		}
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
		}
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}

	if appErr := s.ensureSoulRegistrationS3Artifacts(ctx, agentIDHex, nextVersion, regBytes, regSHA256); appErr != nil {
		return 0, appErr
	}
	if appErr := s.finalizeSoulAgentRegistrationV2Identity(ctx, identity, capsNorm, claimLevels, nextVersion, now); appErr != nil {
		return 0, appErr
	}

	return nextVersion, nil
}

func (s *Server) deriveSoulRegistrationV3NextVersion(agentIDHex string, regV3 *soul.RegistrationFileV3) (prev int, next int, appErr *apptheory.AppError) {
	if s == nil || regV3 == nil {
		return 0, 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))

	if regV3.PreviousVersionURI == nil || strings.TrimSpace(*regV3.PreviousVersionURI) == "" {
		return 0, 1, nil
	}

	prevURI := strings.TrimSpace(*regV3.PreviousVersionURI)
	u, err := url.Parse(prevURI)
	if err != nil {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is invalid"}
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "s3" || strings.TrimSpace(u.Host) == "" {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is invalid"}
	}
	if !strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(s.cfg.SoulPackBucketName)) {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match expected bucket"}
	}

	key := strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
	prefix := fmt.Sprintf("registry/v1/agents/%s/versions/", agentIDHex)
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match expected agent"}
	}

	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) != "registration.json" {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match expected format"}
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n <= 0 {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match expected format"}
	}

	return n, n + 1, nil
}

func (s *Server) validateSoulRegistrationPreviousVersionURIv3(reg *soul.RegistrationFileV3, agentIDHex string, nextVersion int) *apptheory.AppError {
	if s == nil || reg == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if nextVersion <= 1 {
		// First version: previousVersionUri must be empty/null.
		if reg.PreviousVersionURI != nil && strings.TrimSpace(*reg.PreviousVersionURI) != "" {
			log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_set_on_first", agentIDHex, nextVersion)
			return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri must be null for the first version"}
		}
		return nil
	}

	prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
	expected := fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	if reg.PreviousVersionURI == nil || strings.TrimSpace(*reg.PreviousVersionURI) == "" {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=missing_prev_uri", agentIDHex, nextVersion)
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is required for subsequent versions"}
	}
	if strings.TrimSpace(*reg.PreviousVersionURI) != expected {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s next_version=%d reason=prev_uri_mismatch expected=%s got=%s", agentIDHex, nextVersion, expected, strings.TrimSpace(*reg.PreviousVersionURI))
		return &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri does not match the expected previous version"}
	}
	return nil
}
