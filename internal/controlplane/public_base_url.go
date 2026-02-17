package controlplane

import "strings"

func (s *Server) publicBaseURL() string {
	if s == nil {
		return ""
	}

	rootDomain := strings.TrimSpace(s.cfg.WebAuthnRPID)
	if rootDomain == "" {
		rootDomain = "lesser.host"
	}

	stage := strings.ToLower(strings.TrimSpace(s.cfg.Stage))
	if stage == "" {
		stage = "lab"
	}

	switch stage {
	case "live", "prod", "production":
		return "https://" + rootDomain
	default:
		return "https://" + stage + "." + rootDomain
	}
}
