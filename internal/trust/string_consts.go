package trust

const (
	renderPolicyAlways     = "always"
	renderPolicySuspicious = "suspicious"

	overagePolicyAllow = "allow"
	overagePolicyBlock = "block"

	moderationTriggerOnReports      = "on_reports"
	moderationTriggerAlways         = "always"
	moderationTriggerLinksMediaOnly = "links_media_only"
	moderationTriggerVirality       = "virality"

	aiBatchingModeNone      = "none"
	aiBatchingModeInRequest = "in_request"
	aiBatchingModeWorker    = "worker"
	aiBatchingModeHybrid    = "hybrid"

	statusOK               = "ok"
	statusQueued           = "queued"
	statusSkipped          = "skipped"
	statusBlocked          = "blocked"
	statusDisabled         = "disabled"
	statusInvalid          = "invalid"
	statusError            = "error"
	statusNotCheckedBudget = "not_checked_budget"

	riskLow    = "low"
	riskMedium = "medium"
	riskHigh   = "high"

	schemeHTTP  = "http"
	schemeHTTPS = "https"

	modelSetDeterministic = "deterministic"

	budgetReasonDebited       = "debited"
	budgetReasonOverage       = "overage"
	budgetReasonCacheHit      = "cache_hit"
	budgetReasonNotConfigured = "budget not configured"
	budgetReasonExceeded      = "budget exceeded"

	budgetErrKindInternal      = "internal"
	budgetErrKindExceeded      = "exceeded"
	budgetErrKindNotConfigured = "not_configured"

	errorCodeBlockedSSRF = "blocked_ssrf"
	errorCodeInvalidURL  = "invalid_url"
)
