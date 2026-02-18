package provisionworker

import "strings"

const (
	managedStageDev     = "dev"
	managedStageStaging = "staging"
	managedStageLive    = "live"
)

func normalizeManagedLesserStage(value string) string {
	stage := strings.ToLower(strings.TrimSpace(value))
	switch stage {
	case managedStageLive, "prod", "production":
		return managedStageLive
	case managedStageStaging, "stage":
		return managedStageStaging
	case managedStageDev, "development", defaultControlPlaneStage, "test", "sandbox", "":
		return managedStageDev
	default:
		return managedStageDev
	}
}
