package controlplane

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
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
