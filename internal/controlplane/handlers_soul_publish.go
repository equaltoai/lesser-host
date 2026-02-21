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

func (s *Server) handleSoulPublishReputationRoot(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul pack bucket not configured"}
	}

	contractAddr, txTo, appErr := s.soulReputationAttestationContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	active, err := s.listSoulActiveAgentIdentities(ctx.Context())
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list agents"}
	}
	if len(active) == 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "no active agents"}
	}

	reps := make([]models.SoulAgentReputation, 0, len(active))
	for _, id := range active {
		if id == nil {
			continue
		}
		agentID := strings.ToLower(strings.TrimSpace(id.AgentID))
		if agentID == "" {
			continue
		}
		rep, repErr := s.getSoulAgentReputation(ctx.Context(), agentID)
		if theoryErrors.IsNotFound(repErr) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "missing reputation for agent " + agentID}
		}
		if repErr != nil || rep == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read reputation"}
		}
		reps = append(reps, *rep)
	}

	sort.Slice(reps, func(i, j int) bool { return strings.TrimSpace(reps[i].AgentID) < strings.TrimSpace(reps[j].AgentID) })

	blockRef, appErr := requireUniformSoulReputationBlockRef(reps)
	if appErr != nil {
		return nil, appErr
	}

	leafCodec := "keccak256(jcs(json(models.SoulAgentReputation)))"
	treeCodec := "keccak256(left||right), duplicate last"

	leafHashes, proofs, root, err := buildMerkleProofsForReputations(reps, leafCodec, treeCodec)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to build merkle tree"}
	}

	rootHex := strings.ToLower(root.Hex())
	prefix := fmt.Sprintf("registry/v1/reputation/roots/%s/", rootHex)
	now := time.Now().UTC()

	snap := reputationRootSnapshot{
		Version:     "1",
		Kind:        "reputation",
		Root:        rootHex,
		BlockRef:    blockRef,
		Count:       len(reps),
		ComputedAt:  now,
		LeafCodec:   leafCodec,
		TreeCodec:   treeCodec,
		Reputations: reps,
	}
	snapBody, _ := json.Marshal(snap)
	snapKey := prefix + "snapshot.json"

	proofsBody, _ := json.Marshal(map[string]any{
		"version":    "1",
		"root":       rootHex,
		"block_ref":  blockRef,
		"count":      len(reps),
		"leaf_codec": leafCodec,
		"tree_codec": treeCodec,
		"proofs":     proofs,
		"leaves":     leafHashes,
	})
	proofsKey := prefix + "proofs.json"

	manifest := buildMerkleManifest(now, rootHex, blockRef, len(reps), map[string][]byte{
		snapKey:   snapBody,
		proofsKey: proofsBody,
	})
	manifestBody, _ := json.Marshal(manifest)
	manifestKey := prefix + "manifest.json"

	if err := s.soulPacks.PutObject(ctx.Context(), snapKey, snapBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist snapshot"}
	}
	if err := s.soulPacks.PutObject(ctx.Context(), proofsKey, proofsBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist proofs"}
	}
	if err := s.soulPacks.PutObject(ctx.Context(), manifestKey, manifestBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist manifest"}
	}

	data, err := soulattestations.EncodePublishRootCall(root, uint64(blockRef), uint64(len(reps)))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode publishRoot"}
	}

	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, appErr
	}

	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       "0",
		Data:        hexutil.Encode(data),
	}
	payloadJSON, _ := json.Marshal(payload)

	opID := soulPublishRootOpID(models.SoulOperationKindPublishReputationRoot, s.cfg.SoulChainID, txTo, rootHex, blockRef, len(reps))
	opMetaJSON, _ := json.Marshal(map[string]any{
		"root":         rootHex,
		"block_ref":    blockRef,
		"count":        len(reps),
		"contract":     strings.ToLower(contractAddr.Hex()),
		"snapshot_key": snapKey,
		"proofs_key":   proofsKey,
		"manifest_key": manifestKey,
	})

	op := &models.SoulOperation{
		OperationID:     opID,
		Kind:            models.SoulOperationKindPublishReputationRoot,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: strings.TrimSpace(string(payloadJSON)),
		SnapshotJSON:    strings.TrimSpace(string(opMetaJSON)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			existing, getErr := s.getSoulOperation(ctx.Context(), opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.reputation.publish",
		Target:    fmt.Sprintf("soul_operation:%s", opID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}).Create()

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
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul pack bucket not configured"}
	}

	contractAddr, txTo, appErr := s.soulValidationAttestationContractAddress()
	if appErr != nil {
		return nil, appErr
	}

	active, err := s.listSoulActiveAgentIdentities(ctx.Context())
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list agents"}
	}
	if len(active) == 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "no active agents"}
	}

	leaves := make([]validationRootLeaf, 0, len(active))
	blockRef := int64(0)

	for _, id := range active {
		if id == nil {
			continue
		}
		agentID := strings.ToLower(strings.TrimSpace(id.AgentID))
		if agentID == "" {
			continue
		}
		rep, repErr := s.getSoulAgentReputation(ctx.Context(), agentID)
		if theoryErrors.IsNotFound(repErr) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "missing reputation for agent " + agentID}
		}
		if repErr != nil || rep == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to read reputation"}
		}
		if blockRef == 0 {
			blockRef = rep.BlockRef
		} else if rep.BlockRef != blockRef {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "reputation block_ref mismatch"}
		}

		leaves = append(leaves, validationRootLeaf{
			Version:           "1",
			AgentID:           agentID,
			BlockRef:          rep.BlockRef,
			Validation:        rep.Validation,
			ValidationsPassed: rep.ValidationsPassed,
			UpdatedAt:         rep.UpdatedAt,
		})
	}

	if blockRef <= 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "missing block_ref for snapshot"}
	}

	sort.Slice(leaves, func(i, j int) bool {
		return strings.TrimSpace(leaves[i].AgentID) < strings.TrimSpace(leaves[j].AgentID)
	})

	leafCodec := "keccak256(jcs(json(validationRootLeaf)))"
	treeCodec := "keccak256(left||right), duplicate last"

	leafHashes, proofs, root, err := buildMerkleProofsForValidationLeaves(leaves, leafCodec, treeCodec)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to build merkle tree"}
	}

	rootHex := strings.ToLower(root.Hex())
	prefix := fmt.Sprintf("registry/v1/validation/roots/%s/", rootHex)
	now := time.Now().UTC()

	snap := validationRootSnapshot{
		Version:    "1",
		Kind:       "validation",
		Root:       rootHex,
		BlockRef:   blockRef,
		Count:      len(leaves),
		ComputedAt: now,
		LeafCodec:  leafCodec,
		TreeCodec:  treeCodec,
		Leaves:     leaves,
	}
	snapBody, _ := json.Marshal(snap)
	snapKey := prefix + "snapshot.json"

	proofsBody, _ := json.Marshal(map[string]any{
		"version":    "1",
		"root":       rootHex,
		"block_ref":  blockRef,
		"count":      len(leaves),
		"leaf_codec": leafCodec,
		"tree_codec": treeCodec,
		"proofs":     proofs,
		"leaves":     leafHashes,
	})
	proofsKey := prefix + "proofs.json"

	manifest := buildMerkleManifest(now, rootHex, blockRef, len(leaves), map[string][]byte{
		snapKey:   snapBody,
		proofsKey: proofsBody,
	})
	manifestBody, _ := json.Marshal(manifest)
	manifestKey := prefix + "manifest.json"

	if err := s.soulPacks.PutObject(ctx.Context(), snapKey, snapBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist snapshot"}
	}
	if err := s.soulPacks.PutObject(ctx.Context(), proofsKey, proofsBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist proofs"}
	}
	if err := s.soulPacks.PutObject(ctx.Context(), manifestKey, manifestBody, "application/json", "no-store"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist manifest"}
	}

	data, err := soulattestations.EncodePublishRootCall(root, uint64(blockRef), uint64(len(leaves)))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode publishRoot"}
	}

	safeAddr, appErr := s.soulRegistrySafeAddress()
	if appErr != nil {
		return nil, appErr
	}

	payload := &safeTxPayload{
		SafeAddress: safeAddr,
		To:          txTo,
		Value:       "0",
		Data:        hexutil.Encode(data),
	}
	payloadJSON, _ := json.Marshal(payload)

	opID := soulPublishRootOpID(models.SoulOperationKindPublishValidationRoot, s.cfg.SoulChainID, txTo, rootHex, blockRef, len(leaves))
	opMetaJSON, _ := json.Marshal(map[string]any{
		"root":         rootHex,
		"block_ref":    blockRef,
		"count":        len(leaves),
		"contract":     strings.ToLower(contractAddr.Hex()),
		"snapshot_key": snapKey,
		"proofs_key":   proofsKey,
		"manifest_key": manifestKey,
	})

	op := &models.SoulOperation{
		OperationID:     opID,
		Kind:            models.SoulOperationKindPublishValidationRoot,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: strings.TrimSpace(string(payloadJSON)),
		SnapshotJSON:    strings.TrimSpace(string(opMetaJSON)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = op.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(op).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			existing, getErr := s.getSoulOperation(ctx.Context(), opID)
			if getErr == nil && existing != nil {
				op = existing
			}
		} else {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create operation"}
		}
	}

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.validation.publish",
		Target:    fmt.Sprintf("soul_operation:%s", opID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}).Create()

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

