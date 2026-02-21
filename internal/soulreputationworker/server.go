package soulreputationworker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soulreputation"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulPackStore interface {
	PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error
}

type tipLogClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	Close()
}

type tipLogDialer func(ctx context.Context, rpcURL string) (tipLogClient, error)

// Server computes v0 soul reputation snapshots.
type Server struct {
	cfg     config.Config
	store   *store.Store
	packs   soulPackStore
	dialTip tipLogDialer
	now     func() time.Time
}

// NewServer constructs a soul reputation worker Server.
func NewServer(cfg config.Config, st *store.Store, packs soulPackStore) *Server {
	return &Server{
		cfg:     cfg,
		store:   st,
		packs:   packs,
		dialTip: dialTipLogClient,
		now:     time.Now,
	}
}

// Register registers scheduled events with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	ruleName := fmt.Sprintf("%s-%s-soul-reputation-recompute", s.cfg.AppName, s.cfg.Stage)
	app.EventBridge(apptheory.EventBridgeRule(ruleName), s.handleRecompute)
}

type reputationSnapshot struct {
	Version            string                       `json:"version"`
	ChainID            int64                        `json:"chain_id"`
	TipContractAddress string                       `json:"tip_contract_address"`
	FromBlock          uint64                       `json:"from_block"`
	ToBlock            uint64                       `json:"to_block"`
	ComputedAt         time.Time                    `json:"computed_at"`
	Weights            soulreputation.Weights       `json:"weights"`
	TipScale           float64                      `json:"tip_scale"`
	Reputations        []models.SoulAgentReputation `json:"reputations"`
}

