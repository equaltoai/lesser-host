package soulreputationworker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
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
	"github.com/equaltoai/lesser-host/internal/soulvalidation"
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
	if err := s.requireRecomputePrereqs(ctx); err != nil {
		return nil, err
	}

	rpcURL, contractAddr, skip := s.tipRecomputeConfig()
	if skip != "" {
		return map[string]any{"skipped": skip}, nil
	}

	client, err := s.dialTip(ctx.Context(), rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial tip rpc: %w", err)
	}
	defer client.Close()

	blockRef, err := client.BlockNumber(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to read head block: %w", err)
	}

	fromBlock, chunkSize := s.tipIngestRange(blockRef)
	tipCounts, err := fetchAgentTipCounts(ctx.Context(), client, contractAddr, fromBlock, blockRef, chunkSize)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest tips: %w", err)
	}

	identities, err := s.listAgentIdentities(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("failed to list identities: %w", err)
	}
	sort.Slice(identities, func(i, j int) bool { return soulIdentitySortKey(identities[i]) < soulIdentitySortKey(identities[j]) })

	now := s.now().UTC()
	v0cfg := s.v0Config()

	reps, updated, skippedSuspended, err := s.computeAndPersistReputations(ctx.Context(), identities, blockRef, now, v0cfg, tipCounts)
	if err != nil {
		return nil, err
	}
	totalTipEvents := sumTipEvents(tipCounts)

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

func (s *Server) requireRecomputePrereqs(ctx *apptheory.EventContext) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if s.packs == nil {
		return fmt.Errorf("pack store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}
	return nil
}

func (s *Server) tipRecomputeConfig() (rpcURL string, contractAddr common.Address, skipReason string) {
	if !s.cfg.SoulEnabled {
		return "", common.Address{}, "soul_disabled"
	}
	if !s.cfg.TipEnabled {
		return "", common.Address{}, "tip_disabled"
	}

	rpcURL = strings.TrimSpace(s.cfg.TipRPCURL)
	if rpcURL == "" {
		return "", common.Address{}, "tip_rpc_not_configured"
	}

	contractRaw := strings.TrimSpace(s.cfg.TipContractAddress)
	if !common.IsHexAddress(contractRaw) {
		return "", common.Address{}, "tip_contract_not_configured"
	}

	return rpcURL, common.HexToAddress(contractRaw), ""
}

func (s *Server) tipIngestRange(head uint64) (fromBlock uint64, chunkSize uint64) {
	fromBlock = s.cfg.SoulReputationTipStartBlock
	if fromBlock > head {
		fromBlock = head
	}

	chunkSize = s.cfg.SoulReputationTipBlockChunkSize
	if chunkSize == 0 {
		chunkSize = 5000
	}

	return fromBlock, chunkSize
}

func soulIdentitySortKey(identity *models.SoulAgentIdentity) string {
	if identity == nil {
		return ""
	}
	return strings.TrimSpace(identity.AgentID)
}

func sumTipEvents(tipCounts map[string]int64) int64 {
	total := int64(0)
	for _, n := range tipCounts {
		total += n
	}
	return total
}

func (s *Server) v0Config() soulreputation.V0Config {
	return soulreputation.V0Config{
		TipScale: s.cfg.SoulReputationTipScale,
		Weights: soulreputation.Weights{
			Economic:   s.cfg.SoulReputationWeightEconomic,
			Social:     s.cfg.SoulReputationWeightSocial,
			Validation: s.cfg.SoulReputationWeightValidation,
			Trust:      s.cfg.SoulReputationWeightTrust,
			Integrity:  s.cfg.SoulReputationWeightIntegrity,
		},
	}
}

func (s *Server) computeAndPersistReputations(ctx context.Context, identities []*models.SoulAgentIdentity, blockRef uint64, now time.Time, v0cfg soulreputation.V0Config, tipCounts map[string]int64) ([]models.SoulAgentReputation, int, int, error) {
	reps := make([]models.SoulAgentReputation, 0, len(identities))
	updated := 0
	skippedSuspended := 0

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

		validationScore, validationsPassed, err := s.computeValidationSignals(ctx, agentID, now)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("failed to compute validation signals for %s: %w", agentID, err)
		}

		integritySignals := s.computeIntegritySignals(ctx, agentID)

		signals := soulreputation.SignalCounts{
			TipsReceived:         tipCounts[agentID],
			Interactions:         0,
			ValidationsPassed:    validationsPassed,
			Endorsements:         integritySignals.endorsements,
			Flags:                0,
			DelegationsCompleted: integritySignals.delegationsCompleted,
			BoundaryViolations:   integritySignals.boundaryViolations,
			FailureRecoveries:    integritySignals.failureRecoveries,
		}

		scores := soulreputation.SignalScores{
			Social:     0,
			Validation: validationScore,
			Trust:      0,
			Integrity:  integritySignals.score,
		}

		rep := soulreputation.ComputeV0(agentID, blockRef, now, v0cfg, signals, scores)
		if err := s.putAgentReputation(ctx, &rep); err != nil {
			return nil, 0, 0, fmt.Errorf("failed to persist reputation for %s: %w", agentID, err)
		}

		reps = append(reps, rep)
		updated++
	}

	return reps, updated, skippedSuspended, nil
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

