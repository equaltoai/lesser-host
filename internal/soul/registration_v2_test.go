package soul

import (
	"crypto/ecdsa"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

func mustRegistrationTestKey(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()

	key, err := crypto.HexToECDSA("4f3edf983ac636a65a842ce7c78d9aa706d3b113bce036f3a9cf4d8b7f9e0d72")
	if err != nil {
		t.Fatalf("HexToECDSA: %v", err)
	}
	return key, crypto.PubkeyToAddress(key.PublicKey)
}

func mustPrincipalSignature(t *testing.T, key *ecdsa.PrivateKey, declaration string) string {
	t.Helper()

	digest := crypto.Keccak256([]byte(declaration))
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return hexutil.Encode(sig)
}

func validPrincipalV2(t *testing.T) PrincipalDeclarationV2 {
	t.Helper()

	key, address := mustRegistrationTestKey(t)
	declaration := "I declare that this agent operates under my authority."
	return PrincipalDeclarationV2{
		Type:        "individual",
		Identifier:  address.Hex(),
		DisplayName: "Alice Example",
		ContactURI:  "https://example.com/contact",
		Declaration: declaration,
		Signature:   mustPrincipalSignature(t, key, declaration),
		DeclaredAt:  "2026-03-05T12:00:00Z",
	}
}

func validRegistrationV2(t *testing.T) RegistrationFileV2 {
	t.Helper()

	agentID, err := DeriveAgentIDHex("example.com", "agent-bot")
	if err != nil {
		t.Fatalf("DeriveAgentIDHex: %v", err)
	}

	return RegistrationFileV2{
		Version:   "2",
		AgentID:   agentID,
		Domain:    "example.com",
		LocalID:   "agent-bot",
		Wallet:    "0x000000000000000000000000000000000000dEaD",
		Principal: validPrincipalV2(t),
		SelfDescription: SelfDescriptionV2{
			Purpose:      "Help users plan travel with explicit limitations.",
			Constraints:  "No booking execution.",
			Commitments:  "Disclose uncertainty.",
			Limitations:  "No legal or medical advice.",
			AuthoredBy:   "agent",
			MintingModel: "openai:gpt-4o-mini",
		},
		Capabilities: []CapabilityV2{
			{
				Capability:    "travel_planning",
				Scope:         "Draft itineraries and compare routes.",
				ClaimLevel:    "self-declared",
				LastValidated: "2026-03-05T12:00:00Z",
				ValidationRef: "ref-1",
				DegradesTo:    "email",
			},
		},
		Boundaries: []BoundaryV2{
			{
				ID:             "boundary-1",
				Category:       "refusal",
				Statement:      "I will not impersonate a human.",
				Rationale:      "Prevent deception.",
				AddedAt:        "2026-03-05T12:00:00Z",
				AddedInVersion: "1",
				Signature:      "0x00",
			},
		},
		Transparency: map[string]any{"provider": "openai"},
		Endpoints: EndpointsV2{
			ActivityPub: "https://example.com/ap/@agent-bot",
			MCP:         "https://example.com/mcp",
			Soul:        "https://example.com/.well-known/agent-bot.json",
		},
		Lifecycle: LifecycleV2{
			Status:          "active",
			StatusChangedAt: "2026-03-05T12:00:00Z",
		},
		PreviousVersionURI: nil,
		ChangeSummary:      ptr("Initial publication"),
		Attestations: AttestationsV2{
			HostAttestation: "https://example.com/attestations/1",
			SelfAttestation: "0x00",
		},
		Created: "2026-03-05T12:00:00Z",
		Updated: "2026-03-05T12:00:00Z",
	}
}

func TestRegistrationFileV2_ParseAndValidate(t *testing.T) {
	t.Parallel()

	if _, err := ParseRegistrationFileV2(nil); err == nil {
		t.Fatalf("expected parse error for empty body")
	}
	if _, err := ParseRegistrationFileV2([]byte("{")); err == nil {
		t.Fatalf("expected parse error for invalid json")
	}

	valid := validRegistrationV2(t)
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	parsed, err := ParseRegistrationFileV2(body)
	if err != nil {
		t.Fatalf("ParseRegistrationFileV2: %v", err)
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	var nilReg *RegistrationFileV2
	if err := nilReg.Validate(); err == nil {
		t.Fatalf("expected nil receiver error")
	}

	cases := []struct {
		name string
		mut  func(*RegistrationFileV2)
	}{
		{name: "bad version", mut: func(r *RegistrationFileV2) { r.Version = "3" }},
		{name: "bad agent id", mut: func(r *RegistrationFileV2) { r.AgentID = "0x1234" }},
		{name: "bad domain", mut: func(r *RegistrationFileV2) { r.Domain = "bad domain" }},
		{name: "bad local id", mut: func(r *RegistrationFileV2) { r.LocalID = "@" }},
		{name: "bad wallet", mut: func(r *RegistrationFileV2) { r.Wallet = "not-an-address" }},
		{name: "bad principal", mut: func(r *RegistrationFileV2) { r.Principal.Type = "robot" }},
		{name: "bad self description", mut: func(r *RegistrationFileV2) { r.SelfDescription.Purpose = "short" }},
		{name: "empty capabilities", mut: func(r *RegistrationFileV2) { r.Capabilities = nil }},
		{name: "bad capability", mut: func(r *RegistrationFileV2) { r.Capabilities[0].ClaimLevel = "wrong" }},
		{name: "empty boundaries", mut: func(r *RegistrationFileV2) { r.Boundaries = nil }},
		{name: "bad boundary", mut: func(r *RegistrationFileV2) { r.Boundaries[0].Signature = "" }},
		{name: "nil transparency", mut: func(r *RegistrationFileV2) { r.Transparency = nil }},
		{name: "bad endpoint", mut: func(r *RegistrationFileV2) { r.Endpoints.MCP = "://" }},
		{name: "bad lifecycle", mut: func(r *RegistrationFileV2) { r.Lifecycle.Status = "paused" }},
		{name: "bad previous version", mut: func(r *RegistrationFileV2) { r.PreviousVersionURI = ptr("://bad") }},
		{name: "bad attestations", mut: func(r *RegistrationFileV2) { r.Attestations.SelfAttestation = "" }},
		{name: "bad created", mut: func(r *RegistrationFileV2) { r.Created = "not-time" }},
		{name: "bad updated", mut: func(r *RegistrationFileV2) { r.Updated = "not-time" }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := validRegistrationV2(t)
			tc.mut(&reg)
			if err := reg.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestRegistrationV2PrincipalLeafValidator(t *testing.T) {
	t.Parallel()

	valid := validPrincipalV2(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected principal error: %v", err)
	}

	cases := []PrincipalDeclarationV2{
		{},
		{Type: "robot", Identifier: valid.Identifier, Declaration: valid.Declaration, Signature: valid.Signature, DeclaredAt: valid.DeclaredAt},
		{Type: "individual", Identifier: "bad", Declaration: valid.Declaration, Signature: valid.Signature, DeclaredAt: valid.DeclaredAt},
		{Type: "individual", Identifier: valid.Identifier, Declaration: "short", Signature: valid.Signature, DeclaredAt: valid.DeclaredAt},
		{Type: "individual", Identifier: valid.Identifier, Declaration: valid.Declaration, Signature: "bad", DeclaredAt: valid.DeclaredAt},
		{Type: "individual", Identifier: valid.Identifier, Declaration: valid.Declaration, Signature: valid.Signature, ContactURI: "://bad", DeclaredAt: valid.DeclaredAt},
		{Type: "individual", Identifier: valid.Identifier, Declaration: valid.Declaration, Signature: valid.Signature, DeclaredAt: "bad"},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected principal validation error for %#v", tc)
		}
	}
}

func TestRegistrationV2SignatureHelper(t *testing.T) {
	t.Parallel()

	key, address := mustRegistrationTestKey(t)
	other := common.HexToAddress("0x0000000000000000000000000000000000000001")
	digest := crypto.Keccak256([]byte("hello"))
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := verifyEIP191SignatureOverDigest("bad", digest, hexutil.Encode(sig)); err == nil {
		t.Fatalf("expected invalid address error")
	}
	if err := verifyEIP191SignatureOverDigest(address.Hex(), digest, "0x01"); err == nil {
		t.Fatalf("expected invalid signature length error")
	}
	if err := verifyEIP191SignatureOverDigest(other.Hex(), digest, hexutil.Encode(sig)); err == nil {
		t.Fatalf("expected signature mismatch")
	}
	if err := verifyEIP191SignatureOverDigest(address.Hex(), digest, hexutil.Encode(sig)); err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}

	legacy := append([]byte(nil), sig...)
	legacy[64] += 27
	if err := verifyEIP191SignatureOverDigest(address.Hex(), digest, hexutil.Encode(legacy)); err != nil {
		t.Fatalf("unexpected legacy verify error: %v", err)
	}
}

func TestRegistrationV2SelfDescriptionLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *SelfDescriptionV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	if err := (&SelfDescriptionV2{Purpose: "short", AuthoredBy: "agent"}).Validate(); err == nil {
		t.Fatalf("expected short purpose error")
	}
	if err := (&SelfDescriptionV2{Purpose: strings.Repeat("x", 20), AuthoredBy: "other"}).Validate(); err == nil {
		t.Fatalf("expected bad authoredBy error")
	}
}

func TestRegistrationV2CapabilityLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *CapabilityV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []CapabilityV2{
		{Scope: "scope", ClaimLevel: "self-declared"},
		{Capability: "cap", ClaimLevel: "self-declared"},
		{Capability: "cap", Scope: "scope", ClaimLevel: "bad"},
		{Capability: "cap", Scope: "scope", ClaimLevel: "self-declared", LastValidated: "bad"},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected capability error for %#v", tc)
		}
	}
}

