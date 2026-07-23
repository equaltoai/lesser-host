package hostedgenesis

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/soul"
)

const (
	// EnvDeclarationSchemaVersion selects the hosted genesis declaration
	// contract. Hosted-genesis has exactly one contract: the deployment must
	// set this to "v2" or DeclarationSchemaVersionV2, and a missing/unknown
	// value fails closed instead of selecting a builder.
	EnvDeclarationSchemaVersion = "HOSTED_GENESIS_DECLARATION_SCHEMA_VERSION"
	// EnvGuidanceVersion optionally pins the interview guidance version. Empty
	// follows the selected schema version.
	EnvGuidanceVersion = "HOSTED_GENESIS_GUIDANCE_VERSION"

	DeclarationSchemaVersionV2 = "soul-five-body-schema.v2"
	GuidanceVersionV2          = "soul-five-body-guidance.v2"

	AdversarialReviewVersionV1 = "hosted-genesis-adversarial-review.v1"
	AdversarialReviewerV1      = "hosted-genesis-deterministic-review.v1"
)

const (
	fiveBodySummaryMaxRunes       = 2400
	fiveBodyEvidenceMaxItems      = 8
	fiveBodyRefusalsMinItems      = 3
	fiveBodyRefusalsMaxItems      = 8
	fiveBodyRefusalFieldMaxRunes  = 480
	fiveBodyReviewReportMaxRunes  = 1200
	fiveBodyReviewFindingsMaxItem = 12
)

// DeclarationContract names the selected hosted genesis schema/guidance
// contract. Hosted-genesis declaration production is five-body-only; the
// contract is stored in produced declaration evidence.
type DeclarationContract struct {
	SchemaVersion   string
	GuidanceVersion string
}

// FiveBodyDeclarationContract is the v2 five-body declaration contract — the
// only declaration contract hosted-genesis production can select.
func FiveBodyDeclarationContract() DeclarationContract {
	return DeclarationContract{SchemaVersion: DeclarationSchemaVersionV2, GuidanceVersion: GuidanceVersionV2}
}

// ErrDeclarationContractUnconfigured fails hosted-genesis declaration
// production closed when the contract selection does not explicitly name the
// five-body contract. It is an operator configuration error, not a declaration
// validation code: callers must surface it as operator_action_required and must
// never substitute a different declaration builder.
var ErrDeclarationContractUnconfigured = errors.New("hosted genesis declaration contract is not configured for five-body")

// fiveBodyContract maps a schema/guidance version pair to the five-body
// contract. At least one value must affirmatively select v2 and neither may
// name anything else; every other combination fails closed.
func fiveBodyContract(schemaVersion string, guidanceVersion string) (DeclarationContract, error) {
	schema := strings.ToLower(strings.TrimSpace(schemaVersion))
	guidance := strings.ToLower(strings.TrimSpace(guidanceVersion))
	schemaSelects := schema == "v2" || schema == strings.ToLower(DeclarationSchemaVersionV2)
	guidanceSelects := guidance == "v2" || guidance == strings.ToLower(GuidanceVersionV2)
	schemaValid := schema == "" || schemaSelects
	guidanceValid := guidance == "" || guidanceSelects
	if (schemaSelects || guidanceSelects) && schemaValid && guidanceValid {
		return FiveBodyDeclarationContract(), nil
	}
	return DeclarationContract{}, ErrDeclarationContractUnconfigured
}

// RequireFiveBodyDeclarationContractFromEnv returns the five-body declaration
// contract selected by the process environment. Hosted-genesis production has
// no other lane: a missing, conflicting, or unrecognized value returns
// ErrDeclarationContractUnconfigured instead of a contract.
func RequireFiveBodyDeclarationContractFromEnv() (DeclarationContract, error) {
	return fiveBodyContract(os.Getenv(EnvDeclarationSchemaVersion), os.Getenv(EnvGuidanceVersion))
}

