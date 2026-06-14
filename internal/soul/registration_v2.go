package soul

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/equaltoai/lesser-host/internal/domains"
)

var (
	errRegistrationNil = errors.New("registration is nil")

	regexAgentIDHex64 = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	regexHexSig       = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
)

const (
	registrationAuthorityWalletPrincipal = "wallet_principal"
	registrationAuthorityInstanceTrust   = "instance_trust"
	registrationAnchorHostedOffchain     = "hosted_offchain"
	registrationAnchorImmutableOnchain   = "immutable_onchain"
)

// RegistrationFileV2 is the v2 Soul Registration File schema (lesser-soul/SPEC.md Appendix A).
type RegistrationFileV2 struct {
	Version   string                 `json:"version"`
	AgentID   string                 `json:"agentId"`
	Domain    string                 `json:"domain"`
	LocalID   string                 `json:"localId"`
	Wallet    string                 `json:"wallet"`
	Principal PrincipalDeclarationV2 `json:"principal"`
	// AuthorityModel is explicit for hosted/off-chain registrations that are
	// authorized by the managed instance API key rather than a wallet principal.
	AuthorityModel string `json:"authorityModel,omitempty"`
	AnchorState    string `json:"anchorState,omitempty"`

	SelfDescription SelfDescriptionV2   `json:"selfDescription"`
	Capabilities    []CapabilityV2      `json:"capabilities"`
	Boundaries      []BoundaryV2        `json:"boundaries"`
	Transparency    map[string]any      `json:"transparency"`
	Continuity      []ContinuityEntryV2 `json:"continuity,omitempty"`
	Endpoints       EndpointsV2         `json:"endpoints"`
	Lifecycle       LifecycleV2         `json:"lifecycle"`

	PreviousVersionURI *string `json:"previousVersionUri,omitempty"`
	ChangeSummary      *string `json:"changeSummary,omitempty"`

	Attestations AttestationsV2 `json:"attestations"`
	Created      string         `json:"created"`
	Updated      string         `json:"updated"`
}

type PrincipalDeclarationV2 struct {
	Type        string `json:"type"`
	Identifier  string `json:"identifier"`
	DisplayName string `json:"displayName,omitempty"`
	ContactURI  string `json:"contactUri,omitempty"`
	Declaration string `json:"declaration"`
	Signature   string `json:"signature"`
	DeclaredAt  string `json:"declaredAt"`
}

func (p PrincipalDeclarationV2) HasFields() bool {
	return strings.TrimSpace(p.Type) != "" ||
		strings.TrimSpace(p.Identifier) != "" ||
		strings.TrimSpace(p.DisplayName) != "" ||
		strings.TrimSpace(p.ContactURI) != "" ||
		strings.TrimSpace(p.Declaration) != "" ||
		strings.TrimSpace(p.Signature) != "" ||
		strings.TrimSpace(p.DeclaredAt) != ""
}

var ErrPrincipalBindingMissing = errors.New("verified principal binding is missing")

// PrincipalDeclarationBindingV2 carries the host-verified principal fields that
// were accepted during the registration proof flow. The registration file leaf
// validator remains compatible with the v2 public schema, while host publication
// uses this binding to prevent a valid principal declaration from being replayed
// into another agent's registration.
type PrincipalDeclarationBindingV2 struct {
	Identifier  string
	Declaration string
	Signature   string
	DeclaredAt  string
}

type SelfDescriptionV2 struct {
	Purpose      string `json:"purpose"`
	Constraints  string `json:"constraints,omitempty"`
	Commitments  string `json:"commitments,omitempty"`
	Limitations  string `json:"limitations,omitempty"`
	AuthoredBy   string `json:"authoredBy"`
	MintingModel string `json:"mintingModel,omitempty"`
}

type CapabilityV2 struct {
	Capability    string         `json:"capability"`
	Scope         string         `json:"scope"`
	Constraints   map[string]any `json:"constraints,omitempty"`
	ClaimLevel    string         `json:"claimLevel"`
	LastValidated string         `json:"lastValidated,omitempty"`
	ValidationRef string         `json:"validationRef,omitempty"`
	DegradesTo    string         `json:"degradesTo,omitempty"`
}

type BoundaryV2 struct {
	ID             string  `json:"id"`
	Category       string  `json:"category"`
	Statement      string  `json:"statement"`
	Rationale      string  `json:"rationale,omitempty"`
	AddedAt        string  `json:"addedAt"`
	AddedInVersion string  `json:"addedInVersion"`
	Supersedes     *string `json:"supersedes,omitempty"`
	Signature      string  `json:"signature"`
}

