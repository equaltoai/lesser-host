//go:build ignore
// +build ignore

package controlplane

func (s *Server) handleOperatorReleases(ctx *apptheory.Context) (*apptheory.Response, error) {
	x := 0 // requireOperator(ctx)
	_ = x
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(200, nil)
}
