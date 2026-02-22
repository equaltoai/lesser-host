package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulValidationTestDB struct {
	db     *ttmocks.MockExtendedDB
	qID    *ttmocks.MockQuery
	qChal  *ttmocks.MockQuery
	qRec   *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newSoulValidationTestDB() soulValidationTestDB {
	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qChal := new(ttmocks.MockQuery)
	qRec := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(qChal).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentValidationRecord")).Return(qRec).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qChal, qRec, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
	}

	return soulValidationTestDB{db: db, qID: qID, qChal: qChal, qRec: qRec, qAudit: qAudit}
}

func TestSoulValidationHandlers_IssueRespondEvaluate(t *testing.T) {
	t.Parallel()

	tdb := newSoulValidationTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	issueBody, _ := json.Marshal(soulIssueValidationChallengeRequest{
		ChallengeType: "identity_verify",
		TTLSeconds:    -1,
	})
	issueCtx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID},
		Request:      apptheory.Request{Body: issueBody},
	}
	issueCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	issueResp, err := s.handleSoulIssueValidationChallenge(issueCtx)
	if err != nil || issueResp.Status != 200 {
		t.Fatalf("issue: resp=%#v err=%v", issueResp, err)
	}
	var issueOut soulIssueValidationChallengeResponse
	if err := json.Unmarshal(issueResp.Body, &issueOut); err != nil {
		t.Fatalf("unmarshal issue: %v", err)
	}
	if issueOut.Challenge.AgentID != agentID || issueOut.Challenge.ChallengeID == "" || issueOut.Challenge.Status != models.SoulValidationChallengeStatusIssued {
		t.Fatalf("unexpected challenge: %#v", issueOut.Challenge)
	}
	if issueOut.Challenge.ValidatorID != "system" {
		t.Fatalf("expected default validator, got %#v", issueOut.Challenge.ValidatorID)
	}

	chalID := issueOut.Challenge.ChallengeID

	tdb.qChal.On("First", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentValidationChallenge](t, args, 0)
		*dest = models.SoulAgentValidationChallenge{
			AgentID:       agentID,
			ChallengeID:   chalID,
			ChallengeType: "identity_verify",
			ValidatorID:   "system",
			Request:       "req",
			Status:        models.SoulValidationChallengeStatusIssued,
			IssuedAt:      time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:     time.Now().Add(-time.Minute).UTC(),
		}
	}).Times(2)

	respBody, _ := json.Marshal(soulRecordValidationResponseRequest{Response: "ok"})
	respCtx := &apptheory.Context{
		RequestID:    "r2",
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID, "challengeId": chalID},
		Request:      apptheory.Request{Body: respBody},
	}
	respCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	respResp, err := s.handleSoulRecordValidationResponse(respCtx)
	if err != nil || respResp.Status != 200 {
		t.Fatalf("response: resp=%#v err=%v", respResp, err)
	}

	evalBody, _ := json.Marshal(soulEvaluateValidationChallengeRequest{Result: models.SoulValidationResultPass})
	evalCtx := &apptheory.Context{
		RequestID:    "r3",
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID, "challengeId": chalID},
		Request:      apptheory.Request{Body: evalBody},
	}
	evalCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	evalResp, err := s.handleSoulEvaluateValidationChallenge(evalCtx)
	if err != nil || evalResp.Status != 200 {
		t.Fatalf("evaluate: resp=%#v err=%v", evalResp, err)
	}
	var evalOut soulEvaluateValidationChallengeResponse
	if err := json.Unmarshal(evalResp.Body, &evalOut); err != nil {
		t.Fatalf("unmarshal eval: %v", err)
	}
	if evalOut.Challenge.Status != models.SoulValidationChallengeStatusEvaluated || evalOut.Record.Result != models.SoulValidationResultPass {
		t.Fatalf("unexpected evaluate output: %#v", evalOut)
	}
}

func TestSoulValidationHandlers_NotFoundAndBadRequest(t *testing.T) {
	t.Parallel()

	tdb := newSoulValidationTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}

	agentID := "0x" + strings.Repeat("11", 32)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()

	issueBody, _ := json.Marshal(soulIssueValidationChallengeRequest{ChallengeType: "nope"})
	issueCtx := &apptheory.Context{AuthIdentity: "op", Params: map[string]string{"agentId": agentID}, Request: apptheory.Request{Body: issueBody}}
	issueCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if _, err := s.handleSoulIssueValidationChallenge(issueCtx); err == nil {
		t.Fatalf("expected bad_request")
	}

	tdb.qChal.On("First", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(theoryErrors.ErrItemNotFound).Once()
	respBody, _ := json.Marshal(soulRecordValidationResponseRequest{Response: "ok"})
	respCtx := &apptheory.Context{
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID, "challengeId": "missing"},
		Request:      apptheory.Request{Body: respBody},
	}
	respCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if _, err := s.handleSoulRecordValidationResponse(respCtx); err == nil {
		t.Fatalf("expected not_found")
	}
}