func TestRegistrationV2BoundaryLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *BoundaryV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []BoundaryV2{
		{Category: "refusal", Statement: "x", AddedAt: "2026-03-05T12:00:00Z", AddedInVersion: "1", Signature: "0x00"},
		{ID: "b1", Category: "other", Statement: "x", AddedAt: "2026-03-05T12:00:00Z", AddedInVersion: "1", Signature: "0x00"},
		{ID: "b1", Category: "refusal", AddedAt: "2026-03-05T12:00:00Z", AddedInVersion: "1", Signature: "0x00"},
		{ID: "b1", Category: "refusal", Statement: "x", AddedAt: "bad", AddedInVersion: "1", Signature: "0x00"},
		{ID: "b1", Category: "refusal", Statement: "x", AddedAt: "2026-03-05T12:00:00Z", Signature: "0x00"},
		{ID: "b1", Category: "refusal", Statement: "x", AddedAt: "2026-03-05T12:00:00Z", AddedInVersion: "1", Signature: "bad"},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected boundary error for %#v", tc)
		}
	}
}

func TestRegistrationV2EndpointsLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *EndpointsV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []EndpointsV2{
		{ActivityPub: "://bad"},
		{MCP: "://bad"},
		{Soul: "://bad"},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected endpoints error for %#v", tc)
		}
	}
}

