package provisionworker

import "strings"

const (
	managedStageDev             = "dev"
	managedStageStaging         = "staging"
	managedStageLive            = "live"
	managedStageLiveProdAlias   = "prod"
	managedStageLiveLongAlias   = "production"
	managedStageStagingAlias    = "stage"
	managedStageDevelopmentName = "development"
)

func normalizeManagedLesserStage(value string) string {
	stage := strings.ToLower(strings.TrimSpace(value))
	switch stage {
	case managedStageLive, managedStageLiveProdAlias, managedStageLiveLongAlias:
		return managedStageLive
	case managedStageStaging, managedStageStagingAlias:
		return managedStageStaging
	case managedStageDev, managedStageDevelopmentName, defaultControlPlaneStage, "test", "sandbox", "":
		return managedStageDev
	default:
		return managedStageDev
	}
}