// ParseFiveBodyDeclarationContract parses a caller-provided schema/guidance
// version pair with the same fail-closed rule as the env selector: missing or
// unknown versions return ErrDeclarationContractUnconfigured, never a
// substitute contract.
func ParseFiveBodyDeclarationContract(schemaVersion string, guidanceVersion string) (DeclarationContract, error) {
	return fiveBodyContract(schemaVersion, guidanceVersion)
}

// IsFiveBody reports whether the contract names the v2 five-body lane.
func (c DeclarationContract) IsFiveBody() bool {
	return strings.EqualFold(strings.TrimSpace(c.SchemaVersion), DeclarationSchemaVersionV2) || strings.EqualFold(strings.TrimSpace(c.GuidanceVersion), GuidanceVersionV2)
}

// ValidateDeclarationContractVersions ensures a typed candidate carries the
// exact Host-selected five-body schema/guidance versions before
// declaration_ready. A non-five-body contract fails closed as unconfigured.
func ValidateDeclarationContractVersions(schemaVersion string, guidanceVersion string, contract DeclarationContract) error {
	if !contract.IsFiveBody() {
		return ErrDeclarationContractUnconfigured
	}
	canonical := FiveBodyDeclarationContract()
	if !strings.EqualFold(strings.TrimSpace(schemaVersion), canonical.SchemaVersion) ||
		!strings.EqualFold(strings.TrimSpace(guidanceVersion), canonical.GuidanceVersion) {
		return NewDeclarationValidationError(DeclarationCodeInvalid)
	}
	return nil
}

// FiveBodyDeclaration captures the five first-class bodies constructed by the v2
// hosted genesis contract. Capabilities and transparency remain satellites.
type FiveBodyDeclaration struct {
	Identity   FiveBodySection  `json:"identity"`
	Philosophy FiveBodySection  `json:"philosophy"`
	Discipline FiveBodySection  `json:"discipline"`
	Boundaries FiveBodySection  `json:"boundaries"`
	Soul       FiveBodySoulBody `json:"soul"`
}

// FiveBodySection is a capped body summary with optional supporting notes. The
// notes are still self-declared transcript evidence, not Host proof.
type FiveBodySection struct {
	Summary string   `json:"summary"`
	Notes   []string `json:"notes,omitempty"`
}

// FiveBodySoulBody contains the soul-body summary and concrete refusals.
type FiveBodySoulBody struct {
	Summary  string                `json:"summary"`
	Notes    []string              `json:"notes,omitempty"`
	Refusals []FiveBodyRefusalRule `json:"refusals"`
}

// FiveBodyRefusalRule is the concrete refusal-floor unit. Each refusal names the
// bypass attempt, the invariant that blocks it, and the closest safe path.
type FiveBodyRefusalRule struct {
	Bypass          string `json:"bypass"`
	Invariant       string `json:"invariant"`
	ClosestSafePath string `json:"closestSafePath"`
}

// AdversarialReview records the independent fail-closed review shape. In unit
// tests this is deterministic; live model-based canary review remains an
// operator-owned deploy proof, not a unit-test dependency.
type AdversarialReview struct {
	Version  string                     `json:"version"`
	Reviewer string                     `json:"reviewer"`
	Result   string                     `json:"result"`
	Findings []AdversarialReviewFinding `json:"findings"`
	Report   string                     `json:"report"`
}

// AdversarialReviewFinding preserves the find -> refute -> report shape.
type AdversarialReviewFinding struct {
	Finding    string `json:"finding"`
	Refutation string `json:"refutation"`
	Report     string `json:"report"`
}