func TestRegistrationV2LifecycleLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *LifecycleV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []LifecycleV2{
		{Status: "bad", StatusChangedAt: "2026-03-05T12:00:00Z"},
		{Status: "active", StatusChangedAt: "bad"},
		{Status: "active", StatusChangedAt: "2026-03-05T12:00:00Z", SuccessorAgentID: ptr("bad")},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected lifecycle error for %#v", tc)
		}
	}
}

func TestRegistrationV2AttestationsLeafValidator(t *testing.T) {
	t.Parallel()

	var nilValue *AttestationsV2
	if err := nilValue.Validate(); err == nil {
		t.Fatalf("expected nil error")
	}
	cases := []AttestationsV2{
		{},
		{SelfAttestation: "bad"},
		{SelfAttestation: "0x00", HostAttestation: "://bad"},
	}
	for _, tc := range cases {
		tc := tc
		if err := tc.Validate(); err == nil {
			t.Fatalf("expected attestations error for %#v", tc)
		}
	}
}

func TestRegistrationV2RFC3339Validator(t *testing.T) {
	t.Parallel()

	if err := validateRFC3339(""); err == nil {
		t.Fatalf("expected empty error")
	}
	if err := validateRFC3339("bad"); err == nil {
		t.Fatalf("expected invalid timestamp error")
	}
	if err := validateRFC3339("2026-03-05T12:00:00Z"); err != nil {
		t.Fatalf("unexpected rfc3339 error: %v", err)
	}
	if err := validateRFC3339("2026-03-05T12:00:00.123456789Z"); err != nil {
		t.Fatalf("unexpected rfc3339nano error: %v", err)
	}
}

func ptr[T any](v T) *T {
	return &v
}
