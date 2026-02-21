package controlplane

import "github.com/equaltoai/lesser-host/internal/store/models"

func (s *Server) provisionJobResponseWithDerivedFields(j *models.ProvisionJob) provisionJobResponse {
	resp := provisionJobResponseFromModel(j)
	if s == nil || j == nil {
		return resp
	}
	if resp.BodyEnabled {
		resp.McpURL = managedInstanceMcpURL(s.cfg.Stage, resp.BaseDomain)
	}
	return resp
}

