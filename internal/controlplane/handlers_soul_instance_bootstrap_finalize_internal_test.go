package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulInstanceGetRegistrationMintConversation_RepairsPublishedPendingHostedInstanceTrust(t *testing.T) {
	t.Parallel()

	reg, identity, completedConv := soulInstanceHostedFinalizeFixture(t)
	identity.SelfDescriptionVersion = 1
	identity.Status = models.SoulAgentStatusPending
	identity.LifecycleStatus = models.SoulAgentStatusPending
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	stubSoulInstanceFinalizeReadContext(t, tdb, reg, identity, completedConv)
	publishedAt := time.Date(2026, 3, 7, 12, 10, 0, 0, time.UTC)
	// Pre-correction prototype rows recorded completed in the promotion while
	// leaving authoritative session and compatibility truth declaration_ready.
	promotion := updateSoulAgentPromotionForConversation(buildSoulAgentPromotionFromRegistration(&reg, publishedAt), mintConversationTestConversationID, models.SoulMintConversationStatusCompleted, publishedAt)
	promotion = updateSoulAgentPromotionForGraduation(promotion, 1, publishedAt)
	tdb.qPromotion.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qPromotion)
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		*testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0) = *promotion
	}).Once()
	qVersion := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qVersion).Maybe()
	addStandardMockQueryStubs(qVersion)
	qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
		*testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0) = models.SoulAgentVersion{
			AgentID:            reg.AgentID,
			VersionNumber:      1,
			RegistrationURI:    "s3://bucket/registration.json",
			RegistrationSHA256: strings.Repeat("b", 64),
			CreatedAt:          publishedAt.Add(-time.Minute),
		}
	}).Once()
	tx := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tx
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tx).Once()
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tx).Once()
	tx.On("Execute").Return(nil).Once()

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", resp.Status, string(resp.Body))
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != string(hostedgenesis.StatusPublished) || out.Conversation.PublishedVersion != 1 || out.Conversation.PublishedAt == nil || out.Conversation.PollAfterSeconds != 0 || out.Conversation.ProducedDeclarations != nil {
		t.Fatalf("expected terminal published convergence, got %#v", out.Conversation)
	}
	assertSoulInstanceFinalizeIdentityActivationPersisted(t, tdb, false)
	if packs, ok := s.soulPacks.(*fakeSoulPackStoreForPublish); !ok {
		t.Fatalf("unexpected soul pack store: %T", s.soulPacks)
	} else if len(packs.puts) != 0 {
		t.Fatalf("published repair must not rewrite registration artifacts: %#v", packs.puts)
	}
}

func assertSoulInstanceFinalizeIdentityActivationPersisted(t *testing.T, tdb *mintConversationTestDB, wantVersion bool) {
	t.Helper()
	tdb.qIdentity.AssertCalled(t, "Update", mock.MatchedBy(func(fields []string) bool {
		required := []string{"Status", "LifecycleStatus", "AnchorState", "UpdatedAt"}
		if wantVersion {
			required = append(required, "SelfDescriptionVersion")
		}
		for _, req := range required {
			found := false
			for _, got := range fields {
				if got == req {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}))
}