func (s *Server) handleRecompute(ctx *apptheory.EventContext, _ events.EventBridgeEvent) (any, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if s.packs == nil {
		return nil, fmt.Errorf("pack store not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("event context is nil")
	}

	if !s.cfg.SoulEnabled {
		return map[string]any{"skipped": "soul_disabled"}, nil
	}
	if !s.cfg.TipEnabled {
		return map[string]any{"skipped": "tip_disabled"}, nil
	}

	rpcURL := strings.TrimSpace(s.cfg.TipRPCURL)
	if rpcURL == "" {
		return map[string]any{"skipped": "tip_rpc_not_configured"}, nil
	}

	contractRaw := strings.TrimSpace(s.cfg.TipContractAddress)
	if !common.IsHexAddress(contractRaw) {
		return map[string]any{"skipped": "tip_contract_not_configured"}, nil
	}
	contractAddr := common.HexToAddress(contractRaw)

	client, err := s.dialTip(ctx.Context(), rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial tip rpc: %w", err)
	}
	defer client.Close()

	blockRef, err := client.BlockNumber(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to read head block: %w", err)
	}

	fromBlock := s.cfg.SoulReputationTipStartBlock
	if fromBlock > blockRef {
		fromBlock = blockRef
	}

	chunkSize := s.cfg.SoulReputationTipBlockChunkSize
	if chunkSize == 0 {
		chunkSize = 5000
	}

	tipCounts, err := fetchAgentTipCounts(ctx.Context(), client, contractAddr, fromBlock, blockRef, chunkSize)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest tips: %w", err)
	}

	identities, err := s.listAgentIdentities(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to list identities: %w", err)
	}
	sort.Slice(identities, func(i, j int) bool {
		ai := ""
		aj := ""
		if identities[i] != nil {
			ai = strings.TrimSpace(identities[i].AgentID)
		}
		if identities[j] != nil {
			aj = strings.TrimSpace(identities[j].AgentID)
		}
		return ai < aj
	})

	now := s.now().UTC()
	v0cfg := soulreputation.V0Config{
		TipScale: s.cfg.SoulReputationTipScale,
		Weights: soulreputation.Weights{
			Economic:   s.cfg.SoulReputationWeightEconomic,
			Social:     s.cfg.SoulReputationWeightSocial,
			Validation: s.cfg.SoulReputationWeightValidation,
			Trust:      s.cfg.SoulReputationWeightTrust,
		},
	}

	reps := make([]models.SoulAgentReputation, 0, len(identities))
	updated := 0
	skippedSuspended := 0
	totalTipEvents := int64(0)
	for _, n := range tipCounts {
		totalTipEvents += n
	}

	for _, identity := range identities {
		if identity == nil {
			continue
		}

		agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
		if agentID == "" {
			continue
		}

		if strings.TrimSpace(identity.Status) == models.SoulAgentStatusSuspended {
			skippedSuspended++
			continue
		}

		signals := soulreputation.SignalCounts{
			TipsReceived: tipCounts[agentID],
			// Stubs for v0.
			Interactions:      0,
			ValidationsPassed: 0,
			Endorsements:      0,
			Flags:             0,
		}

		rep := soulreputation.ComputeV0(agentID, blockRef, now, v0cfg, signals)
		if putErr := s.putAgentReputation(ctx.Context(), &rep); putErr != nil {
			return nil, fmt.Errorf("failed to persist reputation for %s: %w", agentID, putErr)
		}
		reps = append(reps, rep)
		updated++
	}

	snapshot := reputationSnapshot{
		Version:            "1",
		ChainID:            s.cfg.TipChainID,
		TipContractAddress: strings.ToLower(contractAddr.Hex()),
		FromBlock:          fromBlock,
		ToBlock:            blockRef,
		ComputedAt:         now,
		Weights:            v0cfg.Weights.Normalized(),
		TipScale:           v0cfg.TipScale,
		Reputations:        reps,
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	key := reputationSnapshotS3Key(s.cfg.TipChainID, blockRef)
	if err := s.packs.PutObject(ctx.Context(), key, body, "application/json", "no-store"); err != nil {
		return nil, fmt.Errorf("failed to write snapshot: %w", err)
	}

	return map[string]any{
		"block_ref":            blockRef,
		"from_block":           fromBlock,
		"to_block":             blockRef,
		"agents_considered":    len(identities),
		"agents_updated":       updated,
		"agents_suspended":     skippedSuspended,
		"snapshot_key":         key,
		"tip_agents_with_tips": len(tipCounts),
		"tip_events_total":     totalTipEvents,
	}, nil
}

func reputationSnapshotS3Key(chainID int64, blockRef uint64) string {
	if chainID <= 0 {
		return fmt.Sprintf("registry/v1/reputation/snapshots/block-%d.json", blockRef)
	}
	return fmt.Sprintf("registry/v1/reputation/snapshots/chain-%d/block-%d.json", chainID, blockRef)
}

var agentTipSentTopic0 = crypto.Keccak256Hash([]byte("AgentTipSent(bytes32,uint256,address,address,address,uint256,bytes32)"))

func fetchAgentTipCounts(ctx context.Context, client tipLogClient, contract common.Address, fromBlock uint64, toBlock uint64, chunkSize uint64) (map[string]int64, error) {
	if client == nil {
		return nil, errors.New("evm client is required")
	}

	if toBlock < fromBlock {
		return map[string]int64{}, nil
	}
	if chunkSize == 0 {
		chunkSize = 5000
	}

	counts := map[string]int64{}

	for start := fromBlock; start <= toBlock; start += chunkSize {
		end := start + chunkSize - 1
		if end > toBlock {
			end = toBlock
		}

		q := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Addresses: []common.Address{contract},
			Topics:    [][]common.Hash{{agentTipSentTopic0}},
		}

		logs, err := client.FilterLogs(ctx, q)
		if err != nil {
			return nil, err
		}

		for _, lg := range logs {
			if len(lg.Topics) < 3 {
				continue
			}
			agentID := "0x" + hex.EncodeToString(lg.Topics[2].Bytes())
			counts[agentID]++
		}
	}

	return counts, nil
}

func (s *Server) listAgentIdentities(ctx context.Context) ([]*models.SoulAgentIdentity, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not initialized")
	}

	var items []*models.SoulAgentIdentity
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("SK", "=", "IDENTITY").
		All(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Server) putAgentReputation(ctx context.Context, rep *models.SoulAgentReputation) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return errors.New("store not initialized")
	}
	if rep == nil {
		return errors.New("reputation is nil")
	}

	fields := []string{
		"BlockRef",
		"Composite",
		"Economic",
		"Social",
		"Validation",
		"Trust",
		"TipsReceived",
		"Interactions",
		"ValidationsPassed",
		"Endorsements",
		"Flags",
		"UpdatedAt",
	}

	err := s.store.DB.WithContext(ctx).Model(rep).IfExists().Update(fields...)
	if err == nil {
		return nil
	}
	if theoryErrors.IsNotFound(err) {
		return s.store.DB.WithContext(ctx).Model(rep).IfNotExists().Create()
	}
	return err
}

func dialTipLogClient(ctx context.Context, rpcURL string) (tipLogClient, error) {
	rpcURL = strings.TrimSpace(rpcURL)
	if rpcURL == "" {
		return nil, errors.New("rpc url is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rc, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	return ethclient.NewClient(rc), nil
}
