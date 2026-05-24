package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	m3CanarySchemaVersion = 1
	defaultStage          = "lab"
	canonicalEmailDomain  = "lessersoul.ai"
	smtpLocalPartLimit    = 64
)

var managedInstanceSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

type canaryConfig struct {
	EvidencePath        string
	OutputPath          string
	Stage               string
	AgentID             string
	RequireLegacyAlias  bool
	RequireBodyMCP      bool
	RequireUnknownAlias bool
}

type canaryEvidence struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at,omitempty"`
	Stage         string                 `json:"stage"`
	TableName     string                 `json:"table_name,omitempty"`
	Mode          string                 `json:"mode,omitempty"`
	Contract      canaryContract         `json:"contract,omitempty"`
	Agents        []canaryAgentEvidence  `json:"agents"`
	UnknownAlias  *unknownAliasCanary    `json:"unknown_alias,omitempty"`
	Raw           map[string]interface{} `json:"-"`
}

type canaryContract struct {
	CanonicalAddressFormat string `json:"canonical_address_format,omitempty"`
	LegacyAliasPolicy      string `json:"legacy_alias_policy,omitempty"`
	RedactionPolicy        string `json:"redaction_policy,omitempty"`
}

type canaryAgentEvidence struct {
	AgentID          string            `json:"agent_id"`
	LocalID          string            `json:"local_id"`
	InstanceSlug     string            `json:"instance_slug"`
	CanonicalAddress string            `json:"canonical_address"`
	LegacyAlias      string            `json:"legacy_alias,omitempty"`
	Primary          primaryCanary     `json:"primary"`
	Legacy           legacyAliasCanary `json:"legacy,omitempty"`
	BodyMCP          *bodyMCPCanary    `json:"body_mcp,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
	Raw              map[string]any    `json:"-"`
}

type primaryCanary struct {
	Inbound        addressCanary        `json:"inbound"`
	Outbound       addressCanary        `json:"outbound"`
	Mailbox        mailboxCanary        `json:"mailbox"`
	Resolve        resolveCanary        `json:"resolve"`
	Contactability contactabilityCanary `json:"contactability"`
}

type legacyAliasCanary struct {
	Inbound          addressCanary          `json:"inbound"`
	CanonicalMailbox addressCanary          `json:"canonical_mailbox"`
	NonAdvertisement nonAdvertisementCanary `json:"non_advertisement"`
	Resolve          resolveCanary          `json:"resolve"`
}

type addressCanary struct {
	Passed           bool   `json:"passed"`
	Address          string `json:"address,omitempty"`
	Recipient        string `json:"recipient,omitempty"`
	Sender           string `json:"sender,omitempty"`
	MailboxToAddress string `json:"mailbox_to_address,omitempty"`
	CanonicalizedTo  string `json:"canonicalized_to,omitempty"`
	Status           string `json:"status,omitempty"`
	MessageRef       string `json:"message_ref,omitempty"`
	DeliveryID       string `json:"delivery_id,omitempty"`
}

type mailboxCanary struct {
	List            bool   `json:"list"`
	Get             bool   `json:"get"`
	Content         bool   `json:"content"`
	Search          bool   `json:"search"`
	ContentRedacted bool   `json:"content_redacted"`
	ToAddress       string `json:"to_address,omitempty"`
	FromAddress     string `json:"from_address,omitempty"`
}

type resolveCanary struct {
	Status  string `json:"status"`
	AgentID string `json:"agent_id,omitempty"`
	Address string `json:"address,omitempty"`
}

type contactabilityCanary struct {
	Passed  bool   `json:"passed"`
	Address string `json:"address,omitempty"`
	Status  string `json:"status,omitempty"`
}

type nonAdvertisementCanary struct {
	Passed                  bool     `json:"passed"`
	PublicEmailAddress      string   `json:"public_email_address,omitempty"`
	PublicEmailAddresses    []string `json:"public_email_addresses,omitempty"`
	LegacyAddressAdvertised bool     `json:"legacy_address_advertised"`
}

type bodyMCPCanary struct {
	IdentityWhoamiEmail string `json:"identity_whoami_email,omitempty"`
	IdentityLookupEmail string `json:"identity_lookup_email,omitempty"`
	IdentityWhoami      bool   `json:"identity_whoami"`
	IdentityLookup      bool   `json:"identity_lookup"`
	EmailSend           bool   `json:"email_send"`
	EmailReply          bool   `json:"email_reply"`
	EmailRead           bool   `json:"email_read"`
	EmailGet            bool   `json:"email_get"`
	EmailGetContent     bool   `json:"email_get_content"`
	EmailSearch         bool   `json:"email_search"`
	LegacyAliasInbound  bool   `json:"legacy_alias_inbound,omitempty"`
}

type unknownAliasCanary struct {
	Address       string `json:"address"`
	InboundStatus string `json:"inbound_status,omitempty"`
	ResolveStatus string `json:"resolve_status,omitempty"`
}

type canaryValidationReport struct {
	SchemaVersion       int      `json:"schema_version"`
	GeneratedAt         string   `json:"generated_at"`
	Mode                string   `json:"mode"`
	Stage               string   `json:"stage"`
	EvidencePath        string   `json:"evidence_path"`
	AgentsChecked       int      `json:"agents_checked"`
	PrimaryChecked      int      `json:"primary_checked"`
	LegacyChecked       int      `json:"legacy_checked"`
	BodyMCPChecked      int      `json:"body_mcp_checked"`
	UnknownAliasChecked bool     `json:"unknown_alias_checked"`
	Issues              []string `json:"issues,omitempty"`
	Caveats             []string `json:"caveats,omitempty"`
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now().UTC()))
}

func runCLI(args []string, getenv func(string) string, stdout io.Writer, stderr io.Writer, now time.Time) int {
	cfg, err := parseConfig(args, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	evidence, err := loadCanaryEvidence(cfg.EvidencePath)
	if err != nil {
		fmt.Fprintf(stderr, "load evidence: %v\n", err)
		return 1
	}
	report := validateCanaryEvidence(evidence, cfg, now)
	if err := writeValidationReport(report, cfg.OutputPath, stdout); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if len(report.Issues) > 0 {
		return 2
	}
	return 0
}

func parseConfig(args []string, getenv func(string) string) (canaryConfig, error) {
	stageDefault := strings.ToLower(strings.TrimSpace(getenv("STAGE")))
	if stageDefault == "" {
		stageDefault = defaultStage
	}
	cfg := canaryConfig{Stage: stageDefault}
	fs := flag.NewFlagSet("soul-email-m3-canary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.EvidencePath, "evidence", "", "Redacted Project 37 M3 canary evidence JSON path")
	fs.StringVar(&cfg.OutputPath, "out", "", "Output validation report JSON path (default stdout)")
	fs.StringVar(&cfg.Stage, "stage", cfg.Stage, "Expected control-plane stage")
	fs.StringVar(&cfg.AgentID, "agent-id", "", "Optional target agent id")
	fs.BoolVar(&cfg.RequireLegacyAlias, "require-legacy-alias", false, "Require every checked agent to include legacy alias canary evidence")
	fs.BoolVar(&cfg.RequireBodyMCP, "require-body-mcp", false, "Require every checked agent to include lesser-body MCP compatibility evidence")
	fs.BoolVar(&cfg.RequireUnknownAlias, "require-unknown-alias", false, "Require top-level unknown bare alias fail-closed evidence")
	if err := fs.Parse(args); err != nil {
		return canaryConfig{}, err
	}
	cfg.EvidencePath = strings.TrimSpace(cfg.EvidencePath)
	cfg.OutputPath = strings.TrimSpace(cfg.OutputPath)
	cfg.Stage = strings.ToLower(strings.TrimSpace(cfg.Stage))
	cfg.AgentID = strings.ToLower(strings.TrimSpace(cfg.AgentID))
	if cfg.EvidencePath == "" {
		return canaryConfig{}, errors.New("--evidence is required")
	}
	if cfg.Stage == "" {
		return canaryConfig{}, errors.New("--stage or STAGE is required")
	}
	return cfg, nil
}

func loadCanaryEvidence(path string) (canaryEvidence, error) {
	data, err := os.ReadFile(path) // #nosec G703 -- operator-provided local evidence path.
	if err != nil {
		return canaryEvidence{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return canaryEvidence{}, err
	}
	var evidence canaryEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return canaryEvidence{}, err
	}
	evidence.Raw = raw
	return evidence, nil
}

func validateCanaryEvidence(e canaryEvidence, cfg canaryConfig, now time.Time) canaryValidationReport {
	report := canaryValidationReport{
		SchemaVersion: m3CanarySchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Mode:          "validate-redacted-canary-evidence",
		Stage:         cfg.Stage,
		EvidencePath:  cfg.EvidencePath,
	}
	issues := issueCollector{}
	caveats := issueCollector{}
	validateEvidenceHeader(e, cfg, &issues)
	validateCanaryAgents(e.Agents, cfg, &issues, &caveats, &report)
	validateRequiredCanaryCoverage(e, cfg, &issues, &report)
	report.Issues = issues.list()
	report.Caveats = caveats.list()
	return report
}

func validateEvidenceHeader(e canaryEvidence, cfg canaryConfig, issues *issueCollector) {
	if e.SchemaVersion != m3CanarySchemaVersion {
		issues.add("schema_version=%d expected=%d", e.SchemaVersion, m3CanarySchemaVersion)
	}
	if strings.ToLower(strings.TrimSpace(e.Stage)) != cfg.Stage {
		issues.add("stage=%q expected=%q", strings.ToLower(strings.TrimSpace(e.Stage)), cfg.Stage)
	}
	if len(e.Agents) == 0 {
		issues.add("agents is required")
	}
	if err := ensureEvidenceRedacted(e.Raw); err != nil {
		issues.add("redaction: %v", err)
	}
}

func validateCanaryAgents(agents []canaryAgentEvidence, cfg canaryConfig, issues *issueCollector, caveats *issueCollector, report *canaryValidationReport) {
	for i, agent := range agents {
		agent.AgentID = strings.ToLower(strings.TrimSpace(agent.AgentID))
		if cfg.AgentID != "" && agent.AgentID != cfg.AgentID {
			continue
		}
		report.AgentsChecked++
		prefix := fmt.Sprintf("agents[%d:%s]", i, valueOr(agent.AgentID, "unknown"))
		validateAgentEvidence(prefix, agent, cfg, issues, caveats, report)
	}
	if cfg.AgentID != "" && report.AgentsChecked == 0 {
		issues.add("target_agent_not_found: %s", cfg.AgentID)
	}
}

func validateRequiredCanaryCoverage(e canaryEvidence, cfg canaryConfig, issues *issueCollector, report *canaryValidationReport) {
	if report.PrimaryChecked == 0 {
		issues.add("no primary canary evidence checked")
	}
	if cfg.RequireLegacyAlias && report.LegacyChecked == 0 {
		issues.add("no legacy alias canary evidence checked")
	}
	if cfg.RequireBodyMCP && report.BodyMCPChecked == 0 {
		issues.add("no body MCP canary evidence checked")
	}
	if cfg.RequireUnknownAlias || e.UnknownAlias != nil {
		validateUnknownAlias(e.UnknownAlias, issues)
		report.UnknownAliasChecked = e.UnknownAlias != nil
	}
}

func validateAgentEvidence(prefix string, agent canaryAgentEvidence, cfg canaryConfig, issues *issueCollector, caveats *issueCollector, report *canaryValidationReport) {
	canonical := normalizeEmail(agent.CanonicalAddress)
	legacy := normalizeEmail(agent.LegacyAlias)
	localID := strings.ToLower(strings.TrimSpace(agent.LocalID))
	instanceSlug := strings.ToLower(strings.TrimSpace(agent.InstanceSlug))

	if agent.AgentID == "" {
		issues.add("%s.agent_id is required", prefix)
	}
	if localID == "" {
		issues.add("%s.local_id is required", prefix)
	}
	if !managedInstanceSlugRE.MatchString(instanceSlug) {
		issues.add("%s.instance_slug is invalid", prefix)
	}
	expectedCanonical, ok := expectedCanonicalAddress(localID, instanceSlug)
	if !ok {
		issues.add("%s.canonical_address cannot be derived", prefix)
	} else if canonical != expectedCanonical {
		issues.add("%s.canonical_address=%q expected=%q", prefix, canonical, expectedCanonical)
	}
	if localPartLength(canonical) > smtpLocalPartLimit {
		issues.add("%s.canonical_address local-part exceeds %d octets", prefix, smtpLocalPartLimit)
	}

	validatePrimaryCanary(prefix+".primary", agent, canonical, issues, report)

	if legacy != "" || cfg.RequireLegacyAlias {
		validateLegacyCanary(prefix+".legacy", agent, canonical, legacy, issues, report)
	}
	if agent.BodyMCP != nil || cfg.RequireBodyMCP {
		validateBodyMCPCanary(prefix+".body_mcp", agent.BodyMCP, canonical, legacy, issues, caveats, report)
	}
}

func validatePrimaryCanary(prefix string, agent canaryAgentEvidence, canonical string, issues *issueCollector, report *canaryValidationReport) {
	report.PrimaryChecked++
	validatePassedAddress(prefix+".inbound", agent.Primary.Inbound, canonical, addressExpectation{recipient: canonical, mailboxTo: canonical}, issues)
	validatePassedAddress(prefix+".outbound", agent.Primary.Outbound, canonical, addressExpectation{sender: canonical}, issues)
	validatePrimaryMailbox(prefix+".mailbox", agent.Primary.Mailbox, canonical, issues)
	validateSuccessfulResolve(prefix+".resolve", agent.Primary.Resolve, canonical, agent.AgentID, issues)
	if !agent.Primary.Contactability.Passed {
		issues.add("%s.contactability.passed must be true", prefix)
	}
	if addr := normalizeEmail(agent.Primary.Contactability.Address); addr != "" && addr != canonical {
		issues.add("%s.contactability.address=%q expected=%q", prefix, addr, canonical)
	}
}

type addressExpectation struct {
	recipient string
	sender    string
	mailboxTo string
}

func validatePassedAddress(prefix string, actual addressCanary, canonical string, expected addressExpectation, issues *issueCollector) {
	if !actual.Passed {
		issues.add("%s.passed must be true", prefix)
	}
	if expected.recipient != "" {
		if recipient := normalizeEmail(firstNonEmpty(actual.Recipient, actual.Address)); recipient != expected.recipient {
			issues.add("%s.recipient/address=%q expected=%q", prefix, recipient, expected.recipient)
		}
	}
	if expected.sender != "" {
		if sender := normalizeEmail(firstNonEmpty(actual.Sender, actual.Address)); sender != expected.sender {
			issues.add("%s.sender/address=%q expected=%q", prefix, sender, expected.sender)
		}
	}
	if expected.mailboxTo != "" {
		if mailboxTo := normalizeEmail(actual.MailboxToAddress); mailboxTo != expected.mailboxTo {
			issues.add("%s.mailbox_to_address=%q expected=%q", prefix, mailboxTo, expected.mailboxTo)
		}
	}
	if actual.CanonicalizedTo != "" && normalizeEmail(actual.CanonicalizedTo) != canonical {
		issues.add("%s.canonicalized_to=%q expected=%q", prefix, normalizeEmail(actual.CanonicalizedTo), canonical)
	}
	if strings.EqualFold(strings.TrimSpace(actual.Status), "failed") || strings.EqualFold(strings.TrimSpace(actual.Status), "bounced") {
		issues.add("%s.status must not be %q", prefix, actual.Status)
	}
}

func validatePrimaryMailbox(prefix string, mailbox mailboxCanary, canonical string, issues *issueCollector) {
	checks := []struct {
		name string
		ok   bool
	}{
		{name: "list", ok: mailbox.List},
		{name: "get", ok: mailbox.Get},
		{name: "content", ok: mailbox.Content},
		{name: "search", ok: mailbox.Search},
		{name: "content_redacted", ok: mailbox.ContentRedacted},
	}
	for _, check := range checks {
		if !check.ok {
			issues.add("%s.%s must be true", prefix, check.name)
		}
	}
	if addr := normalizeEmail(mailbox.ToAddress); addr != "" && addr != canonical {
		issues.add("%s.to_address=%q expected=%q", prefix, addr, canonical)
	}
}

func validateSuccessfulResolve(prefix string, resolve resolveCanary, canonical string, agentID string, issues *issueCollector) {
	if !strings.EqualFold(strings.TrimSpace(resolve.Status), "ok") {
		issues.add("%s.status=%q expected=ok", prefix, resolve.Status)
	}
	if addr := normalizeEmail(resolve.Address); addr != "" && addr != canonical {
		issues.add("%s.address=%q expected=%q", prefix, addr, canonical)
	}
	if got := strings.ToLower(strings.TrimSpace(resolve.AgentID)); got != "" && agentID != "" && got != strings.ToLower(strings.TrimSpace(agentID)) {
		issues.add("%s.agent_id=%q expected=%q", prefix, got, strings.ToLower(strings.TrimSpace(agentID)))
	}
}

func validateLegacyCanary(prefix string, agent canaryAgentEvidence, canonical string, legacy string, issues *issueCollector, report *canaryValidationReport) {
	report.LegacyChecked++
	if legacy == "" {
		issues.add("%s legacy_alias is required", prefix)
		return
	}
	if legacy == canonical {
		issues.add("%s legacy_alias must differ from canonical address", prefix)
	}
	if expectedLegacy, ok := expectedLegacyAddress(agent.LocalID); ok && legacy != expectedLegacy {
		issues.add("%s legacy_alias=%q expected=%q", prefix, legacy, expectedLegacy)
	}
	validatePassedAddress(prefix+".inbound", agent.Legacy.Inbound, canonical, addressExpectation{recipient: legacy, mailboxTo: canonical}, issues)
	validatePassedAddress(prefix+".canonical_mailbox", agent.Legacy.CanonicalMailbox, canonical, addressExpectation{mailboxTo: canonical}, issues)
	validateNonAdvertisement(prefix+".non_advertisement", agent.Legacy.NonAdvertisement, canonical, legacy, issues)
	validateLegacyResolve(prefix+".resolve", agent.Legacy.Resolve, legacy, issues)
}

func validateNonAdvertisement(prefix string, nonAdv nonAdvertisementCanary, canonical string, legacy string, issues *issueCollector) {
	if !nonAdv.Passed {
		issues.add("%s.passed must be true", prefix)
	}
	if nonAdv.LegacyAddressAdvertised {
		issues.add("%s.legacy_address_advertised must be false", prefix)
	}
	if addr := normalizeEmail(nonAdv.PublicEmailAddress); addr != "" && addr != canonical {
		issues.add("%s.public_email_address=%q expected=%q", prefix, addr, canonical)
	}
	for _, addr := range nonAdv.PublicEmailAddresses {
		normalized := normalizeEmail(addr)
		if normalized == legacy {
			issues.add("%s.public_email_addresses includes legacy alias %q", prefix, legacy)
		}
		if normalized != "" && normalized != canonical {
			issues.add("%s.public_email_addresses includes unexpected address %q", prefix, normalized)
		}
	}
}

func validateLegacyResolve(prefix string, resolve resolveCanary, legacy string, issues *issueCollector) {
	status := strings.ToLower(strings.TrimSpace(resolve.Status))
	switch status {
	case "not_found", "fail_closed", "unauthorized", "forbidden":
		// Legacy aliases are host-internal inbound aliases, not public/current resolve targets.
	case "":
		issues.add("%s.status is required and must show fail-closed behavior for %q", prefix, legacy)
	default:
		issues.add("%s.status=%q must not resolve legacy alias %q as public/current", prefix, status, legacy)
	}
	if addr := normalizeEmail(resolve.Address); addr == legacy {
		issues.add("%s.address must not advertise legacy alias %q", prefix, legacy)
	}
}

func validateBodyMCPCanary(prefix string, body *bodyMCPCanary, canonical string, legacy string, issues *issueCollector, caveats *issueCollector, report *canaryValidationReport) {
	if body == nil {
		issues.add("%s is required", prefix)
		return
	}
	report.BodyMCPChecked++
	checks := []struct {
		name string
		ok   bool
	}{
		{name: "identity_whoami", ok: body.IdentityWhoami},
		{name: "identity_lookup", ok: body.IdentityLookup},
		{name: "email_send", ok: body.EmailSend},
		{name: "email_reply", ok: body.EmailReply},
		{name: "email_read", ok: body.EmailRead},
		{name: "email_get", ok: body.EmailGet},
		{name: "email_get_content", ok: body.EmailGetContent},
		{name: "email_search", ok: body.EmailSearch},
	}
	for _, check := range checks {
		if !check.ok {
			issues.add("%s.%s must be true", prefix, check.name)
		}
	}
	// identity_lookup_email is the authoritative lookup — must match canonical.
	validateBodyMCPIdentityEmail(prefix, "identity_lookup_email", body.IdentityLookupEmail, canonical, legacy, false, issues, caveats)
	// identity_whoami_email may reflect signed/on-chain registration state that
	// lags behind the current channel until the agent republishes; legacy values
	// are recorded as a caveat rather than a hard failure.
	validateBodyMCPIdentityEmail(prefix, "identity_whoami_email", body.IdentityWhoamiEmail, canonical, legacy, true, issues, caveats)
}

func validateBodyMCPIdentityEmail(prefix string, name string, value string, canonical string, legacy string, allowLegacy bool, issues *issueCollector, caveats *issueCollector) {
	addr := normalizeEmail(value)
	if addr == "" {
		issues.add("%s.%s is required and must equal canonical address %q", prefix, name, canonical)
		return
	}
	if addr == canonical {
		return
	}
	if allowLegacy && legacy != "" && addr == legacy {
		caveats.add("%s.%s=%q reflects legacy signed-registration address (canonical=%q); accepted as a declared caveat", prefix, name, addr, canonical)
		return
	}
	issues.add("%s.%s=%q expected=%q", prefix, name, addr, canonical)
}

func validateUnknownAlias(alias *unknownAliasCanary, issues *issueCollector) {
	if alias == nil {
		issues.add("unknown_alias evidence is required")
		return
	}
	addr := normalizeEmail(alias.Address)
	if addr == "" {
		issues.add("unknown_alias.address is required")
	}
	statusesChecked := 0
	for _, check := range []struct {
		name  string
		value string
	}{
		{name: "inbound_status", value: alias.InboundStatus},
		{name: "resolve_status", value: alias.ResolveStatus},
	} {
		if strings.TrimSpace(check.value) == "" {
			continue
		}
		statusesChecked++
		if !isFailClosedStatus(check.value) {
			issues.add("unknown_alias.%s=%q must be fail-closed", check.name, check.value)
		}
	}
	if statusesChecked == 0 {
		issues.add("unknown_alias requires at least one fail-closed status: inbound_status or resolve_status")
	}
}

func ensureEvidenceRedacted(raw map[string]any) error {
	if raw == nil {
		return nil
	}
	return walkJSON(raw, "", func(path string, key string, value any) error {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if disallowedEvidenceKey(lowerKey) {
			return fmt.Errorf("disallowed field %q", path)
		}
		if s, ok := value.(string); ok && looksLikeSecretValue(lowerKey, s) {
			return fmt.Errorf("sensitive-looking value at %q", path)
		}
		return nil
	})
}

func walkJSON(value any, path string, visit func(path string, key string, value any) error) error {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if err := visit(childPath, k, item); err != nil {
				return err
			}
			if err := walkJSON(item, childPath, visit); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range v {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			// Visit array elements directly so that string values embedded in
			// arrays are checked against redaction policy (disallowed keys and
			// sensitive-looking values). Without this call, secrets placed inside
			// JSON arrays bypass the redaction verifier (CSR-020).
			key := fmt.Sprintf("[%d]", i)
			if err := visit(childPath, key, item); err != nil {
				return err
			}
			if err := walkJSON(item, childPath, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func disallowedEvidenceKey(key string) bool {
	safeKeys := map[string]struct{}{
		"content_redacted":  {},
		"contentredacted":   {},
		"email_get_content": {},
		"emailgetcontent":   {},
		"body_mcp":          {},
		"bodymcp":           {},
	}
	if _, ok := safeKeys[key]; ok {
		return false
	}
	disallowedParts := []string{"password", "secret", "token", "authorization", "provider_payload", "providerpayload", "raw", "body"}
	for _, part := range disallowedParts {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func looksLikeSecretValue(key string, value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		return true
	}
	if strings.Contains(strings.ToLower(key), "ssm") && strings.HasPrefix(trimmed, "/") {
		return false
	}
	if len(trimmed) >= 80 && !strings.Contains(trimmed, " ") && strings.Count(trimmed, "-") < 4 {
		return true
	}
	return false
}

func expectedCanonicalAddress(localID string, instanceSlug string) (string, bool) {
	localID = strings.ToLower(strings.TrimSpace(localID))
	instanceSlug = strings.ToLower(strings.TrimSpace(instanceSlug))
	if localID == "" || !managedInstanceSlugRE.MatchString(instanceSlug) {
		return "", false
	}
	return localID + "." + instanceSlug + "@" + canonicalEmailDomain, true
}

func expectedLegacyAddress(localID string) (string, bool) {
	localID = strings.ToLower(strings.TrimSpace(localID))
	if localID == "" {
		return "", false
	}
	return localID + "@" + canonicalEmailDomain, true
}

func normalizeEmail(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	addr, err := mail.ParseAddress(raw)
	if err == nil && addr != nil && strings.TrimSpace(addr.Address) != "" {
		raw = addr.Address
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func localPartLength(addr string) int {
	addr = normalizeEmail(addr)
	local, _, ok := strings.Cut(addr, "@")
	if !ok {
		return 0
	}
	return len([]byte(local))
}

func isFailClosedStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "not_found", "fail_closed", "dropped", "bounced", "rejected", "unauthorized", "forbidden":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

type issueCollector struct {
	issues []string
}

func (c *issueCollector) add(format string, args ...any) {
	c.issues = append(c.issues, fmt.Sprintf(format, args...))
}

func (c issueCollector) list() []string {
	out := append([]string(nil), c.issues...)
	sort.Strings(out)
	return out
}

func writeValidationReport(report canaryValidationReport, outputPath string, stdout io.Writer) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if strings.TrimSpace(outputPath) == "" {
		_, err = stdout.Write(data)
		return err
	}
	return os.WriteFile(outputPath, data, 0o600) // #nosec G703 -- operator-provided local report path.
}