type ContinuityEntryV2 struct {
	Type       string   `json:"type"`
	Timestamp  string   `json:"timestamp"`
	Summary    string   `json:"summary"`
	Recovery   string   `json:"recovery,omitempty"`
	References []string `json:"references,omitempty"`
	Signature  string   `json:"signature"`
}

type EndpointsV2 struct {
	ActivityPub string `json:"activitypub,omitempty"`
	MCP         string `json:"mcp,omitempty"`
	Soul        string `json:"soul,omitempty"`
}

type LifecycleV2 struct {
	Status           string  `json:"status"`
	StatusChangedAt  string  `json:"statusChangedAt"`
	Reason           *string `json:"reason,omitempty"`
	SuccessorAgentID *string `json:"successorAgentId,omitempty"`
}

type AttestationsV2 struct {
	HostAttestation string `json:"hostAttestation,omitempty"`
	HostAuthority   string `json:"hostAuthority,omitempty"`
	SelfAttestation string `json:"selfAttestation"`
}

func ParseRegistrationFileV2(body []byte) (*RegistrationFileV2, error) {
	if len(body) == 0 {
		return nil, errors.New("registration body is required")
	}
	var reg RegistrationFileV2
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

func (r *RegistrationFileV2) Validate() error {
	if r == nil {
		return errRegistrationNil
	}
	authorityModel, err := validateRegistrationAuthorityModel(r.AuthorityModel)
	if err != nil {
		return err
	}
	if err := validateRegistrationVersion(r.Version, "2"); err != nil {
		return err
	}
	if err := validateRegistrationAuthorityAndAnchor(authorityModel, r.AnchorState); err != nil {
		return err
	}
	if err := validateRegistrationIdentityForAuthority(r.AgentID, r.Domain, r.LocalID, r.Wallet, authorityModel); err != nil {
		return err
	}
	if err := r.validateCoreSectionsForAuthority(authorityModel); err != nil {
		return err
	}
	if err := validateOptionalPreviousVersionURI(r.PreviousVersionURI); err != nil {
		return err
	}
	if err := r.Attestations.ValidateForAuthority(authorityModel); err != nil {
		return fmt.Errorf("attestations: %w", err)
	}
	return validateRegistrationTimestamps(r.Created, r.Updated)
}

func validateRegistrationAuthorityModel(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", registrationAuthorityWalletPrincipal:
		return registrationAuthorityWalletPrincipal, nil
	case registrationAuthorityInstanceTrust:
		return registrationAuthorityInstanceTrust, nil
	default:
		return "", errors.New("authorityModel is invalid")
	}
}

func validateRegistrationAuthorityAndAnchor(authorityModel string, anchorState string) error {
	anchorState = strings.ToLower(strings.TrimSpace(anchorState))
	switch anchorState {
	case "", registrationAnchorHostedOffchain, registrationAnchorImmutableOnchain:
	default:
		return errors.New("anchorState is invalid")
	}
	if authorityModel == registrationAuthorityInstanceTrust && anchorState != registrationAnchorHostedOffchain {
		return errors.New("anchorState must be hosted_offchain for authorityModel=instance_trust")
	}
	return nil
}

func validateRegistrationVersion(version string, expected string) error {
	if strings.TrimSpace(version) != expected {
		return fmt.Errorf("version must be %q", expected)
	}
	return nil
}

func validateRegistrationIdentityForAuthority(agentID string, domain string, localID string, wallet string, authorityModel string) error {
	if !regexAgentIDHex64.MatchString(strings.ToLower(strings.TrimSpace(agentID))) {
		return errors.New("agentId must be a 0x-prefixed 32-byte hex string")
	}

	normalizedDomain, err := domains.NormalizeDomain(domain)
	if err != nil || normalizedDomain == "" {
		return errors.New("domain is invalid")
	}

	normalizedLocalID, err := ValidateManagedHandle(localID)
	if err != nil || normalizedLocalID == "" {
		return errors.New("localId is invalid")
	}

	wallet = strings.TrimSpace(wallet)
	if authorityModel == registrationAuthorityInstanceTrust {
		if wallet != "" {
			return errors.New("wallet must be omitted for authorityModel=instance_trust")
		}
		return nil
	}

	if !common.IsHexAddress(wallet) {
		return errors.New("wallet is invalid")
	}
	return nil
}

