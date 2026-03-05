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

// RegistrationFileV2 is the v2 Soul Registration File schema (lesser-soul/SPEC.md Appendix A).
type RegistrationFileV2 struct {
	Version   string                 `json:"version"`
	AgentID   string                 `json:"agentId"`
	Domain    string                 `json:"domain"`
	LocalID   string                 `json:"localId"`
	Wallet    string                 `json:"wallet"`
	Principal PrincipalDeclarationV2 `json:"principal"`

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
	if strings.TrimSpace(r.Version) != "2" {
		return errors.New("version must be \"2\"")
	}

	agentID := strings.ToLower(strings.TrimSpace(r.AgentID))
	if !regexAgentIDHex64.MatchString(agentID) {
		return errors.New("agentId must be a 0x-prefixed 32-byte hex string")
	}

	domain, err := domains.NormalizeDomain(r.Domain)
	if err != nil || domain == "" {
		return errors.New("domain is invalid")
	}

	local, err := NormalizeLocalAgentID(r.LocalID)
	if err != nil || local == "" {
		return errors.New("localId is invalid")
	}

	wallet := strings.TrimSpace(r.Wallet)
	if !common.IsHexAddress(wallet) {
		return errors.New("wallet is invalid")
	}

	if err := r.Principal.Validate(); err != nil {
		return fmt.Errorf("principal: %w", err)
	}
	if err := r.SelfDescription.Validate(); err != nil {
		return fmt.Errorf("selfDescription: %w", err)
	}
	if len(r.Capabilities) == 0 {
		return errors.New("capabilities must be a non-empty array")
	}
	for i := range r.Capabilities {
		if err := r.Capabilities[i].Validate(); err != nil {
			return fmt.Errorf("capabilities[%d]: %w", i, err)
		}
	}
	if len(r.Boundaries) == 0 {
		return errors.New("boundaries must be a non-empty array")
	}
	for i := range r.Boundaries {
		if err := r.Boundaries[i].Validate(); err != nil {
			return fmt.Errorf("boundaries[%d]: %w", i, err)
		}
	}
	if r.Transparency == nil {
		return errors.New("transparency is required")
	}
	if err := r.Endpoints.Validate(); err != nil {
		return fmt.Errorf("endpoints: %w", err)
	}
	if err := r.Lifecycle.Validate(); err != nil {
		return fmt.Errorf("lifecycle: %w", err)
	}
	if r.PreviousVersionURI != nil && strings.TrimSpace(*r.PreviousVersionURI) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(*r.PreviousVersionURI)); err != nil {
			return errors.New("previousVersionUri is invalid")
		}
	}
	if err := r.Attestations.Validate(); err != nil {
		return fmt.Errorf("attestations: %w", err)
	}
	if err := validateRFC3339(r.Created); err != nil {
		return fmt.Errorf("created: %w", err)
	}
	if err := validateRFC3339(r.Updated); err != nil {
		return fmt.Errorf("updated: %w", err)
	}

	// Enforce normalized identity fields.
	if !strings.EqualFold(domain, strings.TrimSpace(r.Domain)) {
		// Allow either normalized or original; but require it normalizes to same.
		// (No-op: already validated via NormalizeDomain.)
	}
	if !strings.EqualFold(local, strings.TrimSpace(r.LocalID)) {
		// Same note as domain.
	}

	return nil
}

func (p *PrincipalDeclarationV2) Validate() error {
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
	declarationDigest := crypto.Keccak256([]byte(declaration))
	if err := verifyEIP191SignatureOverDigest(identifier, declarationDigest, sig); err != nil {
		return errors.New("signature is invalid")
	}
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
	if !regexHexSig.MatchString(strings.TrimSpace(b.Signature)) {
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
	if a == nil {
		return errors.New("is required")
	}
	if strings.TrimSpace(a.SelfAttestation) == "" {
		return errors.New("selfAttestation is required")
	}
	if !regexHexSig.MatchString(strings.TrimSpace(a.SelfAttestation)) {
		return errors.New("selfAttestation must be hex (0x...)")
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
