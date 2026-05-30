//go:build ignore
// +build ignore

package controlplane

func (s *Server) handleOperatorRemediateMCPDrift(ctx *apptheory.Context) (*apptheory.Response, error) {
	requireOperator(ctx)
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(200, nil)
}