func (r *RegistrationFileV2) validateCoreSectionsForAuthority(authorityModel string) error {
	if authorityModel == registrationAuthorityInstanceTrust {
		if r.Principal.HasFields() {
			return errors.New("principal must be omitted for authorityModel=instance_trust")
		}
	} else if err := r.Principal.ValidateWithDomainSeparation(strings.ToLower(strings.TrimSpace(r.AgentID))); err != nil {
		return fmt.Errorf("principal: %w", err)
	}
	if err := validateRegistrationSharedCoreSections(
		r.AgentID,
		r.SelfDescription,
		r.Capabilities,
		r.Boundaries,
		r.Transparency,
		r.Endpoints,
		r.Lifecycle,
		authorityModel,
	); err != nil {
		return err
	}
	return nil
}

func validateRegistrationSharedCoreSections(
	agentID string,
	selfDescription SelfDescriptionV2,
	capabilities []CapabilityV2,
	boundaries []BoundaryV2,
	transparency map[string]any,
	endpoints EndpointsV2,
	lifecycle LifecycleV2,
	authorityModel string,
) error {
	_ = agentID
	if err := selfDescription.Validate(); err != nil {
		return fmt.Errorf("selfDescription: %w", err)
	}
	if err := validateCapabilitiesV2(capabilities); err != nil {
		return err
	}
	if err := validateBoundariesV2ForAuthority(boundaries, authorityModel); err != nil {
		return err
	}
	if transparency == nil {
		return errors.New("transparency is required")
	}
	if err := endpoints.Validate(); err != nil {
		return fmt.Errorf("endpoints: %w", err)
	}
	if err := lifecycle.Validate(); err != nil {
		return fmt.Errorf("lifecycle: %w", err)
	}
	return nil
}

func validateCapabilitiesV2(capabilities []CapabilityV2) error {
	if len(capabilities) == 0 {
		return errors.New("capabilities must be a non-empty array")
	}
	for i := range capabilities {
		if err := capabilities[i].Validate(); err != nil {
			return fmt.Errorf("capabilities[%d]: %w", i, err)
		}
	}
	return nil
}

func validateBoundariesV2ForAuthority(boundaries []BoundaryV2, authorityModel string) error {
	if len(boundaries) == 0 {
		return errors.New("boundaries must be a non-empty array")
	}
	for i := range boundaries {
		if err := boundaries[i].ValidateForAuthority(authorityModel); err != nil {
			return fmt.Errorf("boundaries[%d]: %w", i, err)
		}
	}
	return nil
}

func validateOptionalPreviousVersionURI(previousVersionURI *string) error {
	if previousVersionURI == nil {
		return nil
	}
	uri := strings.TrimSpace(*previousVersionURI)
	if uri == "" {
		return nil
	}
	if _, err := url.ParseRequestURI(uri); err != nil {
		return errors.New("previousVersionUri is invalid")
	}
	return nil
}

func validateRegistrationTimestamps(created string, updated string) error {
	if err := validateRFC3339(created); err != nil {
		return fmt.Errorf("created: %w", err)
	}
	if err := validateRFC3339(updated); err != nil {
		return fmt.Errorf("updated: %w", err)
	}
	return nil
}

func (p *PrincipalDeclarationV2) Validate() error {
	return p.ValidateWithDomainSeparation("")
}

