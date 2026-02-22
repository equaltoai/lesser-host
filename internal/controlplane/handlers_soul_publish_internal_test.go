package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/merkle"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeSoulPackStoreForPublish struct {
	puts map[string][]byte
}

func (f *fakeSoulPackStoreForPublish) PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error {
	if f.puts == nil {
		f.puts = map[string][]byte{}
	}
	f.puts[key] = append([]byte(nil), body...)
	return nil
}

func (f *fakeSoulPackStoreForPublish) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, string, string, error) {
	return nil, "", "", nil
}

type soulPublishTestDB struct {
	db     *ttmocks.MockExtendedDB
	qID    *ttmocks.MockQuery
	qRep   *ttmocks.MockQuery
	qOp    *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newSoulPublishTestDB(t *testing.T) soulPublishTestDB {
	t.Helper()

	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)
	qOp := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qRep, qOp, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
	}

	return soulPublishTestDB{db: db, qID: qID, qRep: qRep, qOp: qOp, qAudit: qAudit}
}

func TestHandleSoulPublishReputationRoot_CreatesArtifactsAndProofs(t *testing.T) {
	t.Parallel()

	runSoulPublishRootArtifactsTest(t, models.SoulOperationKindPublishReputationRoot, "0x0000000000000000000000000000000000000002", func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleSoulPublishReputationRoot(ctx)
	}, func(agentID string, repCall int) models.SoulAgentReputation {
		if repCall == 0 {
			return models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.1, Economic: 0.1, UpdatedAt: time.Now().UTC()}
		}
		return models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Composite: 0.2, Economic: 0.2, UpdatedAt: time.Now().UTC()}
	})
}

func TestHandleSoulPublishValidationRoot_CreatesArtifactsAndProofs(t *testing.T) {
	t.Parallel()

	runSoulPublishRootArtifactsTest(t, models.SoulOperationKindPublishValidationRoot, "0x0000000000000000000000000000000000000003", func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleSoulPublishValidationRoot(ctx)
	}, func(agentID string, repCall int) models.SoulAgentReputation {
		if repCall == 0 {
			return models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Validation: 0.5, ValidationsPassed: 2, UpdatedAt: time.Now().UTC()}
		}
		return models.SoulAgentReputation{AgentID: agentID, BlockRef: 10, Validation: 0.25, ValidationsPassed: 1, UpdatedAt: time.Now().UTC()}
	})
}

func runSoulPublishRootArtifactsTest(
	t *testing.T,
	opKind string,
	expectedTo string,
	call func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error),
	makeRep func(agentID string, repCall int) models.SoulAgentReputation,
) {
	t.Helper()

	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"

	s, packs := setupSoulPublishServer(t, agentA, agentB, makeRep)

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "op"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := call(s, ctx)
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	out := decodePublishRootResponse(t, resp)
	assertPublishRootMetadata(t, out, opKind, expectedTo)
	assertPublishRootArtifactsExist(t, packs, out)
	assertPublishRootProofsVerify(t, packs, out)
}

func setupSoulPublishServer(t *testing.T, agentA string, agentB string, makeRep func(agentID string, repCall int) models.SoulAgentReputation) (*Server, *fakeSoulPackStoreForPublish) {
	t.Helper()

	tdb := newSoulPublishTestDB(t)

	tdb.qID.On("All", mock.Anything).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentIdentity](t, args, 0)
		*dest = []*models.SoulAgentIdentity{
			{AgentID: agentA, Status: models.SoulAgentStatusActive},
			{AgentID: agentB, Status: models.SoulAgentStatusActive},
		}
	}).Return(nil).Once()

	repCalls := 0
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		agentID := agentA
		if repCalls > 0 {
			agentID = agentB
		}
		*dest = makeRep(agentID, repCalls)
		repCalls++
	}).Return(nil).Times(2)

	packs := &fakeSoulPackStoreForPublish{}
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                              true,
			SoulChainID:                              8453,
			SoulRegistryContractAddress:              "0x0000000000000000000000000000000000000001",
			SoulReputationAttestationContractAddress: "0x0000000000000000000000000000000000000002",
			SoulValidationAttestationContractAddress: "0x0000000000000000000000000000000000000003",
			SoulAdminSafeAddress:                     "0x0000000000000000000000000000000000000004",
			SoulTxMode:                               "safe",
		},
		soulPacks: packs,
	}

	return s, packs
}

func decodePublishRootResponse(t *testing.T, resp *apptheory.Response) publishRootResponse {
	t.Helper()

	if resp == nil {
		t.Fatalf("missing response")
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out publishRootResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func assertPublishRootMetadata(t *testing.T, out publishRootResponse, opKind string, expectedTo string) {
	t.Helper()

	if out.Operation.Kind != opKind || out.Operation.Status != models.SoulOperationStatusPending {
		t.Fatalf("unexpected operation: %#v", out.Operation)
	}
	if out.BlockRef != 10 || out.Count != 2 {
		t.Fatalf("unexpected snapshot metadata: %#v", out)
	}
	if out.SafeTx == nil || strings.ToLower(out.SafeTx.To) != expectedTo {
		t.Fatalf("unexpected safe tx: %#v", out.SafeTx)
	}
}

func assertPublishRootArtifactsExist(t *testing.T, packs *fakeSoulPackStoreForPublish, out publishRootResponse) {
	t.Helper()

	if packs == nil {
		t.Fatalf("missing packs")
	}
	requirePutsKey(t, packs, out.SnapshotKey)
	requirePutsKey(t, packs, out.ProofsKey)
	requirePutsKey(t, packs, out.ManifestKey)
}

func requirePutsKey(t *testing.T, packs *fakeSoulPackStoreForPublish, key string) []byte {
	t.Helper()

	body, ok := packs.puts[key]
	if !ok {
		t.Fatalf("expected object at %q", key)
	}
	return body
}

func assertPublishRootProofsVerify(t *testing.T, packs *fakeSoulPackStoreForPublish, out publishRootResponse) {
	t.Helper()

	body := requirePutsKey(t, packs, out.ProofsKey)

	var proofsDoc struct {
		Proofs []merkleProofEntry `json:"proofs"`
	}
	if err := json.Unmarshal(body, &proofsDoc); err != nil {
		t.Fatalf("unmarshal proofs: %v", err)
	}
	if len(proofsDoc.Proofs) != 2 {
		t.Fatalf("expected 2 proofs, got %#v", proofsDoc.Proofs)
	}

	root := common.HexToHash(out.Root)
	for _, p := range proofsDoc.Proofs {
		leaf := common.HexToHash(p.LeafHash)
		sibs := make([]common.Hash, 0, len(p.Proof))
		for _, h := range p.Proof {
			sibs = append(sibs, common.HexToHash(h))
		}
		if !merkle.Verify(leaf, p.Index, sibs, root) {
			t.Fatalf("expected proof to verify: %#v", p)
		}
	}
}
