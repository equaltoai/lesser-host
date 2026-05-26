package controlplane

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// handleOperatorInstancesDrift returns fleet-wide drift telemetry for all
// active instances against the configured default target versions. Uses
// UpdateJob + ProvisionJob + Instance evidence to compute per-component
// drift, including MCP wired_against vs current_body. Operator JWT required.
// Response shape per Project 39 provisioning walk Change 5.3.
func (s *Server) handleOperatorInstancesDrift(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	instances, appErr := s.listActiveInstances(ctx)
	if appErr != nil {
		return nil, appErr
	}

	evidence := gatherFleetEvidence(ctx, s, instances)

	lesserTarget := strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	bodyTarget := strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)

	resp := computeFleetDrift(evidence, lesserTarget, bodyTarget)
	return apptheory.JSON(http.StatusOK, resp)
}
