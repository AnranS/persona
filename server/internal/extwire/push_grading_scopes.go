package extwire

// ScopePushGrading lets an explicitly authorized extension manage its own push grading layer and preview combined
// push grading decisions without mutating notification state (GET/PUT/DELETE /api/v1/push/grading/layer,
// POST /api/v1/push/grading/preview). Granted by name only, never included in a legacy every-scope token.
const ScopePushGrading = "push.grading"

func init() {
	addRouteScopes(map[string]string{
		"GET /api/v1/push/grading/layer":    ScopePushGrading,
		"PUT /api/v1/push/grading/layer":    ScopePushGrading,
		"DELETE /api/v1/push/grading/layer": ScopePushGrading,
		"POST /api/v1/push/grading/preview": ScopePushGrading,
	})
}
