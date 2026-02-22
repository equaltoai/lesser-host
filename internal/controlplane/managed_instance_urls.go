package controlplane

import (
	"fmt"
	"strings"
)

const (
	managedStageLive    = "live"
	managedStageStaging = "staging"
	managedStageDev     = "dev"
)

func managedInstanceStageForControlPlane(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	switch stage {
	case managedStageLive, "prod", "production":
		return managedStageLive
	case managedStageStaging, "stage":
		return managedStageStaging
	default:
		return managedStageDev
	}
}

func managedInstanceStageDomain(controlPlaneStage string, baseDomain string) string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if base == "" {
		return ""
	}
	stage := managedInstanceStageForControlPlane(controlPlaneStage)
	if stage == managedStageLive {
		return base
	}
	return fmt.Sprintf("%s.%s", stage, base)
}

func managedInstanceMcpURL(controlPlaneStage string, baseDomain string) string {
	stageDomain := managedInstanceStageDomain(controlPlaneStage, baseDomain)
	if stageDomain == "" {
		return ""
	}
	return fmt.Sprintf("https://api.%s/mcp", stageDomain)
}
