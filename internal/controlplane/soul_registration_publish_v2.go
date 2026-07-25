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
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

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
	derive func() (int, int, *apptheory.AppTheoryError),
	validatePrevious func(int) *apptheory.AppTheoryError,
	extraWrites func(tx core.TransactionBuilder) error,
) (int, *apptheory.AppTheoryError) {
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
			return 0, newAppTheoryError("app.conflict", "version conflict; reload and try again")
		}
		return 0, newAppTheoryError("app.internal", "failed to record version history")
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
) (versionNumber int, appErr *apptheory.AppTheoryError) {
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
) (versionNumber int, appErr *apptheory.AppTheoryError) {
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

func (s *Server) deriveSoulRegistrationV2NextVersion(agentIDHex string, regV2 *soul.RegistrationFileV2) (prev int, next int, appErr *apptheory.AppTheoryError) {
	if s == nil || regV2 == nil {
		return 0, 0, newAppTheoryError("app.internal", "internal error")
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

func (s *Server) ensureSoulRegistrationS3Artifacts(ctx context.Context, agentIDHex string, version int, regBytes []byte, regSHA256 string) *apptheory.AppTheoryError {
	if s == nil || s.soulPacks == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if version <= 0 {
		return newAppTheoryError("app.internal", "internal error")
	}

	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, version)
	if appErr := s.ensureSoulPackObjectSHA256(ctx, versionedKey, regBytes, regSHA256); appErr != nil {
		return appErr
	}

	currentKey := soulRegistrationS3Key(agentIDHex)
	if err := s.soulPacks.PutObject(ctx, currentKey, regBytes, "application/json", "private, max-age=0"); err != nil {
		return newAppTheoryError("app.internal", "failed to publish registration")
	}

	return nil
}

func (s *Server) ensureSoulPackObjectSHA256(ctx context.Context, key string, expectedBody []byte, expectedSHA256 string) *apptheory.AppTheoryError {
	if s == nil || s.soulPacks == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return newAppTheoryError("app.internal", "internal error")
	}
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if expectedSHA256 == "" || len(expectedSHA256) != 64 {
		return newAppTheoryError("app.internal", "internal error")
	}

	body, _, _, err := s.soulPacks.GetObject(ctx, key, 512*1024)
	if err == nil {
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != expectedSHA256 {
			log.Printf("controlplane: soul_integrity s3_sha_mismatch key=%s expected_sha=%s got_sha=%s", key, expectedSHA256, got)
			return newAppTheoryError("app.conflict", "registration artifact integrity violation")
		}
		return nil
	}

	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		if putErr := s.soulPacks.PutObject(ctx, key, expectedBody, "application/json", "private, max-age=0"); putErr != nil {
			return newAppTheoryError("app.internal", "failed to publish versioned registration")
		}
		return nil
	}

	return newAppTheoryError("app.internal", "failed to fetch registration")
}

func (s *Server) finalizeSoulAgentRegistrationV2Identity(ctx context.Context, identity *models.SoulAgentIdentity, capsNorm []string, claimLevels map[string]string, version int, now time.Time) *apptheory.AppTheoryError {
	if s == nil || identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if version <= 0 {
		return newAppTheoryError("app.internal", "internal error")
	}

	if appErr := s.updateSoulAgentCapabilities(ctx, identity, capsNorm, claimLevels, now, true); appErr != nil {
		return appErr
	}

	updates := make([]string, 0, 16)
	if identity.SelfDescriptionVersion != version {
		identity.SelfDescriptionVersion = version
		updates = append(updates, "SelfDescriptionVersion")
	}

	updates = append(updates, soulIdentityRegistrationPublishActivationFields(identity, true)...)

	if len(updates) > 0 {
		identity.UpdatedAt = now.UTC()
		updates = append(updates, "UpdatedAt")
		if err := s.store.DB.WithContext(ctx).Model(identity).IfExists().Update(updates...); err != nil {
			return newAppTheoryError("app.internal", "failed to update identity version")
		}
	}

	return nil
}

func (s *Server) ensureSoulAgentRegistrationPublishedIdentityActive(ctx context.Context, identity *models.SoulAgentIdentity, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if identity.SelfDescriptionVersion <= 0 {
		return nil
	}
	updates := soulIdentityRegistrationPublishActivationFields(identity, false)
	if len(updates) == 0 {
		return nil
	}
	identity.UpdatedAt = now.UTC()
	_ = identity.UpdateKeys()
	updates = append(updates, "UpdatedAt")
	if err := s.store.DB.WithContext(ctx).Model(identity).IfExists().Update(updates...); err != nil {
		return newAppTheoryError("app.internal", "failed to activate identity publication")
	}
	return nil
}

func soulIdentityRegistrationPublishActivationFields(identity *models.SoulAgentIdentity, persistAlreadyActive bool) []string {
	if !shouldPersistSoulIdentityActivationOnRegistrationPublish(identity, persistAlreadyActive) {
		return nil
	}
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive
	if strings.TrimSpace(identity.AnchorState) == "" {
		identity.AnchorState = models.SoulAnchorStateHostedOffchain
	}
	applyHostedBoundSoulPolicyDefaults(identity)
	return []string{
		"Status",
		"LifecycleStatus",
		"PolicyVersion",
		"AnchorState",
		"OperationalBinding",
		"CapabilityPolicyVersion",
		"CallerAccessPaymentPolicyVersion",
		"EmailDefaultAllowed",
		"PhoneEntitlementStatus",
		"SMSAllowed",
		"VoiceAllowed",
		"PublicPaidCallerAccess",
		"PolicyMigrationState",
	}
}

func shouldPersistSoulIdentityActivationOnRegistrationPublish(identity *models.SoulAgentIdentity, persistAlreadyActive bool) bool {
	if identity == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	if status == "" || status == models.SoulAgentStatusPending {
		return true
	}
	return persistAlreadyActive && status == models.SoulAgentStatusActive
}