// NormalizeFiveBodyDeclaration trims and caps v2 body evidence before
// deterministic validation or persistence.
func NormalizeFiveBodyDeclaration(in FiveBodyDeclaration) FiveBodyDeclaration {
	in.Identity = normalizeFiveBodySection(in.Identity)
	in.Philosophy = normalizeFiveBodySection(in.Philosophy)
	in.Discipline = normalizeFiveBodySection(in.Discipline)
	in.Boundaries = normalizeFiveBodySection(in.Boundaries)
	in.Soul.Summary = trimRunes(strings.TrimSpace(in.Soul.Summary), fiveBodySummaryMaxRunes)
	in.Soul.Notes = normalizeFiveBodyNotes(in.Soul.Notes)
	refusals := make([]FiveBodyRefusalRule, 0, len(in.Soul.Refusals))
	for _, refusal := range in.Soul.Refusals {
		refusal.Bypass = trimRunes(strings.TrimSpace(refusal.Bypass), fiveBodyRefusalFieldMaxRunes)
		refusal.Invariant = trimRunes(strings.TrimSpace(refusal.Invariant), fiveBodyRefusalFieldMaxRunes)
		refusal.ClosestSafePath = trimRunes(strings.TrimSpace(refusal.ClosestSafePath), fiveBodyRefusalFieldMaxRunes)
		if refusal.Bypass == "" && refusal.Invariant == "" && refusal.ClosestSafePath == "" {
			continue
		}
		refusals = append(refusals, refusal)
		if len(refusals) >= fiveBodyRefusalsMaxItems {
			break
		}
	}
	in.Soul.Refusals = refusals
	return in
}

func normalizeFiveBodySection(in FiveBodySection) FiveBodySection {
	return FiveBodySection{Summary: trimRunes(strings.TrimSpace(in.Summary), fiveBodySummaryMaxRunes), Notes: normalizeFiveBodyNotes(in.Notes)}
}

