package controlplane

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type mintConversationFinalizeRegistrationOptions struct {
	AuthorityModel    string
	AnchorState       string
	IncludeSignatures bool
}

func (s *Server) buildMintConversationFinalizeV2Registration(
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	issuedAt time.Time,
	nextVersion int,
	selfAttestation string,
) (reg map[string]any, regV2 *soul.RegistrationFileV2, digest []byte, capsNorm []string, claimLevels map[string]string, appErr *apptheory.AppTheoryError) {
	return s.buildMintConversationFinalizeV2RegistrationWithOptions(agentIDHex, identity, decl, boundarySignatures, issuedAt, nextVersion, selfAttestation, mintConversationFinalizeRegistrationOptions{
		AuthorityModel:    models.SoulAuthorityModelWalletPrincipal,
		AnchorState:       soulIdentityAnchorState(identity),
		IncludeSignatures: true,
	})
}

func (s *Server) buildMintConversationFinalizeV2RegistrationWithOptions(
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	issuedAt time.Time,
	nextVersion int,
	selfAttestation string,
	opts mintConversationFinalizeRegistrationOptions,
) (reg map[string]any, regV2 *soul.RegistrationFileV2, digest []byte, capsNorm []string, claimLevels map[string]string, appErr *apptheory.AppTheoryError) {
	if s == nil || identity == nil {
		return nil, nil, nil, nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, nil, nil, nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if nextVersion <= 0 {
		return nil, nil, nil, nil, nil, newAppTheoryError("app.bad_request", "invalid version")
	}

	issuedAt = issuedAt.UTC()
	issuedAtStr := issuedAt.Format(time.RFC3339Nano)
	authorityModel := normalizeSoulAuthorityModel(opts.AuthorityModel)
	if authorityModel == "" {
		authorityModel = models.SoulAuthorityModelWalletPrincipal
	}
	anchorState := strings.ToLower(strings.TrimSpace(opts.AnchorState))
	if anchorState == "" {
		anchorState = soulIdentityAnchorState(identity)
	}
	if anchorState == "" {
		anchorState = models.SoulAnchorStateHostedOffchain
	}
	lifecycleStatus := mintConversationFinalizeLifecycleStatus(identity)
	selfDesc := buildMintConversationFinalizeSelfDescription(decl.SelfDescription)
	capsAny := buildMintConversationFinalizeCapabilities(decl.Capabilities)
	boundsAny := buildMintConversationFinalizeBoundariesWithOptions(decl.Boundaries, boundarySignatures, issuedAtStr, nextVersion, opts.IncludeSignatures)
	changeSummary := mintConversationFinalizeChangeSummary(authorityModel, nextVersion)
	transparency := nonNilMintConversationTransparency(decl.Transparency)

	reg = map[string]any{
		"version":         "2",
		"agentId":         agentIDHex,
		"domain":          strings.TrimSpace(identity.Domain),
		"localId":         strings.TrimSpace(identity.LocalID),
		"authorityModel":  authorityModel,
		"anchorState":     anchorState,
		"selfDescription": selfDesc,
		"capabilities":    capsAny,
		"boundaries":      boundsAny,
		"transparency":    transparency,
		"endpoints":       map[string]any{},
		"lifecycle": map[string]any{
			"status":          lifecycleStatus,
			"statusChangedAt": issuedAtStr,
		},
		"created":       issuedAtStr,
		"updated":       issuedAtStr,
		"changeSummary": changeSummary,
	}
	if strings.TrimSpace(identity.Wallet) != "" {
		reg["wallet"] = strings.TrimSpace(identity.Wallet)
	}
	attestations := map[string]any{}
	if authorityModel == models.SoulAuthorityModelInstanceTrust {
		attestations["hostAuthority"] = models.SoulAuthorityModelInstanceTrust
	} else {
		reg["principal"] = buildMintConversationFinalizePrincipal(identity)
		attestations["selfAttestation"] = strings.TrimSpace(selfAttestation)
	}
	reg["attestations"] = attestations
	if nextVersion > 1 {
		prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
		reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	}

	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, nil, nil, nil, newAppTheoryError("app.bad_request", "invalid registration JSON")
	}
	parsed, appErr := parseMintConversationFinalizeV2Registration(regBytes)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}

	if authorityModel == models.SoulAuthorityModelWalletPrincipal {
		digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
		if appErr != nil {
			return nil, nil, nil, nil, nil, appErr
		}
	}

	// Capability indexing inputs.
	caps := extractCapabilityNames(reg)
	capsNorm = normalizeSoulCapabilitiesLoose(caps)
	claimLevels = extractCapabilityClaimLevels(reg)

	return reg, parsed, digest, capsNorm, claimLevels, nil
}

func mintConversationFinalizeChangeSummary(authorityModel string, nextVersion int) string {
	if authorityModel == models.SoulAuthorityModelInstanceTrust {
		return fmt.Sprintf("Publish hosted/off-chain instance-trust declarations (v%d)", nextVersion)
	}
	return fmt.Sprintf("Publish mint conversation declarations (v%d)", nextVersion)
}

func mintConversationFinalizeLifecycleStatus(identity *models.SoulAgentIdentity) string {
	lifecycleStatus := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if lifecycleStatus == "" {
		lifecycleStatus = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	if lifecycleStatus == "" || lifecycleStatus == models.SoulAgentStatusPending {
		return models.SoulAgentStatusActive
	}
	return lifecycleStatus
}

func mintConversationFinalizeIdentityForPublication(identity *models.SoulAgentIdentity) *models.SoulAgentIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	lifecycleStatus := strings.ToLower(strings.TrimSpace(copy.LifecycleStatus))
	if lifecycleStatus == "" {
		lifecycleStatus = strings.ToLower(strings.TrimSpace(copy.Status))
	}
	if lifecycleStatus == "" || lifecycleStatus == models.SoulAgentStatusPending {
		copy.Status = models.SoulAgentStatusActive
		copy.LifecycleStatus = models.SoulAgentStatusActive
		if strings.TrimSpace(copy.AnchorState) == "" {
			copy.AnchorState = models.SoulAnchorStateHostedOffchain
		}
		applyHostedBoundSoulPolicyDefaults(&copy)
	}
	return &copy
}

