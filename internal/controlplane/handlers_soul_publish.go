package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/merkle"
	"github.com/equaltoai/lesser-host/internal/soulattestations"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type merkleProofEntry struct {
	AgentID   string   `json:"agent_id"`
	Index     int      `json:"index"`
	LeafHash  string   `json:"leaf_hash"`
	Proof     []string `json:"proof"`
	Root      string   `json:"root"`
	BlockRef  int64    `json:"block_ref"`
	LeafCodec string   `json:"leaf_codec"`
	TreeCodec string   `json:"tree_codec"`
}

type reputationRootSnapshot struct {
	Version     string                       `json:"version"`
	Kind        string                       `json:"kind"`
	Root        string                       `json:"root"`
	BlockRef    int64                        `json:"block_ref"`
	Count       int                          `json:"count"`
	ComputedAt  time.Time                    `json:"computed_at"`
	LeafCodec   string                       `json:"leaf_codec"`
	TreeCodec   string                       `json:"tree_codec"`
	Reputations []models.SoulAgentReputation `json:"reputations"`
}

type validationRootLeaf struct {
	Version           string    `json:"version"`
	AgentID           string    `json:"agent_id"`
	BlockRef          int64     `json:"block_ref,omitempty"`
	Validation        float64   `json:"validation"`
	ValidationsPassed int64     `json:"validations_passed"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type validationRootSnapshot struct {
	Version    string               `json:"version"`
	Kind       string               `json:"kind"`
	Root       string               `json:"root"`
	BlockRef   int64                `json:"block_ref"`
	Count      int                  `json:"count"`
	ComputedAt time.Time            `json:"computed_at"`
	LeafCodec  string               `json:"leaf_codec"`
	TreeCodec  string               `json:"tree_codec"`
	Leaves     []validationRootLeaf `json:"leaves"`
}

type publishRootResponse struct {
	Operation   models.SoulOperation `json:"operation"`
	SafeTx      *safeTxPayload       `json:"safe_tx,omitempty"`
	Root        string               `json:"root"`
	BlockRef    int64                `json:"block_ref"`
	Count       int                  `json:"count"`
	SnapshotKey string               `json:"snapshot_key"`
	ProofsKey   string               `json:"proofs_key"`
	ManifestKey string               `json:"manifest_key"`
}

func (s *Server) requireSoulPublishPrereqs(ctx *apptheory.Context) *apptheory.AppError {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return appErr
	}
	if s == nil || s.soulPacks == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	return nil
}

func (s *Server) handleSoulPublishReputationRoot(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulPublishPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	contractAddr, txTo, appErr := s.soulReputationAttestationContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	reps, blockRef, appErr := s.loadSortedSoulReputationsForActiveAgents(ctx.Context())
	if appErr != nil {
		return nil, appErr
	}

	root, rootHex, snapKey, proofsKey, manifestKey, now, appErr := s.buildAndPersistSoulReputationRootArtifacts(ctx.Context(), reps, blockRef)
	if appErr != nil {
		return nil, appErr
	}

	op, payload, appErr := s.createSoulPublishRootOperation(ctx.Context(), contractAddr, txTo, root, rootHex, blockRef, len(reps), snapKey, proofsKey, manifestKey, models.SoulOperationKindPublishReputationRoot, "soul.reputation.publish", strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, now)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, publishRootResponse{
		Operation:   *op,
		SafeTx:      payload,
		Root:        rootHex,
		BlockRef:    blockRef,
		Count:       len(reps),
		SnapshotKey: snapKey,
		ProofsKey:   proofsKey,
		ManifestKey: manifestKey,
	})
}

func (s *Server) handleSoulPublishValidationRoot(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulPublishPrereqs(ctx); appErr != nil {
		return nil, appErr
	}

	contractAddr, txTo, appErr := s.soulValidationAttestationContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	reps, blockRef, appErr := s.loadSortedSoulReputationsForActiveAgents(ctx.Context())
	if appErr != nil {
		return nil, appErr
	}

	leaves := buildValidationLeavesForReputations(reps)
	root, rootHex, snapKey, proofsKey, manifestKey, now, appErr := s.buildAndPersistSoulValidationRootArtifacts(ctx.Context(), leaves, blockRef)
	if appErr != nil {
		return nil, appErr
	}

	op, payload, appErr := s.createSoulPublishRootOperation(ctx.Context(), contractAddr, txTo, root, rootHex, blockRef, len(leaves), snapKey, proofsKey, manifestKey, models.SoulOperationKindPublishValidationRoot, "soul.validation.publish", strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, now)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, publishRootResponse{
		Operation:   *op,
		SafeTx:      payload,
		Root:        rootHex,
		BlockRef:    blockRef,
		Count:       len(leaves),
		SnapshotKey: snapKey,
		ProofsKey:   proofsKey,
		ManifestKey: manifestKey,
	})
}

func (s *Server) requireSoulActiveAgents(ctx context.Context) ([]*models.SoulAgentIdentity, *apptheory.AppError) {
	active, err := s.listSoulActiveAgentIdentities(ctx)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list agents"}
	}
	if len(active) == 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "no active agents"}
	}
	return active, nil
}

func (s *Server) loadSoulReputationsForAgentIdentities(ctx context.Context, active []*models.SoulAgentIdentity) ([]models.SoulAgentReputation, *apptheory.AppError) {
	reps := make([]models.SoulAgentReputation, 0, len(active))
	for _, id := range active {
		if id == nil {
			continue
		}
		agentID := strings.ToLower(strings.TrimSpace(id.AgentID))
		if agentID == "" {
			continue
		}
		rep, repErr := s.getSoulAgentReputation(ctx, agentID)
		if theoryErrors.IsNotFound(repErr) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "missing reputation for agent " + agentID}
		}
		if repErr != nil || rep == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read reputation"}
		}
		reps = append(reps, *rep)
	}

	sort.Slice(reps, func(i, j int) bool { return strings.TrimSpace(reps[i].AgentID) < strings.TrimSpace(reps[j].AgentID) })
	return reps, nil
}

func (s *Server) loadSortedSoulReputationsForActiveAgents(ctx context.Context) ([]models.SoulAgentReputation, int64, *apptheory.AppError) {
	active, appErr := s.requireSoulActiveAgents(ctx)
	if appErr != nil {
		return nil, 0, appErr
	}

	reps, appErr := s.loadSoulReputationsForAgentIdentities(ctx, active)
	if appErr != nil {
		return nil, 0, appErr
	}

	blockRef, appErr := requireUniformSoulReputationBlockRef(reps)
	if appErr != nil {
		return nil, 0, appErr
	}
	return reps, blockRef, nil
}

func buildValidationLeavesForReputations(reps []models.SoulAgentReputation) []validationRootLeaf {
	leaves := make([]validationRootLeaf, 0, len(reps))
	for _, rep := range reps {
		leaves = append(leaves, validationRootLeaf{
			Version:           "1",
			AgentID:           strings.ToLower(strings.TrimSpace(rep.AgentID)),
			BlockRef:          rep.BlockRef,
			Validation:        rep.Validation,
			ValidationsPassed: rep.ValidationsPassed,
			UpdatedAt:         rep.UpdatedAt,
		})
	}
	return leaves
}

func marshalJSON(v any, msg string) ([]byte, *apptheory.AppError) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: msg}
	}
	return body, nil
}

func (s *Server) persistSoulMerkleRootPack(ctx context.Context, prefix string, now time.Time, rootHex string, blockRef int64, count int, snapBody []byte, proofsBody []byte) (snapKey string, proofsKey string, manifestKey string, appErr *apptheory.AppError) {
	snapKey = prefix + "snapshot.json"
	proofsKey = prefix + "proofs.json"

	manifest := buildMerkleManifest(now, rootHex, blockRef, count, map[string][]byte{
		snapKey:   snapBody,
		proofsKey: proofsBody,
	})
	manifestBody, appErr := marshalJSON(manifest, "failed to encode manifest")
	if appErr != nil {
		return "", "", "", appErr
	}
	manifestKey = prefix + "manifest.json"

	if putErr := s.soulPacks.PutObject(ctx, snapKey, snapBody, "application/json", "no-store"); putErr != nil {
		return "", "", "", &apptheory.AppError{Code: "app.internal", Message: "failed to persist snapshot"}
	}
	if putErr := s.soulPacks.PutObject(ctx, proofsKey, proofsBody, "application/json", "no-store"); putErr != nil {
		return "", "", "", &apptheory.AppError{Code: "app.internal", Message: "failed to persist proofs"}
	}
	if putErr := s.soulPacks.PutObject(ctx, manifestKey, manifestBody, "application/json", "no-store"); putErr != nil {
		return "", "", "", &apptheory.AppError{Code: "app.internal", Message: "failed to persist manifest"}
	}

	return snapKey, proofsKey, manifestKey, nil
}

type soulMerkleProofBuilder func(leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error)

type soulMerkleSnapshotBuilder func(rootHex string, now time.Time, leafCodec string, treeCodec string) any

func (s *Server) buildAndPersistSoulRootArtifacts(
	ctx context.Context,
	kind string,
	leafCodec string,
	treeCodec string,
	blockRef int64,
	count int,
	buildProofs soulMerkleProofBuilder,
	buildSnapshot soulMerkleSnapshotBuilder,
) (root common.Hash, rootHex string, snapKey string, proofsKey string, manifestKey string, now time.Time, appErr *apptheory.AppError) {
	leafHashes, proofs, root, err := buildProofs(leafCodec, treeCodec)
	if err != nil {
		return common.Hash{}, "", "", "", "", time.Time{}, &apptheory.AppError{Code: "app.internal", Message: "failed to build merkle tree"}
	}

	rootHex = strings.ToLower(root.Hex())
	prefix := fmt.Sprintf("registry/v1/%s/roots/%s/", strings.TrimSpace(kind), rootHex)
	now = time.Now().UTC()

	snapBody, appErr := marshalJSON(buildSnapshot(rootHex, now, leafCodec, treeCodec), "failed to encode snapshot")
	if appErr != nil {
		return common.Hash{}, "", "", "", "", time.Time{}, appErr
	}

	proofsBody, appErr := marshalJSON(map[string]any{
		"version":    "1",
		"root":       rootHex,
		"block_ref":  blockRef,
		"count":      count,
		"leaf_codec": leafCodec,
		"tree_codec": treeCodec,
		"proofs":     proofs,
		"leaves":     leafHashes,
	}, "failed to encode proofs")
	if appErr != nil {
		return common.Hash{}, "", "", "", "", time.Time{}, appErr
	}

	snapKey, proofsKey, manifestKey, appErr = s.persistSoulMerkleRootPack(ctx, prefix, now, rootHex, blockRef, count, snapBody, proofsBody)
	if appErr != nil {
		return common.Hash{}, "", "", "", "", time.Time{}, appErr
	}
	return root, rootHex, snapKey, proofsKey, manifestKey, now, nil
}

func (s *Server) buildAndPersistSoulReputationRootArtifacts(ctx context.Context, reps []models.SoulAgentReputation, blockRef int64) (root common.Hash, rootHex string, snapKey string, proofsKey string, manifestKey string, now time.Time, appErr *apptheory.AppError) {
	leafCodec := "keccak256(jcs(json(models.SoulAgentReputation)))"
	treeCodec := "keccak256(left||right), duplicate last"

	count := len(reps)
	return s.buildAndPersistSoulRootArtifacts(ctx, "reputation", leafCodec, treeCodec, blockRef, count, func(leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
		return buildMerkleProofsForReputations(reps, leafCodec, treeCodec)
	}, func(rootHex string, now time.Time, leafCodec string, treeCodec string) any {
		return reputationRootSnapshot{
			Version:     "1",
			Kind:        "reputation",
			Root:        rootHex,
			BlockRef:    blockRef,
			Count:       count,
			ComputedAt:  now,
			LeafCodec:   leafCodec,
			TreeCodec:   treeCodec,
			Reputations: reps,
		}
	})
}

func (s *Server) buildAndPersistSoulValidationRootArtifacts(ctx context.Context, leaves []validationRootLeaf, blockRef int64) (root common.Hash, rootHex string, snapKey string, proofsKey string, manifestKey string, now time.Time, appErr *apptheory.AppError) {
	leafCodec := "keccak256(jcs(json(validationRootLeaf)))"
	treeCodec := "keccak256(left||right), duplicate last"

	count := len(leaves)
	return s.buildAndPersistSoulRootArtifacts(ctx, "validation", leafCodec, treeCodec, blockRef, count, func(leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
		return buildMerkleProofsForValidationLeaves(leaves, leafCodec, treeCodec)
	}, func(rootHex string, now time.Time, leafCodec string, treeCodec string) any {
		return validationRootSnapshot{
			Version:    "1",
			Kind:       "validation",
			Root:       rootHex,
			BlockRef:   blockRef,
			Count:      count,
			ComputedAt: now,
			LeafCodec:  leafCodec,
			TreeCodec:  treeCodec,
			Leaves:     leaves,
		}
	})
}

func (s *Server) createSoulPublishRootOperation(ctx context.Context, contractAddr common.Address, txTo string, root common.Hash, rootHex string, blockRef int64, count int, snapKey string, proofsKey string, manifestKey string, kind string, auditAction string, actor string, requestID string, now time.Time) (*models.SoulOperation, *safeTxPayload, *apptheory.AppError) {
	if blockRef < 0 {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid block_ref"}
	}

	data, err := soulattestations.EncodePublishRootCall(root, blockRef, count)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode publishRoot"}
	}

	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, nil, appErr
	}

	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       "0",
		Data:        hexutil.Encode(data),
	}
	payloadJSON, appErr := marshalJSON(payload, "failed to encode safe tx")
	if appErr != nil {
		return nil, nil, appErr
	}

	opID := soulPublishRootOpID(kind, s.cfg.SoulChainID, txTo, rootHex, blockRef, count)
	opMetaJSON, appErr := marshalJSON(map[string]any{
		"root":         rootHex,
		"block_ref":    blockRef,
		"count":        count,
		"contract":     strings.ToLower(contractAddr.Hex()),
		"snapshot_key": snapKey,
		"proofs_key":   proofsKey,
		"manifest_key": manifestKey,
	}, "failed to encode operation metadata")
	if appErr != nil {
		return nil, nil, appErr
	}

	op := &models.SoulOperation{
		OperationID:     opID,
		Kind:            kind,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: strings.TrimSpace(string(payloadJSON)),
		SnapshotJSON:    strings.TrimSpace(string(opMetaJSON)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			existing, getErr := s.getSoulOperation(ctx, opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(actor),
		Action:    strings.TrimSpace(auditAction),
		Target:    fmt.Sprintf("soul_operation:%s", opID),
		RequestID: strings.TrimSpace(requestID),
		CreatedAt: now,
	}
	s.tryWriteAuditLogWithContext(ctx, audit)

	return op, payload, nil
}

func (s *Server) soulReputationAttestationContractAddress() (common.Address, string, *apptheory.AppError) {
	contractAddrRaw := strings.TrimSpace(s.cfg.SoulReputationAttestationContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return common.Address{}, "", &apptheory.AppError{Code: "app.conflict", Message: "reputation attestation is not configured"}
	}
	contractAddr := common.HexToAddress(contractAddrRaw)
	return contractAddr, strings.ToLower(contractAddr.Hex()), nil
}

func (s *Server) soulValidationAttestationContractAddress() (common.Address, string, *apptheory.AppError) {
	contractAddrRaw := strings.TrimSpace(s.cfg.SoulValidationAttestationContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return common.Address{}, "", &apptheory.AppError{Code: "app.conflict", Message: "validation attestation is not configured"}
	}
	contractAddr := common.HexToAddress(contractAddrRaw)
	return contractAddr, strings.ToLower(contractAddr.Hex()), nil
}

func (s *Server) listSoulActiveAgentIdentities(ctx context.Context) ([]*models.SoulAgentIdentity, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not configured")
	}
	var items []*models.SoulAgentIdentity
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("SK", "=", "IDENTITY").
		All(&items); err != nil {
		return nil, err
	}
	out := make([]*models.SoulAgentIdentity, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		if strings.TrimSpace(it.Status) != models.SoulAgentStatusActive {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func requireUniformSoulReputationBlockRef(reps []models.SoulAgentReputation) (int64, *apptheory.AppError) {
	blockRef := int64(0)
	for _, rep := range reps {
		if rep.BlockRef <= 0 {
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "missing block_ref for snapshot"}
		}
		if blockRef == 0 {
			blockRef = rep.BlockRef
			continue
		}
		if rep.BlockRef != blockRef {
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "reputation block_ref mismatch"}
		}
	}
	if blockRef <= 0 {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "missing block_ref for snapshot"}
	}
	return blockRef, nil
}

func buildMerkleProofs[T any](
	items []T,
	leafCodec string,
	treeCodec string,
	agentID func(T) string,
	blockRef func(T) int64,
) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
	leaves := make([]common.Hash, 0, len(items))
	leafOut := make([]map[string]any, 0, len(items))

	for _, item := range items {
		canon, err := canonicalJSON(item)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		h := crypto.Keccak256Hash(canon)
		leaves = append(leaves, h)
		leafOut = append(leafOut, map[string]any{"agent_id": agentID(item), "leaf_hash": strings.ToLower(h.Hex())})
	}

	tree, err := merkle.Build(leaves)
	if err != nil {
		return nil, nil, common.Hash{}, err
	}
	root := tree.Root()

	proofs := make([]merkleProofEntry, 0, len(leaves))
	for i, item := range items {
		p, err := tree.Proof(i)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		proofHex := make([]string, 0, len(p))
		for _, h := range p {
			proofHex = append(proofHex, strings.ToLower(h.Hex()))
		}
		proofs = append(proofs, merkleProofEntry{
			AgentID:   agentID(item),
			Index:     i,
			LeafHash:  strings.ToLower(leaves[i].Hex()),
			Proof:     proofHex,
			Root:      strings.ToLower(root.Hex()),
			BlockRef:  blockRef(item),
			LeafCodec: leafCodec,
			TreeCodec: treeCodec,
		})
	}

	return leafOut, proofs, root, nil
}

func buildMerkleProofsForReputations(reps []models.SoulAgentReputation, leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
	return buildMerkleProofs(reps, leafCodec, treeCodec, func(rep models.SoulAgentReputation) string { return rep.AgentID }, func(rep models.SoulAgentReputation) int64 { return rep.BlockRef })
}

func buildMerkleProofsForValidationLeaves(leavesIn []validationRootLeaf, leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
	return buildMerkleProofs(leavesIn, leafCodec, treeCodec, func(leaf validationRootLeaf) string { return leaf.AgentID }, func(leaf validationRootLeaf) int64 { return leaf.BlockRef })
}

func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsoncanonicalizer.Transform(raw)
}

type merkleManifest struct {
	Version   string            `json:"version"`
	Root      string            `json:"root"`
	BlockRef  int64             `json:"block_ref"`
	Count     int               `json:"count"`
	CreatedAt time.Time         `json:"created_at"`
	Files     []merkleFileEntry `json:"files"`
}

type merkleFileEntry struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
}

func buildMerkleManifest(now time.Time, root string, blockRef int64, count int, bodies map[string][]byte) merkleManifest {
	files := make([]merkleFileEntry, 0, len(bodies))
	for key, body := range bodies {
		sum := sha256.Sum256(body)
		files = append(files, merkleFileEntry{Key: key, SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Key < files[j].Key })
	return merkleManifest{
		Version:   "1",
		Root:      strings.ToLower(strings.TrimSpace(root)),
		BlockRef:  blockRef,
		Count:     count,
		CreatedAt: now.UTC(),
		Files:     files,
	}
}

func soulPublishRootOpID(kind string, chainID int64, txTo string, rootHex string, blockRef int64, count int) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	txTo = strings.ToLower(strings.TrimSpace(txTo))
	rootHex = strings.ToLower(strings.TrimSpace(rootHex))

	var sb strings.Builder
	sb.WriteString(kind)
	sb.WriteString("|")
	sb.WriteString(strconv.FormatInt(chainID, 10))
	sb.WriteString("|")
	sb.WriteString(txTo)
	sb.WriteString("|")
	sb.WriteString(rootHex)
	sb.WriteString("|")
	sb.WriteString(strconv.FormatInt(blockRef, 10))
	sb.WriteString("|")
	sb.WriteString(strconv.Itoa(count))

	sum := sha256.Sum256([]byte(sb.String()))
	return "soulop_" + hex.EncodeToString(sum[:16])
}
