package provisionworker

import "strings"

func normalizeManagedLesserStage(value string) string {
	stage := strings.ToLower(strings.TrimSpace(value))
	switch stage {
	case "live", "prod", "production":
		return "live"
	case "staging", "stage":
		return "staging"
	case "dev", "development", "lab", "test", "sandbox", "":
		return "dev"
	default:
		return "dev"
	}
}