func (s *Server) listAgentValidationRecords(ctx context.Context, agentID string) ([]*models.SoulAgentValidationRecord, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}

	var items []*models.SoulAgentValidationRecord
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentValidationRecord{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "VALIDATION#").
		OrderBy("SK", "ASC").
		All(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Server) computeValidationSignals(ctx context.Context, agentID string, now time.Time) (float64, int64, error) {
	items, err := s.listAgentValidationRecords(ctx, agentID)
	if err != nil {
		return 0, 0, err
	}

	evs := make([]soulvalidation.Event, 0, len(items))
	for _, it := range items {
		if it == nil || it.EvaluatedAt.IsZero() {
			continue
		}
		evs = append(evs, soulvalidation.Event{
			EvaluatedAt: it.EvaluatedAt,
			Result:      it.Result,
			Delta:       it.Score,
		})
	}

	cfg := soulvalidation.Config{
		Epoch:     time.Duration(s.cfg.SoulValidationDecayEpochHours) * time.Hour,
		DecayRate: s.cfg.SoulValidationDecayRate,
	}
	score, passed := soulvalidation.ComputeProgressiveScore(evs, now, cfg)
	return score, passed, nil
}

type integrityResult struct {
	score                float64
	endorsements         int64
	delegationsCompleted int64
	boundaryViolations   int64
	failureRecoveries    int64
}

