package controlplane

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func deriveSoulRegistrationNextVersion(agentIDHex string, previousVersionURI *string, bucketName string) (prev int, next int, appErr *apptheory.AppTheoryError) {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if previousVersionURI == nil || strings.TrimSpace(*previousVersionURI) == "" {
		return 0, 1, nil
	}

	prevURI := strings.TrimSpace(*previousVersionURI)
	u, err := url.Parse(prevURI)
	if err != nil {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri is invalid")
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "s3" || strings.TrimSpace(u.Host) == "" {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri is invalid")
	}
	if !strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(bucketName)) {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri does not match expected bucket")
	}

	key := strings.TrimPrefix(strings.TrimSpace(u.Path), "/")
	prefix := fmt.Sprintf("registry/v1/agents/%s/versions/", agentIDHex)
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri does not match expected agent")
	}

	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) != "registration.json" {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri does not match expected format")
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || n <= 0 {
		return 0, 0, newAppTheoryError("app.bad_request", "previousVersionUri does not match expected format")
	}

	return n, n + 1, nil
}

func validateSoulRegistrationPublishBase(s *Server, identity *models.SoulAgentIdentity, regSHA256 string) *apptheory.AppTheoryError {
	if s == nil || identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if !s.cfg.SoulEnabled {
		return newAppTheoryError("app.not_found", "not found")
	}
	if s.store == nil || s.store.DB == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return newAppTheoryError("app.conflict", "soul registry bucket is not configured")
	}
	if strings.TrimSpace(regSHA256) == "" || len(strings.TrimSpace(regSHA256)) != 64 {
		return newAppTheoryError("app.bad_request", "invalid registration sha256")
	}
	return nil
}

func (s *Server) repairExistingSoulRegistrationVersion(
	ctx context.Context,
	agentIDHex string,
	nextVersion int,
	regBytes []byte,
	regSHA256 string,
	identity *models.SoulAgentIdentity,
	capsNorm []string,
	claimLevels map[string]string,
	now time.Time,
) (bool, *apptheory.AppTheoryError) {
	existing, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, nextVersion)
	if err == nil && existing != nil {
		existingSHA := strings.ToLower(strings.TrimSpace(existing.RegistrationSHA256))
		if existingSHA != strings.ToLower(strings.TrimSpace(regSHA256)) {
			log.Printf("controlplane: soul_integrity version_sha_mismatch agent=%s version=%d expected_sha=%s got_sha=%s", agentIDHex, nextVersion, existingSHA, regSHA256)
			return false, newAppTheoryError("app.conflict", "version already exists with different content")
		}
		if appErr := s.ensureSoulRegistrationS3Artifacts(ctx, agentIDHex, nextVersion, regBytes, regSHA256); appErr != nil {
			return false, appErr
		}
		if appErr := s.finalizeSoulAgentRegistrationV2Identity(ctx, identity, capsNorm, claimLevels, nextVersion, now); appErr != nil {
			return false, appErr
		}
		return true, nil
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return false, newAppTheoryError("app.internal", "failed to read version history")
	}
	return false, nil
}

func (s *Server) loadPreviousSoulRegistrationSHA(ctx context.Context, agentIDHex string, prevVersion int) (string, *apptheory.AppTheoryError) {
	if prevVersion <= 0 {
		return "", nil
	}

	prevRec, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, prevVersion)
	if theoryErrors.IsNotFound(err) {
		if s.cfg.SoulV2StrictIntegrity {
			log.Printf("controlplane: soul_integrity missing_previous_version_record agent=%s prev_version=%d", agentIDHex, prevVersion)
			return "", newAppTheoryError("app.conflict", "missing previous version history; repair is required")
		}
		return "", nil
	}
	if err != nil {
		return "", newAppTheoryError("app.internal", "failed to read version history")
	}
	if prevRec == nil {
		return "", nil
	}
	return strings.TrimSpace(prevRec.RegistrationSHA256), nil
}

type soulRegistrationPublishPlan struct {
	prevVersionFromURI int
	nextVersion        int
	versionRecord      *models.SoulAgentVersion
}

