package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulInstanceHostedInstanceTrustNoWalletBeginCreatesFreshLaneAfterRestartFailure(t *testing.T) {
	t.Parallel()

	reg, identity, promotion := soulInstanceHostedInstanceTrustFixture(t)
	promotion.LatestConversationID = mintConversationTestConversationID
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = *identity
	}).Twice()
	tdb.qPromotion.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qPromotion)
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = promotion
	}).Once()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = models.HostedGenesisSession{
			InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
			RegistrationID: reg.ID,
			AgentID:        reg.AgentID,
			ConversationID: mintConversationTestConversationID,
			Status:         string(hostedgenesis.StatusFailed),
			Failure: &hostedgenesis.Failure{
				Code:    hostedgenesis.FailureCodeInvalidProducedDeclarations,
				Message: hostedgenesis.FailureMessage(hostedgenesis.FailureCodeInvalidProducedDeclarations),
				Recovery: hostedgenesis.Recovery{
					Action: hostedgenesis.RecoveryActionRestartSoulBootstrap,
					Reason: string(hostedgenesis.DeclarationCodeCapabilities),
				},
			},
		}
	}).Once()

	body, _ := json.Marshal(soulAgentRegistrationBeginRequest{
		Domain:  strings.ToUpper(reg.DomainNormalized),
		LocalID: reg.LocalID,
	})
	resp, err := s.handleSoulInstanceAgentRegistrationBegin(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		body,
		nil,
	))
	if err != nil {
		t.Fatalf("unexpected fresh-lane begin error: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201 for fresh lane, got %d", resp.Status)
	}
	var out soulAgentRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.ID == reg.ID || out.Registration.AgentID != reg.AgentID || out.Wallet != nil {
		t.Fatalf("expected a fresh hosted registration lane, got %#v", out.Registration)
	}
	tdb.qReg.AssertCalled(t, "Create")
}
