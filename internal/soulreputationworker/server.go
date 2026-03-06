package soulreputationworker

import (
	"context"
	"crypto/sha256"
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

	"github.com/equaltoai/lesser-host/internal/attestations"
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
	attest  *attestations.KMSService
}

// NewServer constructs a soul reputation worker Server.
func NewServer(cfg config.Config, st *store.Store, packs soulPackStore) *Server {
	return &Server{
		cfg:     cfg,
		store:   st,
		packs:   packs,
		dialTip: dialTipLogClient,
		now:     time.Now,
		attest:  attestations.NewKMSService(cfg.AttestationSigningKeyID, cfg.AttestationPublicKeyIDs),
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

type reputationSnapshotSignaturePayload struct {
	Version        string    `json:"version"`
	SnapshotKey    string    `json:"snapshot_key"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	ChainID        int64     `json:"chain_id"`
	BlockRef       uint64    `json:"block_ref"`
	ComputedAt     time.Time `json:"computed_at"`
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

	sigKey := ""
	if s.attest != nil && s.attest.Enabled() {
		sum := sha256.Sum256(body)
		payloadBytes, err := json.Marshal(reputationSnapshotSignaturePayload{
			Version:        "1",
			SnapshotKey:    key,
			SnapshotSHA256: hex.EncodeToString(sum[:]),
			ChainID:        s.cfg.TipChainID,
			BlockRef:       blockRef,
			ComputedAt:     now,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal snapshot signature payload: %w", err)
		}

		jws, _, err := s.attest.SignPayloadJWS(ctx.Context(), payloadBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to sign snapshot signature payload: %w", err)
		}

		sigKey = reputationSnapshotSignatureS3Key(key)
		if err := s.packs.PutObject(ctx.Context(), sigKey, []byte(jws), "application/jose", "no-store"); err != nil {
			return nil, fmt.Errorf("failed to write snapshot signature: %w", err)
		}
	}

	return map[string]any{
		"block_ref":            blockRef,
		"from_block":           fromBlock,
		"to_block":             blockRef,
		"agents_considered":    len(identities),
		"agents_updated":       updated,
		"agents_suspended":     skippedSuspended,
		"snapshot_key":         key,
		"snapshot_sig_key":     sigKey,
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
			Economic:      s.cfg.SoulReputationWeightEconomic,
			Social:        s.cfg.SoulReputationWeightSocial,
			Validation:    s.cfg.SoulReputationWeightValidation,
			Trust:         s.cfg.SoulReputationWeightTrust,
			Integrity:     s.cfg.SoulReputationWeightIntegrity,
			Communication: s.cfg.SoulReputationWeightCommunication,
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

		integritySignals, err := s.computeIntegritySignals(ctx, agentID)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("failed to compute integrity signals for %s: %w", agentID, err)
		}

		commSignals, err := s.computeCommunicationSignals(ctx, agentID, now)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("failed to compute communication signals for %s: %w", agentID, err)
		}

		signals := soulreputation.SignalCounts{
			TipsReceived:         tipCounts[agentID],
			Interactions:         0,
			ValidationsPassed:    validationsPassed,
			Endorsements:         integritySignals.endorsements,
			Flags:                0,
			DelegationsCompleted: integritySignals.delegationsCompleted,
			BoundaryViolations:   integritySignals.boundaryViolations,
			FailureRecoveries:    integritySignals.failureRecoveries,

			EmailsSent:                      commSignals.emailsSent,
			EmailsReceived:                  commSignals.emailsReceived,
			SMSSent:                         commSignals.smsSent,
			SMSReceived:                     commSignals.smsReceived,
			CallsMade:                       commSignals.callsMade,
			CallsReceived:                   commSignals.callsReceived,
			CommunicationBoundaryViolations: commSignals.boundaryViolations,
			SpamReports:                     commSignals.spamReports,
			ResponseRate:                    commSignals.responseRate,
			AvgResponseTimeMinutes:          commSignals.avgResponseTimeMinutes,
		}

		scores := soulreputation.SignalScores{
			Social:        0,
			Validation:    validationScore,
			Trust:         0,
			Integrity:     integritySignals.score,
			Communication: commSignals.score,
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

func reputationSnapshotSignatureS3Key(snapshotKey string) string {
	snapshotKey = strings.TrimSpace(snapshotKey)
	if snapshotKey == "" {
		return ""
	}
	return snapshotKey + ".sig.jws"
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

type communicationResult struct {
	score float64

	emailsSent     int64
	emailsReceived int64
	smsSent        int64
	smsReceived    int64
	callsMade      int64
	callsReceived  int64

	boundaryViolations int64
	spamReports        int64

	responseRate           float64
	avgResponseTimeMinutes float64
}

func (s *Server) computeCommunicationSignals(ctx context.Context, agentID string, now time.Time) (communicationResult, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return communicationResult{}, errors.New("store not initialized")
	}

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return communicationResult{}, errors.New("agent id is required")
	}

	cutoff := now.UTC().Add(-30 * 24 * time.Hour)
	items, err := s.listRecentCommunicationActivities(ctx, agentID)
	if err != nil {
		return communicationResult{}, err
	}

	result, inboundAt := summarizeInboundCommunication(items, cutoff)
	result, responded, responseDurations := summarizeOutboundCommunication(items, cutoff, result, inboundAt)
	return finalizeCommunicationResult(result, responded, responseDurations), nil
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
func (s *Server) computeIntegritySignals(ctx context.Context, agentID string) (integrityResult, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return integrityResult{}, errors.New("store not initialized")
	}

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return integrityResult{}, errors.New("agent id is required")
	}

	rels, err := s.listSoulRelationships(ctx, agentID)
	if err != nil {
		return integrityResult{}, err
	}
	delegationsTotal, delegationsCompleted, delegationQualitySum, endorsers := summarizeRelationshipIntegrity(agentID, rels)

	failures, err := s.listSoulAgentFailures(ctx, agentID)
	if err != nil {
		return integrityResult{}, err
	}
	totalFailures, recoveredFailures, boundaryViolations := summarizeFailureIntegrity(failures)

	commActs, err := s.listRecentCommunicationActivities(ctx, agentID)
	if err != nil {
		return integrityResult{}, err
	}
	boundaryViolations += countCommunicationBoundaryViolations(commActs)

	boundariesDeclared, err := s.countDeclaredBoundaries(ctx, agentID)
	if err != nil {
		return integrityResult{}, err
	}
	if err := s.addLegacyEndorsers(ctx, agentID, endorsers); err != nil {
		return integrityResult{}, err
	}

	return finalizeIntegrityResult(boundariesDeclared, delegationsTotal, delegationsCompleted, delegationQualitySum, totalFailures, recoveredFailures, boundaryViolations, endorsers), nil
}

func (s *Server) listRecentCommunicationActivities(ctx context.Context, agentID string) ([]*models.SoulAgentCommActivity, error) {
	var items []*models.SoulAgentCommActivity
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentCommActivity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "COMM#").
		OrderBy("SK", "DESC").
		Limit(1000).
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func summarizeInboundCommunication(items []*models.SoulAgentCommActivity, cutoff time.Time) (communicationResult, map[string]time.Time) {
	result := communicationResult{}
	inboundAt := map[string]time.Time{}
	for _, it := range items {
		if !isInboundReceiveActivity(it, cutoff) {
			continue
		}
		incrementInboundCommunication(&result, it.ChannelType)
		recordInboundTimestamp(inboundAt, strings.TrimSpace(it.MessageID), it.Timestamp)
	}
	return result, inboundAt
}

func summarizeOutboundCommunication(items []*models.SoulAgentCommActivity, cutoff time.Time, result communicationResult, inboundAt map[string]time.Time) (communicationResult, map[string]struct{}, []time.Duration) {
	responded := map[string]struct{}{}
	responseDurations := make([]time.Duration, 0, 32)
	for _, it := range items {
		if !isOutboundSendActivity(it, cutoff) {
			continue
		}
		incrementOutboundCommunication(&result, it.ChannelType)
		if strings.ToLower(strings.TrimSpace(it.BoundaryCheck)) == models.SoulCommBoundaryCheckViolated {
			result.boundaryViolations++
		}
		if duration, ok := responseDurationForActivity(it, inboundAt, responded); ok {
			responseDurations = append(responseDurations, duration)
		}
	}
	return result, responded, responseDurations
}

func finalizeCommunicationResult(result communicationResult, responded map[string]struct{}, responseDurations []time.Duration) communicationResult {
	totalInbound := result.emailsReceived + result.smsReceived + result.callsReceived
	if totalInbound > 0 {
		result.responseRate = float64(len(responded)) / float64(totalInbound)
	}
	result.avgResponseTimeMinutes = averageResponseMinutes(responseDurations)
	result.score = scoreCommunicationResult(result, totalInbound)
	result.spamReports = 0
	return result
}

func isInboundReceiveActivity(it *models.SoulAgentCommActivity, cutoff time.Time) bool {
	if it == nil || it.Timestamp.Before(cutoff) {
		return false
	}
	return strings.ToLower(strings.TrimSpace(it.Direction)) == models.SoulCommDirectionInbound &&
		strings.ToLower(strings.TrimSpace(it.Action)) == "receive"
}

func isOutboundSendActivity(it *models.SoulAgentCommActivity, cutoff time.Time) bool {
	if it == nil || it.Timestamp.Before(cutoff) {
		return false
	}
	return strings.ToLower(strings.TrimSpace(it.Direction)) == models.SoulCommDirectionOutbound &&
		strings.ToLower(strings.TrimSpace(it.Action)) == "send"
}

func incrementInboundCommunication(result *communicationResult, channel string) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "email":
		result.emailsReceived++
	case "sms":
		result.smsReceived++
	case "voice":
		result.callsReceived++
	}
}

func incrementOutboundCommunication(result *communicationResult, channel string) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "email":
		result.emailsSent++
	case "sms":
		result.smsSent++
	case "voice":
		result.callsMade++
	}
}

func recordInboundTimestamp(inboundAt map[string]time.Time, messageID string, ts time.Time) {
	if messageID == "" {
		return
	}
	if existing, ok := inboundAt[messageID]; !ok || ts.Before(existing) {
		inboundAt[messageID] = ts
	}
}

func responseDurationForActivity(it *models.SoulAgentCommActivity, inboundAt map[string]time.Time, responded map[string]struct{}) (time.Duration, bool) {
	replyTo := strings.TrimSpace(it.InReplyTo)
	if replyTo == "" {
		return 0, false
	}
	start, ok := inboundAt[replyTo]
	if !ok || !it.Timestamp.After(start) {
		return 0, false
	}
	if _, seen := responded[replyTo]; seen {
		return 0, false
	}
	responded[replyTo] = struct{}{}
	return it.Timestamp.Sub(start), true
}

func averageResponseMinutes(durations []time.Duration) float64 {
	if len(durations) == 0 {
		return 0
	}
	sum := time.Duration(0)
	for _, d := range durations {
		sum += d
	}
	return sum.Minutes() / float64(len(durations))
}

func scoreCommunicationResult(result communicationResult, totalInbound int64) float64 {
	score := 0.5
	if totalInbound > 0 {
		score += 0.4 * clamp01(result.responseRate)
		timeScore := 1.0
		if result.avgResponseTimeMinutes > 0 {
			timeScore = math.Exp(-result.avgResponseTimeMinutes / 60.0)
		}
		score += 0.3 * clamp01(timeScore)
	}
	score -= 0.1 * float64(result.boundaryViolations)
	return clamp01(score)
}

func (s *Server) listSoulRelationships(ctx context.Context, agentID string) ([]*models.SoulAgentRelationship, error) {
	var rels []*models.SoulAgentRelationship
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentRelationship{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "RELATIONSHIP#").
		All(&rels); err != nil {
		return nil, err
	}
	return rels, nil
}

func summarizeRelationshipIntegrity(agentID string, rels []*models.SoulAgentRelationship) (int64, int64, float64, map[string]struct{}) {
	var delegationsTotal int64
	var delegationsCompleted int64
	var delegationQualitySum float64
	endorsers := map[string]struct{}{}
	for _, r := range rels {
		if shouldSkipRelationshipIntegrity(agentID, r) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(r.Type)) {
		case models.SoulRelationshipTypeDelegation:
			delegationsTotal++
			if quality, ok := completedDelegationQuality(r); ok {
				delegationsCompleted++
				delegationQualitySum += quality
			}
		case models.SoulRelationshipTypeEndorsement:
			recordEndorser(endorsers, agentID, r.FromAgentID)
		}
	}
	return delegationsTotal, delegationsCompleted, delegationQualitySum, endorsers
}

func shouldSkipRelationshipIntegrity(agentID string, rel *models.SoulAgentRelationship) bool {
	return rel == nil || strings.ToLower(strings.TrimSpace(rel.FromAgentID)) == agentID
}

func completedDelegationQuality(rel *models.SoulAgentRelationship) (float64, bool) {
	outcome, qualityScore, hasQuality := extractRelationshipOutcomeAndQuality(rel)
	if !isDelegationCompletedOutcome(outcome) {
		return 0, false
	}
	if hasQuality {
		return clamp01(qualityScore), true
	}
	return 1.0, true
}

func recordEndorser(endorsers map[string]struct{}, agentID string, fromAgentID string) {
	from := strings.ToLower(strings.TrimSpace(fromAgentID))
	if from == "" || from == agentID {
		return
	}
	endorsers[from] = struct{}{}
}

func (s *Server) listSoulAgentFailures(ctx context.Context, agentID string) ([]*models.SoulAgentFailure, error) {
	var failures []*models.SoulAgentFailure
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentFailure{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "FAILURE#").
		All(&failures); err != nil {
		return nil, err
	}
	return failures, nil
}

func summarizeFailureIntegrity(failures []*models.SoulAgentFailure) (int64, int64, int64) {
	var totalFailures int64
	var recoveredFailures int64
	var boundaryViolations int64
	for _, f := range failures {
		if f == nil {
			continue
		}
		totalFailures++
		if strings.ToLower(f.Status) == "recovered" {
			recoveredFailures++
		}
		if isBoundaryViolationFailureType(f.FailureType) {
			boundaryViolations++
		}
	}
	return totalFailures, recoveredFailures, boundaryViolations
}

func countCommunicationBoundaryViolations(commActs []*models.SoulAgentCommActivity) int64 {
	var violations int64
	for _, act := range commActs {
		if act == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(act.Direction)) != models.SoulCommDirectionOutbound {
			continue
		}
		if strings.ToLower(strings.TrimSpace(act.BoundaryCheck)) == models.SoulCommBoundaryCheckViolated {
			violations++
		}
	}
	return violations
}

func (s *Server) countDeclaredBoundaries(ctx context.Context, agentID string) (int64, error) {
	var boundaries []*models.SoulAgentBoundary
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentBoundary{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "BOUNDARY#").
		All(&boundaries); err != nil {
		return 0, err
	}
	var declared int64
	for _, b := range boundaries {
		if b != nil {
			declared++
		}
	}
	return declared, nil
}

func (s *Server) addLegacyEndorsers(ctx context.Context, agentID string, endorsers map[string]struct{}) error {
	var v1Endorsements []*models.SoulAgentPeerEndorsement
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentPeerEndorsement{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "ENDORSEMENT#").
		All(&v1Endorsements); err != nil {
		return err
	}
	for _, e := range v1Endorsements {
		if e != nil {
			recordEndorser(endorsers, agentID, e.EndorserAgentID)
		}
	}
	return nil
}

func finalizeIntegrityResult(boundariesDeclared int64, delegationsTotal int64, delegationsCompleted int64, delegationQualitySum float64, totalFailures int64, recoveredFailures int64, boundaryViolations int64, endorsers map[string]struct{}) integrityResult {
	score := 0.5
	if boundariesDeclared > 0 {
		score += 0.3
	}
	score += 0.2 * clamp01(delegationOutcomeQuality(delegationsTotal, delegationQualitySum))
	if totalFailures > 0 {
		recoveryRatio := float64(recoveredFailures) / float64(totalFailures)
		score -= 0.2 * (1 - clamp01(recoveryRatio))
		score += 0.1 * clamp01(recoveryRatio)
	}
	score -= 0.15 * float64(boundaryViolations)
	return integrityResult{
		score:                clamp01(score),
		endorsements:         int64(len(endorsers)),
		delegationsCompleted: delegationsCompleted,
		boundaryViolations:   boundaryViolations,
		failureRecoveries:    recoveredFailures,
	}
}

func delegationOutcomeQuality(delegationsTotal int64, delegationQualitySum float64) float64 {
	if delegationsTotal <= 0 {
		return 0
	}
	return delegationQualitySum / float64(delegationsTotal)
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
	return ft == "boundary_violation"
}

func isDelegationCompletedOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "completed", "complete", "succeeded", "success":
		return true
	default:
		return false
	}
}

func extractRelationshipOutcomeAndQuality(rel *models.SoulAgentRelationship) (outcome string, qualityScore float64, hasQuality bool) {
	if rel == nil {
		return "", 0, false
	}

	// Dual-read during migration: prefer typed context map, fallback to legacy JSON string.
	m := rel.ContextV2
	if m == nil {
		legacy := strings.TrimSpace(rel.ContextJSON)
		if legacy != "" {
			_ = json.Unmarshal([]byte(legacy), &m)
		}
	}
	return extractRelationshipOutcomeAndQualityFromMap(m)
}

func extractRelationshipOutcomeAndQualityFromMap(m map[string]any) (outcome string, qualityScore float64, hasQuality bool) {
	if m == nil {
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
		"AgentID",
		"BlockRef",
		"Composite",
		"Economic",
		"Social",
		"Validation",
		"Trust",
		"Integrity",
		"Communication",
		"TipsReceived",
		"Interactions",
		"ValidationsPassed",
		"Endorsements",
		"Flags",
		"DelegationsCompleted",
		"BoundaryViolations",
		"FailureRecoveries",
		"EmailsSent",
		"EmailsReceived",
		"SMSSent",
		"SMSReceived",
		"CallsMade",
		"CallsReceived",
		"CommunicationBoundaryViolations",
		"SpamReports",
		"ResponseRate",
		"AvgResponseTimeMinutes",
		"UpdatedAt",
	}

	err := s.store.DB.WithContext(ctx).
		Model(rep).
		WithConditionExpression("attribute_not_exists(blockRef) OR blockRef <= :newBlockRef", map[string]any{
			"newBlockRef": rep.BlockRef,
		}).
		Update(fields...)
	if err == nil || theoryErrors.IsConditionFailed(err) {
		return nil
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
