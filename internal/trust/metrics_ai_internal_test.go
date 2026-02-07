package trust

import (
	"errors"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai"
)

func TestEmitAIRequestMetrics_NoPanics(t *testing.T) {
	t.Parallel()

	var s *Server
	s.emitAIRequestMetrics("", "", ai.Response{}, nil)

	s = &Server{}
	s.emitAIRequestMetrics("", "", ai.Response{}, nil)

	s = &Server{cfg: configForTests()}
	s.emitAIRequestMetrics("inst", "m", ai.Response{Status: ai.JobStatusOK, Cached: true}, nil)
	s.emitAIRequestMetrics("inst", "m", ai.Response{Status: ai.JobStatusQueued}, nil)
	s.emitAIRequestMetrics("inst", "m", ai.Response{Status: ai.JobStatusNotCheckedBudget, Budget: ai.BudgetDecision{Reason: "concurrency_limit"}}, nil)
	s.emitAIRequestMetrics("inst", "m", ai.Response{Status: ai.JobStatusError}, errors.New("boom"))
	s.emitAIRequestMetrics("inst", "m", ai.Response{Status: ""}, errors.New("boom"))
}
