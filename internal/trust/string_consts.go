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
	statusInvalid          = "invalid"
	statusError            = "error"
	statusNotCheckedBudget = "not_checked_budget"

	riskLow    = "low"
	riskMedium = "medium"
	riskHigh   = "high"

	schemeHTTP  = "http"
	schemeHTTPS = "https"

	modelSetDeterministic = "deterministic"

	budgetReasonDebited = "debited"
	budgetReasonOverage = "overage"

	budgetErrKindInternal      = "internal"
	budgetErrKindExceeded      = "exceeded"
	budgetErrKindNotConfigured = "not_configured"
)
