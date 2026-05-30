//go:build ignore
// +build ignore

package controlplane

func registerOperatorRoutes(app *App, s *Server) {
	app.Get("/api/v1/operators/releases", s.handleOperatorReleases, apptheory.RequireAuth())
	app.Get("/api/v1/operators/instances/drift", s.handleOperatorInstancesDrift, apptheory.RequireAuth())
	app.Post("/api/v1/operators/instances/remediate-mcp-drift", s.handleOperatorRemediateMCPDrift, apptheory.RequireAuth())
}
