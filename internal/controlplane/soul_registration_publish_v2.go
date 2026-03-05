package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// soulRegistrationV2TxHook is the minimal transaction surface used by callers that need to
// create additional records in the same DynamoDB transaction as the version record.
type soulRegistrationV2TxHook interface {
	Create(model any, cond ...core.TransactCondition) core.TransactionBuilder
}

func (s *Server) publishSoulAgentRegistrationV2(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regV2 *soul.RegistrationFileV2,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
) (versionNumber int, appErr *apptheory.AppError) {
	return s.publishSoulAgentRegistrationV2WithExtraWrites(
		ctx,
		agentIDHex,
		identity,
		regV2,
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

func (s *Server) publishSoulAgentRegistrationV2WithExtraWrites(
	ctx context.Context,
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	regV2 *soul.RegistrationFileV2,
	regBytes []byte,
	regSHA256 string,
	selfSig string,
	changeSummary string,
	capsNorm []string,
	claimLevels map[string]string,
	expectedVersion *int,
	now time.Time,
	extraWrites func(tx soulRegistrationV2TxHook) error,
) (versionNumber int, appErr *apptheory.AppError) {
	if s == nil || identity == nil || regV2 == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return 0, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if s.store == nil || s.store.DB == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.soulPacks == nil {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(regSHA256) == "" || len(strings.TrimSpace(regSHA256)) != 64 {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration sha256"}
	}

	prevVersionFromURI, nextVersion, appErr := s.deriveSoulRegistrationV2NextVersion(agentIDHex, regV2)
	if appErr != nil {
		return 0, appErr
	}
	if expectedVersion != nil && *expectedVersion != prevVersionFromURI {
		return 0, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version does not match previousVersionUri"}
	}
	if appErr := s.validateSoulRegistrationPreviousVersionURI(regV2, agentIDHex, nextVersion); appErr != nil {
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

func (s *Server) deriveSoulRegistrationV2NextVersion(agentIDHex string, regV2 *soul.RegistrationFileV2) (prev int, next int, appErr *apptheory.AppError) {
	if s == nil || regV2 == nil {
		return 0, 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))

	if regV2.PreviousVersionURI == nil || strings.TrimSpace(*regV2.PreviousVersionURI) == "" {
		return 0, 1, nil
	}

	prevURI := strings.TrimSpace(*regV2.PreviousVersionURI)
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

func soulIdentitySelfDescriptionVersionCondition(expected int) []core.TransactCondition {
	if expected <= 0 {
		return []core.TransactCondition{
			tabletheory.IfExists(),
			tabletheory.ConditionExpression("attribute_not_exists(selfDescriptionVersion) OR selfDescriptionVersion = :sdv", map[string]any{":sdv": 0}),
		}
	}
	return []core.TransactCondition{
		tabletheory.IfExists(),
		tabletheory.Condition("SelfDescriptionVersion", "=", expected),
	}
}

func (s *Server) getSoulAgentVersionRecord(ctx context.Context, agentIDHex string, version int) (*models.SoulAgentVersion, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, errors.New("agent id is required")
	}
	if version <= 0 {
		return nil, errors.New("version is required")
	}

	var out models.SoulAgentVersion
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "=", fmt.Sprintf("VERSION#%d", version)).
		First(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Server) ensureSoulRegistrationS3Artifacts(ctx context.Context, agentIDHex string, version int, regBytes []byte, regSHA256 string) *apptheory.AppError {
	if s == nil || s.soulPacks == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if version <= 0 {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, version)
	if appErr := s.ensureSoulPackObjectSHA256(ctx, versionedKey, regBytes, regSHA256); appErr != nil {
		return appErr
	}

	currentKey := soulRegistrationS3Key(agentIDHex)
	if err := s.soulPacks.PutObject(ctx, currentKey, regBytes, "application/json", "private, max-age=0"); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to publish registration"}
	}

	return nil
}

func (s *Server) ensureSoulPackObjectSHA256(ctx context.Context, key string, expectedBody []byte, expectedSHA256 string) *apptheory.AppError {
	if s == nil || s.soulPacks == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if expectedSHA256 == "" || len(expectedSHA256) != 64 {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	body, _, _, err := s.soulPacks.GetObject(ctx, key, 512*1024)
	if err == nil {
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != expectedSHA256 {
			log.Printf("controlplane: soul_integrity s3_sha_mismatch key=%s expected_sha=%s got_sha=%s", key, expectedSHA256, got)
			return &apptheory.AppError{Code: "app.conflict", Message: "registration artifact integrity violation"}
		}
		return nil
	}

	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		if putErr := s.soulPacks.PutObject(ctx, key, expectedBody, "application/json", "private, max-age=0"); putErr != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to publish versioned registration"}
		}
		return nil
	}

	return &apptheory.AppError{Code: "app.internal", Message: "failed to fetch registration"}
}

func (s *Server) finalizeSoulAgentRegistrationV2Identity(ctx context.Context, identity *models.SoulAgentIdentity, capsNorm []string, claimLevels map[string]string, version int, now time.Time) *apptheory.AppError {
	if s == nil || identity == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if version <= 0 {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if appErr := s.updateSoulAgentCapabilities(ctx, identity, capsNorm, claimLevels, now, true); appErr != nil {
		return appErr
	}

	if identity.SelfDescriptionVersion != version {
		identity.SelfDescriptionVersion = version
		if err := s.store.DB.WithContext(ctx).Model(identity).IfExists().Update("SelfDescriptionVersion"); err != nil {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to update identity version"}
		}
	}

	return nil
}