func normalizeFiveBodyNotes(notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, note := range notes {
		note = trimRunes(strings.TrimSpace(note), fiveBodyRefusalFieldMaxRunes)
		if note == "" {
			continue
		}
		out = append(out, note)
		if len(out) >= fiveBodyEvidenceMaxItems {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateFiveBodyDeclaration enforces deterministic v2 presence gates and the
// refusal floor before any declaration_ready transition.
func ValidateFiveBodyDeclaration(in FiveBodyDeclaration) error {
	in = NormalizeFiveBodyDeclaration(in)
	for _, section := range declarationSectionOrder {
		if err := validateDeclarationSection(section, in); err != nil {
			return err
		}
	}
	return nil
}

// ValidateFiveBodyRefusals enforces minItems >= 3 and rejects generic refusal
// placeholders that do not name a bypass, invariant, and closest safe path.
func ValidateFiveBodyRefusals(refusals []FiveBodyRefusalRule) error {
	refusals = NormalizeFiveBodyDeclaration(FiveBodyDeclaration{Soul: FiveBodySoulBody{Refusals: refusals}}).Soul.Refusals
	if len(refusals) < fiveBodyRefusalsMinItems {
		return NewDeclarationValidationError(DeclarationCodeSoulRefusals)
	}
	for _, refusal := range refusals {
		if !isConcreteRefusalField(refusal.Bypass) || !isConcreteRefusalField(refusal.Invariant) || !isConcreteRefusalField(refusal.ClosestSafePath) {
			return NewDeclarationValidationError(DeclarationCodeSoulRefusalsBad)
		}
	}
	return nil
}

func isConcreteRefusalField(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || len([]rune(value)) < 8 {
		return false
	}
	generic := map[string]struct{}{
		"n/a": {}, "na": {}, "none": {}, "unknown": {}, "unsafe": {}, "harmful": {},
		"bad things": {}, "anything unsafe": {}, "policy": {}, "policy violation": {},
		"policy violations": {}, "follow policy": {}, "be safe": {}, "safe path": {},
		"refuse": {}, "refuse unsafe requests": {}, "i refuse unsafe requests": {},
	}
	if _, ok := generic[value]; ok {
		return false
	}
	words := strings.Fields(value)
	if len(words) < 2 {
		return false
	}
	genericTokens := 0
	for _, word := range words {
		trimmed := strings.Trim(word, ".,;:!?()[]{}\"'")
		switch trimmed {
		case "unsafe", "bad", "harmful", "policy", "rules", "rule", "violation", "violations", "things", "requests", "safe", "safety":
			genericTokens++
		}
	}
	return genericTokens < len(words)
}

// FiveBodyBoundaries maps the v2 soul.refusals floor into the Soul boundary
// rows consumed by the current registration publication pipeline.
func FiveBodyBoundaries(refusals []FiveBodyRefusalRule, now time.Time) []soul.BoundaryV2 {
	refusals = NormalizeFiveBodyDeclaration(FiveBodyDeclaration{Soul: FiveBodySoulBody{Refusals: refusals}}).Soul.Refusals
	out := make([]soul.BoundaryV2, 0, len(refusals))
	for i, refusal := range refusals {
		statement := fmt.Sprintf("I will not allow bypass %q because it violates %q; closest safe path: %s.", refusal.Bypass, refusal.Invariant, refusal.ClosestSafePath)
		out = append(out, soul.BoundaryV2{
			ID:             fmt.Sprintf("mint-%d-refusal-%02d", now.Unix(), i+1),
			Category:       "refusal",
			Statement:      statement,
			Rationale:      fmt.Sprintf("Five-body soul refusal: invariant=%s", refusal.Invariant),
			AddedAt:        now.UTC().Format(time.RFC3339),
			AddedInVersion: "1",
			Signature:      "0x00",
		})
	}
	return out
}

// BuildAdversarialReviewV2 performs the deterministic CI-safe review pass used
// before declaration_ready in the v2 lane. It uses independent code (not the
// extracting model) to find candidate issues, refute them, and report the pass.
func BuildAdversarialReviewV2(in FiveBodyDeclaration) (AdversarialReview, error) {
	in = NormalizeFiveBodyDeclaration(in)
	findings := []AdversarialReviewFinding{
		{
			Finding:    "five_body_presence",
			Refutation: "identity, philosophy, discipline, boundaries, and soul bodies are present and non-empty",
			Report:     "No missing first-class body remains after deterministic presence validation.",
		},
		{
			Finding:    "refusal_floor",
			Refutation: "soul.refusals contains at least three concrete bypass/invariant/closestSafePath rules",
			Report:     "No generic refusal placeholder remains after deterministic refusal validation.",
		},
		{
			Finding:    "cadence_inscription",
			Refutation: "the cadence is carried as a named identity/soul/discipline inscription, not as repeated instructions",
			Report:     "No extra cadence teaching is required in produced declaration evidence.",
		},
	}
	if err := ValidateFiveBodyDeclaration(in); err != nil {
		return AdversarialReview{}, err
	}
	review := AdversarialReview{
		Version:  AdversarialReviewVersionV1,
		Reviewer: AdversarialReviewerV1,
		Result:   "pass",
		Findings: findings,
		Report:   "Deterministic adversarial review passed: find -> refute -> report found no unresolved v2 declaration gate issue. Live model adversarial canary remains operator-owned deploy proof.",
	}
	if err := ValidateAdversarialReview(review); err != nil {
		return AdversarialReview{}, err
	}
	return review, nil
}

// ValidateAdversarialReview ensures the independent review evidence has the
// required find -> refute -> report shape and no unresolved result.
func ValidateAdversarialReview(review AdversarialReview) error {
	if strings.TrimSpace(review.Version) != AdversarialReviewVersionV1 || strings.TrimSpace(review.Reviewer) != AdversarialReviewerV1 || strings.ToLower(strings.TrimSpace(review.Result)) != "pass" {
		return NewDeclarationValidationError(DeclarationCodeAdversarialReview)
	}
	if len(review.Findings) == 0 || len(review.Findings) > fiveBodyReviewFindingsMaxItem || strings.TrimSpace(review.Report) == "" || len([]rune(review.Report)) > fiveBodyReviewReportMaxRunes {
		return NewDeclarationValidationError(DeclarationCodeAdversarialReview)
	}
	for _, finding := range review.Findings {
		if strings.TrimSpace(finding.Finding) == "" || strings.TrimSpace(finding.Refutation) == "" || strings.TrimSpace(finding.Report) == "" {
			return NewDeclarationValidationError(DeclarationCodeAdversarialReview)
		}
	}
	return nil
}

func trimRunes(raw string, max int) string {
	if max <= 0 {
		return raw
	}
	runes := []rune(raw)
	if len(runes) <= max {
		return raw
	}
	return string(runes[:max])
}