// ValidateWithDomainSeparation validates the principal declaration. When agentID is
// non-empty, the signature is additionally verified against a domain-separated digest
// that binds the declaration to the specific agent, preventing cross-agent replay.
// When agentID is empty, only the declaration-level signature is checked (backward
// compatible with existing v2 registration files).
func (p *PrincipalDeclarationV2) ValidateWithDomainSeparation(agentID string) error {
	if p == nil {
		return errors.New("is required")
	}
	t := strings.ToLower(strings.TrimSpace(p.Type))
	switch t {
	case "individual", "organization":
	default:
		return errors.New("type must be \"individual\" or \"organization\"")
	}
	identifier := strings.TrimSpace(p.Identifier)
	if identifier == "" {
		return errors.New("identifier is required")
	}
	if !common.IsHexAddress(identifier) {
		return errors.New("identifier must be an Ethereum address")
	}
	declaration := p.Declaration
	if strings.TrimSpace(declaration) == "" || len(strings.TrimSpace(declaration)) < 10 {
		return errors.New("declaration is required")
	}
	sig := strings.TrimSpace(p.Signature)
	if !regexHexSig.MatchString(sig) {
		return errors.New("signature must be hex (0x...)")
	}

	// Primary validation: declaration-level signature (backward-compatible with
	// existing v2 registration files). Some principals sign only the declaration
	// text; agent binding is enforced at the host level through
	// ValidateVerifiedBinding and the registration proof flow.
	declarationDigest := crypto.Keccak256([]byte(declaration))
	declErr := verifyEIP191SignatureOverDigest(identifier, declarationDigest, sig)

	// Secondary validation: when agentID is known, also try domain-separated digest
	// that binds the declaration to the specific agent. New registration files
	// SHOULD use this format to prevent the same principal declaration from being
	// replayed into another agent's registration.
	if agentID != "" {
		domainCtx := "lesser-soul-v2-principal:" + strings.ToLower(strings.TrimSpace(agentID)) + "\n" + declaration
		domainDigest := crypto.Keccak256([]byte(domainCtx))
		if domainErr := verifyEIP191SignatureOverDigest(identifier, domainDigest, sig); domainErr == nil {
			// Agent-bound signature is valid — this is the preferred format.
		} else if declErr != nil {
			return errors.New("signature is invalid")
		}
		// If agent-bound verification failed but declaration-level passed, accept
		// for backward compatibility (host-level binding will catch cross-agent
		// replay through ValidateVerifiedBinding).
		return p.validateStructFields()
	}

	if declErr != nil {
		return errors.New("signature is invalid")
	}
	return p.validateStructFields()
}

// validateStructFields validates the non-signature structural fields of the
// principal declaration.
func (p *PrincipalDeclarationV2) validateStructFields() error {
	if strings.TrimSpace(p.ContactURI) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(p.ContactURI)); err != nil {
			return errors.New("contactUri is invalid")
		}
	}
	if err := validateRFC3339(p.DeclaredAt); err != nil {
		return fmt.Errorf("declaredAt: %w", err)
	}
	return nil
}

func (p *PrincipalDeclarationV2) ValidateVerifiedBinding(binding PrincipalDeclarationBindingV2) error {
	if p == nil {
		return errors.New("principal is required")
	}
	if strings.TrimSpace(binding.Identifier) == "" ||
		strings.TrimSpace(binding.Declaration) == "" ||
		strings.TrimSpace(binding.Signature) == "" ||
		strings.TrimSpace(binding.DeclaredAt) == "" {
		return ErrPrincipalBindingMissing
	}
	// The stored signature is intentionally presence-only here. A v2 public
	// registration can carry the schema's declaration-only signature while host
	// stores the domain-separated proof signature accepted during registration.
	if !strings.EqualFold(strings.TrimSpace(p.Identifier), strings.TrimSpace(binding.Identifier)) {
		return errors.New("principal does not match verified agent principal")
	}
	if strings.TrimSpace(p.Declaration) != strings.TrimSpace(binding.Declaration) {
		return errors.New("principal does not match verified agent principal")
	}
	if strings.TrimSpace(p.DeclaredAt) != strings.TrimSpace(binding.DeclaredAt) {
		return errors.New("principal does not match verified agent principal")
	}
	return nil
}

