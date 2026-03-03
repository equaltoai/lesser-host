package controlplane

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulCreateRelationship_RequiresCreatedAt(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	toAgentIDHex := "0x" + strings.Repeat("22", 32)
	fromAgentIDHex := "0x" + strings.Repeat("11", 32)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()

	identityCall := 0
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if identityCall == 0 {
			*dest = models.SoulAgentIdentity{
				AgentID: fromAgentIDHex,
				Domain:  "example.com",
				LocalID: "agent-alice",
				Wallet:  wallet,
				Status:  models.SoulAgentStatusActive,
			}
		} else {
			*dest = models.SoulAgentIdentity{
				AgentID: toAgentIDHex,
				Domain:  "example.com",
				LocalID: "agent-bob",
				Wallet:  "0x000000000000000000000000000000000000beef",
				Status:  models.SoulAgentStatusActive,
			}
		}
		identityCall++
	}).Twice()

	// Build legacy signature over keccak256(bytes(message)).
	message := "delegation for summaries"
	messageDigest := crypto.Keccak256([]byte(message))
	sig, _ := crypto.Sign(accounts.TextHash(messageDigest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody, _ := json.Marshal(map[string]any{
		"from_agent_id": fromAgentIDHex,
		"type":          "delegation",
		"context":       `{"taskType":"summarization"}`,
		"message":       message,
		"signature":     sigHex,
	})

	ctx := &apptheory.Context{
		RequestID:    "r-relationship-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": toAgentIDHex},
		Request:      apptheory.Request{Body: reqBody},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, gotErr := s.handleSoulCreateRelationship(ctx)
	if gotErr == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := gotErr.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", gotErr)
	}
	if appErr.Message != "created_at is required" {
		t.Fatalf("expected created_at error, got %q", appErr.Message)
	}
}

func TestHandleSoulCreateRelationship_StrictIntegrity_VerifiesScopedSignature(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	createdAtRaw := "2026-03-01T00:00:00Z"
	expectedCreatedAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulV2StrictIntegrity:       true,
		},
	}

	toAgentIDHex := "0x" + strings.Repeat("22", 32)
	fromAgentIDHex := "0x" + strings.Repeat("11", 32)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()

	identityCall := 0
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if identityCall == 0 {
			*dest = models.SoulAgentIdentity{
				AgentID: fromAgentIDHex,
				Domain:  "example.com",
				LocalID: "agent-alice",
				Wallet:  wallet,
				Status:  models.SoulAgentStatusActive,
			}
		} else {
			*dest = models.SoulAgentIdentity{
				AgentID: toAgentIDHex,
				Domain:  "example.com",
				LocalID: "agent-bob",
				Wallet:  "0x000000000000000000000000000000000000beef",
				Status:  models.SoulAgentStatusActive,
			}
		}
		identityCall++
	}).Twice()

	createKinds := map[string]bool{}
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Twice().Run(func(args mock.Arguments) {
		switch item := args.Get(0).(type) {
		case *models.SoulAgentRelationship:
			createKinds["relationship"] = true
			if item.FromAgentID != fromAgentIDHex {
				t.Fatalf("unexpected from agent id: %q", item.FromAgentID)
			}
			if item.ToAgentID != toAgentIDHex {
				t.Fatalf("unexpected to agent id: %q", item.ToAgentID)
			}
			if item.Type != models.SoulRelationshipTypeDelegation {
				t.Fatalf("unexpected relationship type: %q", item.Type)
			}
			if !item.CreatedAt.Equal(expectedCreatedAt) {
				t.Fatalf("expected created_at %s, got %s", expectedCreatedAt, item.CreatedAt)
			}
		case *models.SoulRelationshipFromIndex:
			createKinds["from_index"] = true
			if item.FromAgentID != fromAgentIDHex {
				t.Fatalf("unexpected from index from agent id: %q", item.FromAgentID)
			}
			if item.ToAgentID != toAgentIDHex {
				t.Fatalf("unexpected from index to agent id: %q", item.ToAgentID)
			}
			if item.Type != models.SoulRelationshipTypeDelegation {
				t.Fatalf("unexpected from index relationship type: %q", item.Type)
			}
			if !item.CreatedAt.Equal(expectedCreatedAt) {
				t.Fatalf("expected created_at %s, got %s", expectedCreatedAt, item.CreatedAt)
			}
		default:
			t.Fatalf("unexpected tx create item type %T", item)
		}
	})
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Maybe()

	message := "delegation for summaries"
	contextJSON := `{"taskType":"summarization"}`
	contextMap, _, _, appErr := parseRelationshipContext(json.RawMessage(contextJSON))
	if appErr != nil {
		t.Fatalf("parse context: %v", appErr)
	}
	digest, appErr := computeSoulRelationshipDigest(fromAgentIDHex, toAgentIDHex, models.SoulRelationshipTypeDelegation, contextMap, message, createdAtRaw)
	if appErr != nil {
		t.Fatalf("compute digest: %v", appErr)
	}
	sig, _ := crypto.Sign(accounts.TextHash(digest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody, _ := json.Marshal(map[string]any{
		"from_agent_id": fromAgentIDHex,
		"type":          "delegation",
		"context":       contextJSON,
		"message":       message,
		"created_at":    createdAtRaw,
		"signature":     sigHex,
	})

	ctx := &apptheory.Context{
		RequestID:    "r-relationship-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": toAgentIDHex},
		Request:      apptheory.Request{Body: reqBody},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, gotErr := s.handleSoulCreateRelationship(ctx)
	if gotErr != nil {
		t.Fatalf("unexpected err: %v", gotErr)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if !createKinds["relationship"] || !createKinds["from_index"] {
		t.Fatalf("expected relationship + from_index creates, got %#v", createKinds)
	}
}
