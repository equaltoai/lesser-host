package controlplane

import (
	"strings"
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

// computeFleetDrift computes per-instance drift for the entire active fleet
// using UpdateJob + ProvisionJob + Instance evidence. Targets come from
// config defaults; drift is computed per component.
func computeFleetDrift(
	evidence []*instanceJobsEvidence,
	lesserTarget string,
	bodyTarget string,
) fleetDriftResponse {
	entries := make([]fleetDriftEntry, 0, len(evidence))
	summary := fleetDriftSummary{Total: len(evidence)}

	for _, ev := range evidence {
		if ev == nil {
			continue
		}
		entry := computeInstanceDrift(ev, lesserTarget, bodyTarget)
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

// computeInstanceDrift computes drift for a single instance against target
// versions using evidence from update jobs, provisioning, and instance state.
//
// The MCP wired_against field is resolved from the latest successful MCP-only
// UpdateJob (which records the body version MCP was wired against at job time).
// The current_body field comes from the latest successful body update job (or
// instance state as fallback).
//
// This replaces the previous timestamp-only heuristic which could emit
// contradictory rows (wired_against == current_body while drift == wire-stale)
// and could not distinguish which body version MCP was wired against.
func computeInstanceDrift(
	ev *instanceJobsEvidence,
	lesserTarget string,
	bodyTarget string,
) fleetDriftEntry {
	if ev == nil || ev.instance == nil {
		return fleetDriftEntry{}
	}

	slug := strings.TrimSpace(ev.instance.Slug)

	// Lesser: use evidence-based current version.
	lesserCurrent := lesserVersionFromEvidence(ev)
	lesserTargetVal := strings.TrimSpace(lesserTarget)

	entry := fleetDriftEntry{
		Slug: slug,
		Lesser: fleetDriftComponent{
			Current: lesserCurrent,
			Target:  lesserTargetVal,
			Drift:   computeComponentDrift(lesserCurrent, lesserTargetVal),
		},
	}

	// Body: use evidence-based current version.
	bodyCurrent := bodyVersionFromEvidence(ev)
	bodyTargetVal := strings.TrimSpace(bodyTarget)

	entry.Body = fleetDriftComponent{
		Current: bodyCurrent,
		Target:  bodyTargetVal,
		Drift:   computeComponentDrift(bodyCurrent, bodyTargetVal),
	}

	// MCP: wired_against from latest MCP update evidence, current_body from
	// latest body update evidence. Drift is wire-stale when the versions differ.
	mcpWiredAgainst := mcpWiredAgainstFromEvidence(ev)
	mcpCurrentBody := mcpCurrentBodyFromEvidence(ev)
	mcpDrift := computeMCPEvidenceDrift(mcpWiredAgainst, mcpCurrentBody)

	entry.MCP = fleetDriftMCP{
		WiredAgainst: mcpWiredAgainst,
		CurrentBody:  mcpCurrentBody,
		Drift:        mcpDrift,
	}

	return entry
}

// computeMCPEvidenceDrift determines MCP wiring drift from evidence-derived
// versions. Returns "wire-stale" when MCP was wired against a different body
// version than what is currently deployed (regardless of whether the body is
// at the fleet target). Returns "unknown" when either version is empty.
func computeMCPEvidenceDrift(wiredAgainst, currentBody string) string {
	wiredAgainst = strings.TrimSpace(wiredAgainst)
	currentBody = strings.TrimSpace(currentBody)

	if wiredAgainst == "" || currentBody == "" {
		return stackDriftUnknown
	}
	if wiredAgainst != currentBody {
		return stackDriftWireStale
	}
	return stackDriftOK
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
