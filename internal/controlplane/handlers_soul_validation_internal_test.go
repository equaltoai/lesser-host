package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

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

	expectSoulValidationIdentity(t, tdb.qID, agentID)
	issueOut := issueSoulValidationChallengeForTest(t, s, agentID)
	chalID := issueOut.ChallengeID

	expectSoulValidationChallengeLookup(t, tdb.qChal, issuedSoulValidationChallenge(agentID, chalID))
	recordSoulValidationResponseForTest(t, s, agentID, chalID)

	expectSoulValidationChallengeLookup(t, tdb.qChal, respondedSoulValidationChallenge(agentID, chalID))
	evalOut := evaluateSoulValidationChallengeForTest(t, s, agentID, chalID)

	if evalOut.Challenge.Status != models.SoulValidationChallengeStatusEvaluated || evalOut.Record.Result != models.SoulValidationResultPass {
		t.Fatalf("unexpected evaluate output: %#v", evalOut)
	}
	if evalOut.Record.Request != "operator challenge material" || evalOut.Record.Response != "agent response material" {
		t.Fatalf("operator evaluation response should retain raw transcript: %#v", evalOut.Record)
	}
}

func expectSoulValidationIdentity(t *testing.T, q *ttmocks.MockQuery, agentID string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive}
	}).Once()
}

func issueSoulValidationChallengeForTest(t *testing.T, s *Server, agentID string) models.SoulAgentValidationChallenge {
	t.Helper()
	issueBody, _ := json.Marshal(soulIssueValidationChallengeRequest{
		ChallengeType: "identity_verify",
		Request:       "operator challenge material",
		TTLSeconds:    -1,
	})
	issueResp, err := s.handleSoulIssueValidationChallenge(newOperatorSoulValidationCtx("r1", agentID, "", issueBody))
	if err != nil || issueResp.Status != 200 {
		t.Fatalf("issue: resp=%#v err=%v", issueResp, err)
	}
	var issueOut soulIssueValidationChallengeResponse
	if unmarshalErr := json.Unmarshal(issueResp.Body, &issueOut); unmarshalErr != nil {
		t.Fatalf("unmarshal issue: %v", unmarshalErr)
	}
	if issueOut.Challenge.AgentID != agentID || issueOut.Challenge.ChallengeID == "" || issueOut.Challenge.Status != models.SoulValidationChallengeStatusIssued {
		t.Fatalf("unexpected challenge: %#v", issueOut.Challenge)
	}
	if issueOut.Challenge.ValidatorID != soulValidatorSystem {
		t.Fatalf("expected default validator, got %#v", issueOut.Challenge.ValidatorID)
	}
	return issueOut.Challenge
}

func expectSoulValidationChallengeLookup(t *testing.T, q *ttmocks.MockQuery, challenge models.SoulAgentValidationChallenge) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentValidationChallenge")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentValidationChallenge](t, args, 0)
		*dest = challenge
	}).Once()
}

func issuedSoulValidationChallenge(agentID string, chalID string) models.SoulAgentValidationChallenge {
	issuedAt := time.Now().Add(-time.Minute).UTC()
	return models.SoulAgentValidationChallenge{
		AgentID:       agentID,
		ChallengeID:   chalID,
		ChallengeType: "identity_verify",
		ValidatorID:   soulValidatorSystem,
		Request:       "operator challenge material",
		Status:        models.SoulValidationChallengeStatusIssued,
		IssuedAt:      issuedAt,
		UpdatedAt:     issuedAt,
	}
}

func respondedSoulValidationChallenge(agentID string, chalID string) models.SoulAgentValidationChallenge {
	chal := issuedSoulValidationChallenge(agentID, chalID)
	chal.Response = "agent response material"
	chal.Status = models.SoulValidationChallengeStatusResponded
	chal.RespondedAt = time.Now().Add(-30 * time.Second).UTC()
	chal.UpdatedAt = chal.RespondedAt
	return chal
}

func recordSoulValidationResponseForTest(t *testing.T, s *Server, agentID string, chalID string) {
	t.Helper()
	respBody, _ := json.Marshal(soulRecordValidationResponseRequest{Response: "agent response material"})
	respResp, err := s.handleSoulRecordValidationResponse(newOperatorSoulValidationCtx("r2", agentID, chalID, respBody))
	if err != nil || respResp.Status != 200 {
		t.Fatalf("response: resp=%#v err=%v", respResp, err)
	}
}

func evaluateSoulValidationChallengeForTest(t *testing.T, s *Server, agentID string, chalID string) soulEvaluateValidationChallengeResponse {
	t.Helper()
	evalBody, _ := json.Marshal(soulEvaluateValidationChallengeRequest{Result: models.SoulValidationResultPass})
	evalResp, err := s.handleSoulEvaluateValidationChallenge(newOperatorSoulValidationCtx("r3", agentID, chalID, evalBody))
	if err != nil || evalResp.Status != 200 {
		t.Fatalf("evaluate: resp=%#v err=%v", evalResp, err)
	}
	var evalOut soulEvaluateValidationChallengeResponse
	if err := json.Unmarshal(evalResp.Body, &evalOut); err != nil {
		t.Fatalf("unmarshal eval: %v", err)
	}
	return evalOut
}

func newOperatorSoulValidationCtx(requestID string, agentID string, chalID string, body []byte) *apptheory.Context {
	params := map[string]string{"agentId": agentID}
	if strings.TrimSpace(chalID) != "" {
		params["challengeId"] = chalID
	}
	ctx := &apptheory.Context{
		RequestID:    requestID,
		AuthIdentity: "op",
		Params:       params,
		Request:      apptheory.Request{Body: body},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
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