func requireMintConversationFinalizeVersionAndActive(identity *models.SoulAgentIdentity, nextVersion int, expectedVersion int) *apptheory.AppTheoryError {
	if identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if identity.SelfDescriptionVersion > nextVersion {
		return newAppTheoryError("app.conflict", "agent has advanced beyond this version")
	}
	if identity.SelfDescriptionVersion < expectedVersion {
		return newAppTheoryError("app.conflict", "version conflict; reload and try again")
	}
	return requireMintConversationFinalizeActiveIdentity(identity)
}

func requireMintConversationFinalizeActiveIdentity(identity *models.SoulAgentIdentity) *apptheory.AppTheoryError {
	if identity == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	lifecycleStatus := ""
	lifecycleStatus = strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if lifecycleStatus == "" {
		lifecycleStatus = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	switch lifecycleStatus {
	case "", models.SoulAgentStatusPending, models.SoulAgentStatusActive:
		return nil
	default:
		return newAppTheoryError("app.conflict", "agent must be active or pending before publishing mint conversation registration")
	}
}

func buildMintConversationFinalizePrincipal(identity *models.SoulAgentIdentity) map[string]any {
	return map[string]any{
		"type":        "individual",
		"identifier":  strings.TrimSpace(identity.PrincipalAddress),
		"declaration": strings.TrimSpace(identity.PrincipalDeclaration),
		"signature":   strings.TrimSpace(identity.PrincipalSignature),
		"declaredAt":  strings.TrimSpace(identity.PrincipalDeclaredAt),
	}
}

func buildMintConversationFinalizeSelfDescription(selfDesc soul.SelfDescriptionV2) map[string]any {
	out := map[string]any{
		"purpose":    strings.TrimSpace(selfDesc.Purpose),
		"authoredBy": strings.ToLower(strings.TrimSpace(selfDesc.AuthoredBy)),
	}
	if strings.TrimSpace(selfDesc.Constraints) != "" {
		out["constraints"] = strings.TrimSpace(selfDesc.Constraints)
	}
	if strings.TrimSpace(selfDesc.Commitments) != "" {
		out["commitments"] = strings.TrimSpace(selfDesc.Commitments)
	}
	if strings.TrimSpace(selfDesc.Limitations) != "" {
		out["limitations"] = strings.TrimSpace(selfDesc.Limitations)
	}
	if strings.TrimSpace(selfDesc.MintingModel) != "" {
		out["mintingModel"] = strings.TrimSpace(selfDesc.MintingModel)
	}
	return out
}

func buildMintConversationFinalizeCapabilities(capabilities []soul.CapabilityV2) []any {
	out := make([]any, 0, len(capabilities))
	for i := range capabilities {
		c := capabilities[i]
		item := map[string]any{
			"capability": strings.TrimSpace(c.Capability),
			"scope":      strings.TrimSpace(c.Scope),
			"claimLevel": strings.ToLower(strings.TrimSpace(c.ClaimLevel)),
		}
		if len(c.Constraints) > 0 {
			item["constraints"] = c.Constraints
		}
		if strings.TrimSpace(c.LastValidated) != "" {
			item["lastValidated"] = strings.TrimSpace(c.LastValidated)
		}
		if strings.TrimSpace(c.ValidationRef) != "" {
			item["validationRef"] = strings.TrimSpace(c.ValidationRef)
		}
		if strings.TrimSpace(c.DegradesTo) != "" {
			item["degradesTo"] = strings.TrimSpace(c.DegradesTo)
		}
		out = append(out, item)
	}
	return out
}

func buildMintConversationFinalizeBoundariesWithOptions(boundaries []soul.BoundaryV2, boundarySignatures map[string]string, issuedAt string, nextVersion int, includeSignatures bool) []any {
	out := make([]any, 0, len(boundaries))
	for i := range boundaries {
		b := boundaries[i]
		item := map[string]any{
			"id":             strings.TrimSpace(b.ID),
			"category":       strings.ToLower(strings.TrimSpace(b.Category)),
			"statement":      strings.TrimSpace(b.Statement),
			"addedAt":        issuedAt,
			"addedInVersion": strconv.Itoa(nextVersion),
		}
		if includeSignatures {
			item["signature"] = strings.TrimSpace(boundarySignatures[strings.TrimSpace(b.ID)])
		}
		if strings.TrimSpace(b.Rationale) != "" {
			item["rationale"] = strings.TrimSpace(b.Rationale)
		}
		if b.Supersedes != nil && strings.TrimSpace(*b.Supersedes) != "" {
			item["supersedes"] = strings.TrimSpace(*b.Supersedes)
		}
		out = append(out, item)
	}
	return out
}

func nonNilMintConversationTransparency(transparency map[string]any) map[string]any {
	if transparency == nil {
		return map[string]any{}
	}
	return transparency
}

func parseMintConversationFinalizeV2Registration(regBytes []byte) (*soul.RegistrationFileV2, *apptheory.AppTheoryError) {
	parsed, err := soul.ParseRegistrationFileV2(regBytes)
	if err != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid v2 registration schema")
	}
	if err := parsed.Validate(); err != nil {
		return nil, newAppTheoryError("app.bad_request", err.Error())
	}
	return parsed, nil
}