func publishSoulAgentRegistrationTyped[Reg any, Hook any](
	s *Server,
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	reg Reg,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
	extraWrites func(Hook) error,
	derive func(*Server, string, Reg) (int, int, *apptheory.AppTheoryError),
	validate func(*Server, Reg, string, int) *apptheory.AppTheoryError,
) (int, *apptheory.AppTheoryError) {
	return s.publishSoulAgentRegistrationWithExtraWrites(
		ctx,
		agentIDHex,
		identity,
		regBytes,
		regSHA256,
		selfSig,
		changeSummary,
		capsNorm,
		claimLevels,
		expectedVersion,
		now,
		func() (int, int, *apptheory.AppTheoryError) {
			return derive(s, agentIDHex, reg)
		},
		func(nextVersion int) *apptheory.AppTheoryError {
			return validate(s, reg, agentIDHex, nextVersion)
		},
		func(tx core.TransactionBuilder) error {
			if extraWrites == nil {
				return nil
			}
			hook, ok := any(tx).(Hook)
			if !ok {
				return fmt.Errorf("unexpected transaction hook type %T", tx)
			}
			return extraWrites(hook)
		},
	)
}

func (s *Server) prepareSoulRegistrationPublish(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
	derive func() (int, int, *apptheory.AppTheoryError),
	validatePrevious func(int) *apptheory.AppTheoryError,
) (soulRegistrationPublishPlan, *apptheory.AppTheoryError) {
	prevVersionFromURI, nextVersion, deriveErr := derive()
	if deriveErr != nil {
		return soulRegistrationPublishPlan{}, deriveErr
	}
	if expectedVersion != nil && *expectedVersion != prevVersionFromURI {
		return soulRegistrationPublishPlan{}, newAppTheoryError("app.bad_request", "expected_version does not match previousVersionUri")
	}
	if validateErr := validatePrevious(nextVersion); validateErr != nil {
		return soulRegistrationPublishPlan{}, validateErr
	}
	if prevVersionFromURI == 0 && nextVersion == 1 {
		if historyErr := s.ensureNoSoulRegistrationVersionHistory(ctx, agentIDHex); historyErr != nil {
			return soulRegistrationPublishPlan{}, historyErr
		}
	}
	if identity.SelfDescriptionVersion > nextVersion {
		return soulRegistrationPublishPlan{}, newAppTheoryError("app.conflict", "agent has advanced beyond this version")
	}

	newCaps := normalizeSoulCapabilitiesLoose(capsNorm)
	if transitionErr := s.validateCapabilityClaimLevelTransitions(ctx, identity, newCaps, claimLevels); transitionErr != nil {
		return soulRegistrationPublishPlan{}, transitionErr
	}
	if repaired, repairErr := s.repairExistingSoulRegistrationVersion(ctx, agentIDHex, nextVersion, regBytes, regSHA256, identity, capsNorm, claimLevels, now); repairErr != nil {
		return soulRegistrationPublishPlan{}, repairErr
	} else if repaired {
		return soulRegistrationPublishPlan{nextVersion: nextVersion}, nil
	}
	if identity.SelfDescriptionVersion != prevVersionFromURI {
		return soulRegistrationPublishPlan{}, newAppTheoryError("app.conflict", "version conflict; reload and try again")
	}

	prevRegSHA256, prevErr := s.loadPreviousSoulRegistrationSHA(ctx, agentIDHex, prevVersionFromURI)
	if prevErr != nil {
		return soulRegistrationPublishPlan{}, prevErr
	}

	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
	versionRecord := buildSoulVersionRecord(agentIDHex, s.cfg.SoulPackBucketName, versionedKey, nextVersion, regSHA256, prevRegSHA256, changeSummary, selfSig, now)
	if err := versionRecord.UpdateKeys(); err != nil {
		return soulRegistrationPublishPlan{}, newAppTheoryError("app.internal", "failed to record version history")
	}

	return soulRegistrationPublishPlan{
		prevVersionFromURI: prevVersionFromURI,
		nextVersion:        nextVersion,
		versionRecord:      versionRecord,
	}, nil
}

func (s *Server) ensureNoSoulRegistrationVersionHistory(ctx context.Context, agentIDHex string) *apptheory.AppTheoryError {
	nextExisting, _, appErr := s.getNextSoulAgentVersion(ctx, agentIDHex)
	if appErr != nil {
		return appErr
	}
	if nextExisting > 1 {
		log.Printf("controlplane: soul_integrity version_chain_violation agent=%s reason=missing_prev_uri_with_existing_history next_existing=%d", agentIDHex, nextExisting)
		return newAppTheoryError("app.conflict", "previousVersionUri is required for existing version history")
	}
	return nil
}
