package controlplane

import (
	"fmt"

	"github.com/equaltoai/lesser-host/internal/manageddomain"
)

func managedInstanceStageDomain(controlPlaneStage string, baseDomain string) string {
	return manageddomain.StageDomain(controlPlaneStage, baseDomain)
}

func managedInstanceMcpURL(controlPlaneStage string, baseDomain string) string {
	stageDomain := managedInstanceStageDomain(controlPlaneStage, baseDomain)
	if stageDomain == "" {
		return ""
	}
	return fmt.Sprintf("https://api.%s/mcp/{actor}", stageDomain)
}
