package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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

func (s *Server) publishSoulAgentRegistrationWithExtraWrites(
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
	extraWrites func(tx core.TransactionBuilder) error,
) (int, *apptheory.AppError) {
	if baseErr := validateSoulRegistrationPublishBase(s, identity, regSHA256); baseErr != nil {
		return 0, baseErr
	}

	plan, planErr := s.prepareSoulRegistrationPublish(
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
		derive,
		validatePrevious,
	)
	if planErr != nil {
		return 0, planErr
	}
	if plan.versionRecord == nil {
		return plan.nextVersion, nil
	}

	writeErr := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.ConditionCheck(identity, soulIdentitySelfDescriptionVersionCondition(plan.prevVersionFromURI)...)
		tx.Create(plan.versionRecord)
		if extraWrites != nil {
			return extraWrites(tx)
		}
		return nil
	})
	if writeErr != nil {
		if theoryErrors.IsConditionFailed(writeErr) {
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
		}
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to record version history"}
	}

	if artifactErr := s.ensureSoulRegistrationS3Artifacts(ctx, agentIDHex, plan.nextVersion, regBytes, regSHA256); artifactErr != nil {
		return 0, artifactErr
	}
	if finalizeErr := s.finalizeSoulAgentRegistrationV2Identity(ctx, identity, capsNorm, claimLevels, plan.nextVersion, now); finalizeErr != nil {
		return 0, finalizeErr
	}
	return plan.nextVersion, nil
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
	return publishSoulAgentRegistrationTyped(
		s,
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
		extraWrites,
		(*Server).deriveSoulRegistrationV2NextVersion,
		(*Server).validateSoulRegistrationPreviousVersionURI,
	)
}

func (s *Server) deriveSoulRegistrationV2NextVersion(agentIDHex string, regV2 *soul.RegistrationFileV2) (prev int, next int, appErr *apptheory.AppError) {
	if s == nil || regV2 == nil {
		return 0, 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return deriveSoulRegistrationNextVersion(agentIDHex, regV2.PreviousVersionURI, s.cfg.SoulPackBucketName)
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
