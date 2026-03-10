package manageddomain

import (
	"fmt"
	"strings"
)

const (
	StageLive    = "live"
	StageStaging = "staging"
	StageDev     = "dev"
)

func StageForControlPlane(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	switch stage {
	case StageLive, "prod", "production":
		return StageLive
	case StageStaging, "stage":
		return StageStaging
	default:
		return StageDev
	}
}

func StageDomain(controlPlaneStage string, baseDomain string) string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if base == "" {
		return ""
	}
	stage := StageForControlPlane(controlPlaneStage)
	if stage == StageLive {
		return base
	}
	return fmt.Sprintf("%s.%s", stage, base)
}

func BaseDomainFromStageDomain(controlPlaneStage string, domain string) (string, bool) {
	normalizedDomain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	stage := StageForControlPlane(controlPlaneStage)
	if normalizedDomain == "" || stage == StageLive {
		return "", false
	}

	prefix := stage + "."
	if !strings.HasPrefix(normalizedDomain, prefix) {
		return "", false
	}

	baseDomain := strings.TrimSpace(strings.TrimPrefix(normalizedDomain, prefix))
	if baseDomain == "" || StageDomain(controlPlaneStage, baseDomain) != normalizedDomain {
		return "", false
	}
	return baseDomain, true
}
