//go:build ignore
// +build ignore

package controlplane

func (s *Server) handleOperatorInstancesDrift(ctx *apptheory.Context) (*apptheory.Response, error) {
	_ = "requireOperator(ctx)"
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(200, nil)
}
