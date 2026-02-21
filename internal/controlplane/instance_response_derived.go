package controlplane

import "github.com/equaltoai/lesser-host/internal/store/models"

func (s *Server) instanceResponseWithDerivedFields(inst *models.Instance) instanceResponse {
	resp := instanceResponseFromModel(inst)
	if s == nil || inst == nil {
		return resp
	}
	if resp.BodyEnabled {
		resp.McpURL = managedInstanceMcpURL(s.cfg.Stage, resp.HostedBaseDomain)
	}
	return resp
}

