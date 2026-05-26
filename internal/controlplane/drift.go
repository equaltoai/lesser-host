package controlplane

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fleetDriftComponent holds per-component drift telemetry for a single instance.
type fleetDriftComponent struct {
	Current string `json:"current"`
	Target  string `json:"target"`
	Drift   string `json:"drift"`
}

// fleetDriftMCP holds MCP-specific drift telemetry.
type fleetDriftMCP struct {
	WiredAgainst string `json:"wired_against"`
	CurrentBody  string `json:"current_body"`
	Drift        string `json:"drift"`
}

// fleetDriftEntry is a per-instance entry in the fleet drift response.
type fleetDriftEntry struct {
	Slug   string              `json:"slug"`
	Lesser fleetDriftComponent `json:"lesser"`
	Body   fleetDriftComponent `json:"body"`
	MCP    fleetDriftMCP       `json:"mcp"`
}

// fleetDriftSummary aggregates per-component drift counts across the fleet.
type fleetDriftSummary struct {
	Total        int `json:"total"`
	LesserStale  int `json:"lesser_stale"`
	BodyStale    int `json:"body_stale"`
	MCPWireStale int `json:"mcp_wire_stale"`
}

// fleetDriftResponse is the operator-facing fleet drift response per
// Project 39 provisioning walk Change 5.3.
type fleetDriftResponse struct {
	Instances []fleetDriftEntry `json:"instances"`
	Summary   fleetDriftSummary `json:"summary"`
}

// computeFleetDrift computes per-instance drift for the entire active fleet.
// Targets come from config defaults; drift is computed per component.
func computeFleetDrift(
	instances []*models.Instance,
	lesserTarget string,
	bodyTarget string,
) fleetDriftResponse {
	entries := make([]fleetDriftEntry, 0, len(instances))
	summary := fleetDriftSummary{Total: len(instances)}

	for _, inst := range instances {
		entry := computeInstanceDrift(inst, lesserTarget, bodyTarget)
		entries = append(entries, entry)

		if entry.Lesser.Drift == stackDriftStale {
			summary.LesserStale++
		}
		if entry.Body.Drift == stackDriftStale {
			summary.BodyStale++
		}
		if entry.MCP.Drift == stackDriftWireStale {
			summary.MCPWireStale++
		}
	}

	return fleetDriftResponse{Instances: entries, Summary: summary}
}

// computeInstanceDrift computes drift for a single instance against target versions.
func computeInstanceDrift(
	inst *models.Instance,
	lesserTarget string,
	bodyTarget string,
) fleetDriftEntry {
	slug := strings.TrimSpace(inst.Slug)
	lesserCurrent := strings.TrimSpace(inst.LesserVersion)
	bodyCurrent := strings.TrimSpace(inst.LesserBodyVersion)

	entry := fleetDriftEntry{
		Slug: slug,
		Lesser: fleetDriftComponent{
			Current: lesserCurrent,
			Target:  strings.TrimSpace(lesserTarget),
			Drift:   computeComponentDrift(lesserCurrent, lesserTarget),
		},
		Body: fleetDriftComponent{
			Current: bodyCurrent,
			Target:  strings.TrimSpace(bodyTarget),
			Drift:   computeComponentDrift(bodyCurrent, bodyTarget),
		},
	}

	// MCP drift: wired_against comes from the Instance's LesserBodyVersion
	// (which reflects what MCP was wired against), current_body is the
	// body version currently deployed.
	mcpWiredAgainst := bodyCurrent // By default, MCP is wired against the deploy-time body version
	mcpDrift := stackDriftUnknown
	if bodyCurrent != "" {
		// When MCP is wired, it's against the body version at time of wiring.
		// We approximate this from the instance's stored body version.
		// A true wire-stale condition exists when:
		// 1. The instance has a body version
		// 2. The body version is at target (i.e., no body drift)
		// 3. But the MCP wiring may be out of sync
		// For the fleet drift endpoint, we flag wire-stale when the
		// body version is at target but there's a possible stale wiring
		// (which is conservatively all instances with body != "").
		//
		// In practice, the caller (remediate-mcp-drift) uses a more precise
		// check via update jobs. For the drift display, we conservatively
		// mark body-up-to-date instances as potentially wire-stale if MCP
		// hasn't been recently re-wired.
		mcpDrift = computeFleetMCPDrift(inst, bodyCurrent, bodyTarget)
	}

	entry.MCP = fleetDriftMCP{
		WiredAgainst: mcpWiredAgainst,
		CurrentBody:  bodyCurrent,
		Drift:        mcpDrift,
	}

	return entry
}

// computeComponentDrift determines whether a component version is stale
// relative to its target version.
func computeComponentDrift(current string, target string) string {
	current = strings.TrimSpace(current)
	target = strings.TrimSpace(target)

	if current == "" || target == "" {
		return stackDriftUnknown
	}
	if strings.EqualFold(current, target) {
		return stackDriftOK
	}
	// Compare versions numerically if possible.
	if compareVersions(current, target) < 0 {
		return stackDriftStale
	}
	// current is newer than target — consider it ok (ahead of fleet).
	return stackDriftOK
}

// computeFleetMCPDrift checks MCP wiring staleness. Returns "wire-stale" if
// MCP was wired against a body version older than the currently deployed body
// version and the current body version is at or below the target.
//
// For the fleet-level drift view, we use Instance timestamps as a heuristic:
// if McpWiredAt is set and predates LesserBodyUpdateAt, and the body version
// changed between the two timestamps, MCP is likely wire-stale.
//
// When MCP hasn't been explicitly wired (McpWiredAt is zero), we report
// "ok" for instances with no body (MCP isn't applicable) and "unknown"
// for instances with a body version.
func computeFleetMCPDrift(inst *models.Instance, bodyCurrent string, bodyTarget string) string {
	if bodyCurrent == "" {
		return stackDriftUnknown
	}

	// If McpWiredAt is set and predates LesserBodyUpdateAt, MCP may be stale.
	// But without per-job detail, the Instance-level heuristic is too coarse.
	// For the fleet drift endpoint, we conservatively report "ok" when the
	// body version is at target and MCP was wired at or after the body update.
	// "wire-stale" only when MCP predates the last body update.
	mcpWiredAt := inst.McpWiredAt
	bodyUpdatedAt := inst.LesserBodyUpdateAt

	if !mcpWiredAt.IsZero() && !bodyUpdatedAt.IsZero() && mcpWiredAt.Before(bodyUpdatedAt) {
		return stackDriftWireStale
	}

	// If MCP has never been wired but body is present, it's unknown.
	if mcpWiredAt.IsZero() {
		return stackDriftUnknown
	}

	// Default: MCP wiring appears up-to-date.
	return stackDriftOK
}
