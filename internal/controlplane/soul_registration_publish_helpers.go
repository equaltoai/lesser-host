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

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func deriveSoulRegistrationNextVersion(agentIDHex string, previousVersionURI *string, bucketName string) (prev int, next int, appErr *apptheory.AppError) {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if previousVersionURI == nil || strings.TrimSpace(*previousVersionURI) == "" {
		return 0, 1, nil
	}

	prevURI := strings.TrimSpace(*previousVersionURI)
	u, err := url.Parse(prevURI)
	if err != nil {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is invalid"}
	}
	if strings.ToLower(strings.TrimSpace(u.Scheme)) != "s3" || strings.TrimSpace(u.Host) == "" {
		return 0, 0, &apptheory.AppError{Code: "app.bad_request", Message: "previousVersionUri is invalid"}
	}
	if !strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(bucketName)) {
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

func validateSoulRegistrationPublishBase(s *Server, identity *models.SoulAgentIdentity, regSHA256 string) *apptheory.AppError {
	if s == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(regSHA256) == "" || len(strings.TrimSpace(regSHA256)) != 64 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration sha256"}
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
) (bool, *apptheory.AppError) {
	existing, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, nextVersion)
	if err == nil && existing != nil {
		existingSHA := strings.ToLower(strings.TrimSpace(existing.RegistrationSHA256))
		if existingSHA != strings.ToLower(strings.TrimSpace(regSHA256)) {
			log.Printf("controlplane: soul_integrity version_sha_mismatch agent=%s version=%d expected_sha=%s got_sha=%s", agentIDHex, nextVersion, existingSHA, regSHA256)
			return false, &apptheory.AppError{Code: "app.conflict", Message: "version already exists with different content"}
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
		return false, &apptheory.AppError{Code: "app.internal", Message: "failed to read version history"}
	}
	return false, nil
}

func (s *Server) loadPreviousSoulRegistrationSHA(ctx context.Context, agentIDHex string, prevVersion int) (string, *apptheory.AppError) {
	if prevVersion <= 0 {
		return "", nil
	}

	prevRec, err := s.getSoulAgentVersionRecord(ctx, agentIDHex, prevVersion)
	if theoryErrors.IsNotFound(err) {
		if s.cfg.SoulV2StrictIntegrity {
			log.Printf("controlplane: soul_integrity missing_previous_version_record agent=%s prev_version=%d", agentIDHex, prevVersion)
			return "", &apptheory.AppError{Code: "app.conflict", Message: "missing previous version history; repair is required"}
		}
		return "", nil
	}
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "failed to read version history"}
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
	derive func(*Server, string, Reg) (int, int, *apptheory.AppError),
	validate func(*Server, Reg, string, int) *apptheory.AppError,
) (int, *apptheory.AppError) {
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
		func() (int, int, *apptheory.AppError) {
			return derive(s, agentIDHex, reg)
		},
		func(nextVersion int) *apptheory.AppError {
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
	derive func() (int, int, *apptheory.AppError),
	validatePrevious func(int) *apptheory.AppError,
) (soulRegistrationPublishPlan, *apptheory.AppError) {
	prevVersionFromURI, nextVersion, deriveErr := derive()
	if deriveErr != nil {
		return soulRegistrationPublishPlan{}, deriveErr
	}
	if expectedVersion != nil && *expectedVersion != prevVersionFromURI {
		return soulRegistrationPublishPlan{}, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version does not match previousVersionUri"}
	}
	if validateErr := validatePrevious(nextVersion); validateErr != nil {
		return soulRegistrationPublishPlan{}, validateErr
	}
	if identity.SelfDescriptionVersion > nextVersion {
		return soulRegistrationPublishPlan{}, &apptheory.AppError{Code: "app.conflict", Message: "agent has advanced beyond this version"}
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
		return soulRegistrationPublishPlan{}, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	prevRegSHA256, prevErr := s.loadPreviousSoulRegistrationSHA(ctx, agentIDHex, prevVersionFromURI)
	if prevErr != nil {
		return soulRegistrationPublishPlan{}, prevErr
	}

	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion)
	versionRecord := buildSoulVersionRecord(agentIDHex, s.cfg.SoulPackBucketName, versionedKey, nextVersion, regSHA256, prevRegSHA256, changeSummary, selfSig, now)
	if err := versionRecord.UpdateKeys(); err != nil {
		return soulRegistrationPublishPlan{}, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}

	return soulRegistrationPublishPlan{
		prevVersionFromURI: prevVersionFromURI,
		nextVersion:        nextVersion,
		versionRecord:      versionRecord,
	}, nil
}
