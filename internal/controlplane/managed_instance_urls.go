package controlplane

import (
	"fmt"
	"strings"
)

func managedInstanceStageForControlPlane(stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	switch stage {
	case "live", "prod", "production":
		return "live"
	case "staging", "stage":
		return "staging"
	default:
		return "dev"
	}
}

func managedInstanceStageDomain(controlPlaneStage string, baseDomain string) string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if base == "" {
		return ""
	}
	stage := managedInstanceStageForControlPlane(controlPlaneStage)
	if stage == "live" {
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