func buildMerkleProofsForReputations(reps []models.SoulAgentReputation, leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
	type leaf struct {
		AgentID string `json:"agent_id"`
		Hash    string `json:"leaf_hash"`
	}

	leaves := make([]common.Hash, 0, len(reps))
	leafOut := make([]map[string]any, 0, len(reps))

	for _, rep := range reps {
		canon, err := canonicalJSON(rep)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		h := crypto.Keccak256Hash(canon)
		leaves = append(leaves, h)
		leafOut = append(leafOut, map[string]any{"agent_id": rep.AgentID, "leaf_hash": strings.ToLower(h.Hex())})
	}

	tree, err := merkle.Build(leaves)
	if err != nil {
		return nil, nil, common.Hash{}, err
	}
	root := tree.Root()

	proofs := make([]merkleProofEntry, 0, len(leaves))
	for i, rep := range reps {
		p, err := tree.Proof(i)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		proofHex := make([]string, 0, len(p))
		for _, h := range p {
			proofHex = append(proofHex, strings.ToLower(h.Hex()))
		}
		proofs = append(proofs, merkleProofEntry{
			AgentID:   rep.AgentID,
			Index:     i,
			LeafHash:  strings.ToLower(leaves[i].Hex()),
			Proof:     proofHex,
			Root:      strings.ToLower(root.Hex()),
			BlockRef:  rep.BlockRef,
			LeafCodec: leafCodec,
			TreeCodec: treeCodec,
		})
	}

	return leafOut, proofs, root, nil
}