func verifyEIP191SignatureOverDigest(address string, digest []byte, signature string) error {
	address = strings.TrimSpace(address)
	if !common.IsHexAddress(address) {
		return errors.New("invalid address")
	}

	sig, err := hexutil.Decode(signature)
	if err != nil {
		return err
	}
	if len(sig) != 65 {
		return errors.New("invalid signature")
	}

	if sig[64] == 27 || sig[64] == 28 {
		sig[64] -= 27
	}

	msgHash := accounts.TextHash(digest)
	pubKey, err := crypto.SigToPub(msgHash, sig)
	if err != nil {
		return err
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	if !strings.EqualFold(recoveredAddr.Hex(), address) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (s *SelfDescriptionV2) Validate() error {
	if s == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(s.Purpose) == "" || len(strings.TrimSpace(s.Purpose)) < 10 {
		return errors.New("purpose is required")
	}
	switch strings.ToLower(strings.TrimSpace(s.AuthoredBy)) {
	case "agent", "principal":
	default:
		return errors.New("authoredBy must be \"agent\" or \"principal\"")
	}
	return nil
}

func (c *CapabilityV2) Validate() error {
	if c == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(c.Capability) == "" {
		return errors.New("capability is required")
	}
	if strings.TrimSpace(c.Scope) == "" {
		return errors.New("scope is required")
	}
	switch strings.ToLower(strings.TrimSpace(c.ClaimLevel)) {
	case "self-declared", "challenge-passed", "peer-endorsed", "deprecated":
	default:
		return errors.New("claimLevel is invalid")
	}
	if strings.TrimSpace(c.LastValidated) != "" {
		if err := validateRFC3339(c.LastValidated); err != nil {
			return fmt.Errorf("lastValidated: %w", err)
		}
	}
	return nil
}

func (b *BoundaryV2) Validate() error {
	return b.ValidateForAuthority(registrationAuthorityWalletPrincipal)
}

func (b *BoundaryV2) ValidateForAuthority(authorityModel string) error {
	if b == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("id is required")
	}
	switch strings.ToLower(strings.TrimSpace(b.Category)) {
	case "refusal", "scope_limit", "ethical_commitment", "circuit_breaker":
	default:
		return errors.New("category is invalid")
	}
	if strings.TrimSpace(b.Statement) == "" {
		return errors.New("statement is required")
	}
	if err := validateRFC3339(b.AddedAt); err != nil {
		return fmt.Errorf("addedAt: %w", err)
	}
	if strings.TrimSpace(b.AddedInVersion) == "" {
		return errors.New("addedInVersion is required")
	}
	sig := strings.TrimSpace(b.Signature)
	if authorityModel == registrationAuthorityInstanceTrust {
		if sig != "" {
			return errors.New("signature must be omitted for authorityModel=instance_trust")
		}
		return nil
	}
	if !regexHexSig.MatchString(sig) {
		return errors.New("signature must be hex (0x...)")
	}
	return nil
}

func (e *EndpointsV2) Validate() error {
	if e == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(e.ActivityPub) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(e.ActivityPub)); err != nil {
			return errors.New("activitypub is invalid")
		}
	}
	if strings.TrimSpace(e.MCP) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(e.MCP)); err != nil {
			return errors.New("mcp is invalid")
		}
	}
	if strings.TrimSpace(e.Soul) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(e.Soul)); err != nil {
			return errors.New("soul is invalid")
		}
	}
	return nil
}

func (l *LifecycleV2) Validate() error {
	if l == nil {
		return errors.New("is required")
	}
	switch strings.ToLower(strings.TrimSpace(l.Status)) {
	case "active", "suspended", "self_suspended", "archived", "succeeded":
	default:
		return errors.New("status is invalid")
	}
	if err := validateRFC3339(l.StatusChangedAt); err != nil {
		return fmt.Errorf("statusChangedAt: %w", err)
	}
	if l.SuccessorAgentID != nil && strings.TrimSpace(*l.SuccessorAgentID) != "" {
		if !regexAgentIDHex64.MatchString(strings.ToLower(strings.TrimSpace(*l.SuccessorAgentID))) {
			return errors.New("successorAgentId is invalid")
		}
	}
	return nil
}

func (a *AttestationsV2) Validate() error {
	return a.ValidateForAuthority(registrationAuthorityWalletPrincipal)
}

func (a *AttestationsV2) ValidateForAuthority(authorityModel string) error {
	if a == nil {
		return errors.New("is required")
	}
	if authorityModel == registrationAuthorityInstanceTrust {
		if strings.TrimSpace(a.HostAuthority) != registrationAuthorityInstanceTrust {
			return errors.New("hostAuthority must be instance_trust")
		}
		if strings.TrimSpace(a.SelfAttestation) != "" {
			return errors.New("selfAttestation must be omitted for authorityModel=instance_trust")
		}
	} else {
		if strings.TrimSpace(a.SelfAttestation) == "" {
			return errors.New("selfAttestation is required")
		}
		if !regexHexSig.MatchString(strings.TrimSpace(a.SelfAttestation)) {
			return errors.New("selfAttestation must be hex (0x...)")
		}
	}
	if strings.TrimSpace(a.HostAttestation) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(a.HostAttestation)); err != nil {
			return errors.New("hostAttestation is invalid")
		}
	}
	return nil
}

func validateRFC3339(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("is required")
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		// Allow RFC3339Nano too.
		if _, err2 := time.Parse(time.RFC3339Nano, raw); err2 != nil {
			return errors.New("must be an RFC3339 timestamp")
		}
	}
	return nil
}