// computeIntegritySignals counts integrity-related signals for an agent.
// Integrity is based on: boundary violations (negative), failure recoveries (positive),
// and delegation completions (positive).
func (s *Server) computeIntegritySignals(ctx context.Context, agentID string) integrityResult {
	if s == nil || s.store == nil || s.store.DB == nil {
		return integrityResult{score: 0.5}
	}

	agentID = strings.ToLower(strings.TrimSpace(agentID))

	// Relationship signals (delegations + endorsements).
	var rels []*models.SoulAgentRelationship
	_ = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentRelationship{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "RELATIONSHIP#").
		All(&rels)

	var delegationsTotal int64
	var delegationsCompleted int64
	var delegationQualitySum float64
	var endorsements int64

	for _, r := range rels {
		if r == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(r.Type)) {
		case models.SoulRelationshipTypeDelegation:
			delegationsTotal++
			outcome, qualityScore, hasQuality := extractRelationshipOutcomeAndQuality(r.Context)
			if isDelegationCompletedOutcome(outcome) {
				delegationsCompleted++
				quality := 1.0
				if hasQuality {
					quality = clamp01(qualityScore)
				}
				delegationQualitySum += quality
			}
		case models.SoulRelationshipTypeEndorsement:
			endorsements++
		}
	}

	// Count failure records.
	var failures []*models.SoulAgentFailure
	_ = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentFailure{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "FAILURE#").
		All(&failures)
	var totalFailures, recoveredFailures int64
	for _, f := range failures {
		if f == nil {
			continue
		}
		totalFailures++
		if strings.ToLower(f.Status) == "recovered" {
			recoveredFailures++
		}
	}

	var boundaryViolations int64
	for _, f := range failures {
		if f == nil {
			continue
		}
		if isBoundaryViolationFailureType(f.FailureType) {
			boundaryViolations++
		}
	}

	// Determine whether the agent has declared any boundaries.
	var boundaries []*models.SoulAgentBoundary
	_ = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		All(&boundaries)
	boundariesDeclared := int64(0)
	for _, b := range boundaries {
		if b != nil {
			boundariesDeclared++
		}
	}

	// Backward-compat endorsements (v1).
	var v1Endorsements []*models.SoulAgentPeerEndorsement
	_ = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentPeerEndorsement{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "ENDORSEMENT#").
		All(&v1Endorsements)
	for _, e := range v1Endorsements {
		if e != nil {
			endorsements++
		}
	}

	// Compute integrity score (heuristic v2).
	//
	// Spec guidance:
	// - Declared boundaries + adherence score higher than no boundaries.
	// - Failures are informative; recoveries can be positive signals.
	// - Delegation outcomes (completion + quality) are meaningful trust evidence.
	score := 0.5
	if boundariesDeclared > 0 {
		score += 0.3
	}

	delegationOutcomeQuality := 0.0
	if delegationsTotal > 0 {
		delegationOutcomeQuality = delegationQualitySum / float64(delegationsTotal) // failed/unknown outcomes contribute 0
	}
	score += 0.2 * clamp01(delegationOutcomeQuality)

	if totalFailures > 0 {
		recoveryRatio := float64(recoveredFailures) / float64(totalFailures)
		score -= 0.2 * (1 - clamp01(recoveryRatio))
		score += 0.1 * clamp01(recoveryRatio)
	}
	score -= 0.15 * float64(boundaryViolations)
	score = clamp01(score)

	return integrityResult{
		score:                score,
		endorsements:         endorsements,
		delegationsCompleted: delegationsCompleted,
		boundaryViolations:   boundaryViolations,
		failureRecoveries:    recoveredFailures,
	}
}

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return 0
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func isBoundaryViolationFailureType(failureType string) bool {
	ft := strings.ToLower(strings.TrimSpace(failureType))
	if ft == "" {
		return false
	}
	if ft == "boundary_violation" {
		return true
	}
	return strings.Contains(ft, "boundary")
}

func isDelegationCompletedOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "completed", "complete", "succeeded", "success":
		return true
	default:
		return false
	}
}

func extractRelationshipOutcomeAndQuality(contextJSON string) (outcome string, qualityScore float64, hasQuality bool) {
	contextJSON = strings.TrimSpace(contextJSON)
	if contextJSON == "" {
		return "", 0, false
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(contextJSON), &m); err != nil {
		return "", 0, false
	}

	outcome, _ = m["outcome"].(string)
	outcome = strings.ToLower(strings.TrimSpace(outcome))

	raw, ok := m["qualityScore"]
	if !ok {
		raw, ok = m["quality_score"]
	}
	if !ok {
		return outcome, 0, false
	}

	switch v := raw.(type) {
	case float64:
		return outcome, v, true
	case int:
		return outcome, float64(v), true
	case int64:
		return outcome, float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return outcome, 0, false
		}
		return outcome, f, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return outcome, 0, false
		}
		return outcome, f, true
	default:
		return outcome, 0, false
	}
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
		"Integrity",
		"TipsReceived",
		"Interactions",
		"ValidationsPassed",
		"Endorsements",
		"Flags",
		"DelegationsCompleted",
		"BoundaryViolations",
		"FailureRecoveries",
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