func buildMerkleProofsForValidationLeaves(leavesIn []validationRootLeaf, leafCodec string, treeCodec string) ([]map[string]any, []merkleProofEntry, common.Hash, error) {
	leaves := make([]common.Hash, 0, len(leavesIn))
	leafOut := make([]map[string]any, 0, len(leavesIn))

	for _, leaf := range leavesIn {
		canon, err := canonicalJSON(leaf)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		h := crypto.Keccak256Hash(canon)
		leaves = append(leaves, h)
		leafOut = append(leafOut, map[string]any{"agent_id": leaf.AgentID, "leaf_hash": strings.ToLower(h.Hex())})
	}

	tree, err := merkle.Build(leaves)
	if err != nil {
		return nil, nil, common.Hash{}, err
	}
	root := tree.Root()

	proofs := make([]merkleProofEntry, 0, len(leaves))
	for i, leaf := range leavesIn {
		p, err := tree.Proof(i)
		if err != nil {
			return nil, nil, common.Hash{}, err
		}
		proofHex := make([]string, 0, len(p))
		for _, h := range p {
			proofHex = append(proofHex, strings.ToLower(h.Hex()))
		}
		proofs = append(proofs, merkleProofEntry{
			AgentID:   leaf.AgentID,
			Index:     i,
			LeafHash:  strings.ToLower(leaves[i].Hex()),
			Proof:     proofHex,
			Root:      strings.ToLower(root.Hex()),
			BlockRef:  leaf.BlockRef,
			LeafCodec: leafCodec,
			TreeCodec: treeCodec,
		})
	}

	return leafOut, proofs, root, nil
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
