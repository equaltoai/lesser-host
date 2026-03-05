package trust

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type ensGatewayResolveJSON struct {
	Data string `json:"data"`
}

func (s *Server) handleENSGatewayHealth(ctx *apptheory.Context) (*apptheory.Response, error) {
	_ = ctx
	resp, err := apptheory.JSON(http.StatusOK, map[string]any{
		"ok":      true,
		"service": "ens-gateway",
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"no-store"}
	return resp, nil
}

func (s *Server) handleENSGatewayResolve(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	signer, signerErr := s.ensureENSGatewaySigner(ctx.Context())
	if signerErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if signer == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	query := ctx.Request.Query

	targetRaw := strings.TrimSpace(httpx.FirstQueryValue(query, "sender"))
	if targetRaw == "" {
		targetRaw = strings.TrimSpace(httpx.FirstQueryValue(query, "to"))
	}
	if targetRaw == "" {
		targetRaw = strings.TrimSpace(s.cfg.ENSGatewayResolverAddress)
	}
	if targetRaw == "" {
		return nil, apptheory.NewAppTheoryError("ccip.bad_request", "sender is required").WithStatusCode(http.StatusBadRequest)
	}
	if strings.TrimSpace(s.cfg.ENSGatewayResolverAddress) != "" && !strings.EqualFold(targetRaw, s.cfg.ENSGatewayResolverAddress) {
		// Per EIP-3668, return 404 when the sender is not supported.
		return nil, apptheory.NewAppTheoryError("ccip.sender_unsupported", "sender not supported").WithStatusCode(http.StatusNotFound)
	}
	if !common.IsHexAddress(targetRaw) {
		return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid sender").WithStatusCode(http.StatusBadRequest)
	}
	target := common.HexToAddress(targetRaw)

	dataRaw := strings.TrimSpace(httpx.FirstQueryValue(query, "data"))
	if dataRaw == "" {
		return nil, apptheory.NewAppTheoryError("ccip.bad_request", "data is required").WithStatusCode(http.StatusBadRequest)
	}
	nameRaw := strings.TrimSpace(httpx.FirstQueryValue(query, "name"))

	callData, encodedName, innerData, err := parseENSGatewayRequest(nameRaw, dataRaw)
	if err != nil {
		return nil, err
	}

	ensName, err := decodeDNSName(encodedName)
	if err != nil {
		return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid name").WithStatusCode(http.StatusBadRequest)
	}

	node := ensNameHash(ensName)

	result, err := s.answerENSQuery(ctx.Context(), ensName, node, innerData)
	if err != nil {
		return nil, err
	}

	ttlSeconds := int64(300)
	if s.cfg.ENSGatewaySignatureTTLSeconds > 0 {
		ttlSeconds = s.cfg.ENSGatewaySignatureTTLSeconds
	}
	validUntil := uint64(time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second).Unix())

	sigHash := makeENSSignatureHash(target, validUntil, callData, result)
	var digest [32]byte
	copy(digest[:], sigHash)

	sigCompact, err := signer.SignDigest(ctx.Context(), digest)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	responseBytes, err := ensGatewayResponseABI.Pack(result, validUntil, sigCompact)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	out := ensGatewayResolveJSON{Data: "0x" + hex.EncodeToString(responseBytes)}
	resp, err := apptheory.JSON(http.StatusOK, out)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{fmt.Sprintf("public, max-age=%d", ttlSeconds)}
	return resp, nil
}

func (s *Server) answerENSQuery(ctx context.Context, ensName string, node common.Hash, innerData []byte) ([]byte, error) {
	if len(innerData) < 4 {
		return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid inner resolver data").WithStatusCode(http.StatusBadRequest)
	}

	selector := innerData[:4]
	args := innerData[4:]

	switch {
	case bytesEqual(selector, ensAddrSelector):
		decoded, err := ensAddrInputs.Unpack(args)
		if err != nil || len(decoded) != 1 {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid addr query").WithStatusCode(http.StatusBadRequest)
		}
		qNode, ok := decoded[0].([32]byte)
		if !ok || common.BytesToHash(qNode[:]) != node {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "node mismatch").WithStatusCode(http.StatusBadRequest)
		}

		material, ok, err := s.loadENSGatewayMaterial(ctx, ensName)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		if !ok || !common.IsHexAddress(material.Wallet) {
			out, packErr := ensAddrOutputs.Pack(common.Address{})
			if packErr != nil {
				return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
			}
			return out, nil
		}

		out, packErr := ensAddrOutputs.Pack(common.HexToAddress(material.Wallet))
		if packErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		return out, nil

	case bytesEqual(selector, ensAddrCoinSelector):
		decoded, err := ensAddrCoinInputs.Unpack(args)
		if err != nil || len(decoded) != 2 {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid addr query").WithStatusCode(http.StatusBadRequest)
		}
		qNode, ok := decoded[0].([32]byte)
		if !ok || common.BytesToHash(qNode[:]) != node {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "node mismatch").WithStatusCode(http.StatusBadRequest)
		}
		coinType, ok := decoded[1].(*big.Int)
		if !ok || coinType == nil {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid coinType").WithStatusCode(http.StatusBadRequest)
		}

		// ETH = 60 (SLIP-0044). Return empty for other coin types.
		if coinType.Sign() < 0 || coinType.Uint64() != 60 {
			out, packErr := ensBytesOutputs.Pack([]byte(nil))
			if packErr != nil {
				return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
			}
			return out, nil
		}

		material, ok, err := s.loadENSGatewayMaterial(ctx, ensName)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		if !ok || !common.IsHexAddress(material.Wallet) {
			out, packErr := ensBytesOutputs.Pack([]byte(nil))
			if packErr != nil {
				return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
			}
			return out, nil
		}
		addr := common.HexToAddress(material.Wallet)
		out, packErr := ensBytesOutputs.Pack(addr.Bytes())
		if packErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		return out, nil

	case bytesEqual(selector, ensTextSelector):
		decoded, err := ensTextInputs.Unpack(args)
		if err != nil || len(decoded) != 2 {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid text query").WithStatusCode(http.StatusBadRequest)
		}
		qNode, ok := decoded[0].([32]byte)
		if !ok || common.BytesToHash(qNode[:]) != node {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "node mismatch").WithStatusCode(http.StatusBadRequest)
		}
		key, ok := decoded[1].(string)
		if !ok {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid text key").WithStatusCode(http.StatusBadRequest)
		}

		material, ok, err := s.loadENSGatewayMaterial(ctx, ensName)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		val := ""
		if ok {
			val = ensTextValue(material, strings.TrimSpace(key), ctx)
		}
		out, packErr := ensStringOutputs.Pack(val)
		if packErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		return out, nil

	case bytesEqual(selector, ensContenthashSelector):
		decoded, err := ensContenthashInputs.Unpack(args)
		if err != nil || len(decoded) != 1 {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid contenthash query").WithStatusCode(http.StatusBadRequest)
		}
		qNode, ok := decoded[0].([32]byte)
		if !ok || common.BytesToHash(qNode[:]) != node {
			return nil, apptheory.NewAppTheoryError("ccip.bad_request", "node mismatch").WithStatusCode(http.StatusBadRequest)
		}
		out, packErr := ensBytesOutputs.Pack([]byte(nil))
		if packErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		return out, nil

	default:
		return nil, apptheory.NewAppTheoryError("ccip.unsupported", "unsupported query").WithStatusCode(http.StatusBadRequest)
	}
}

func ensTextValue(material ensGatewayMaterial, key string, ctx context.Context) string {
	switch key {
	case "soul.agentId":
		return strings.TrimSpace(material.AgentID)
	case "soul.registration":
		return strings.TrimSpace(material.RegistrationURI)
	case "soul.mcp":
		return strings.TrimSpace(material.MCPEndpoint)
	case "soul.activitypub":
		return strings.TrimSpace(material.ActivityPubURI)
	case "email":
		return strings.TrimSpace(material.Email)
	case "phone":
		return strings.TrimSpace(material.Phone)
	case "description":
		return truncateUTF8(strings.TrimSpace(material.Description), 256)
	case "soul.status":
		return strings.TrimSpace(material.Status)
	case "soul.successor":
		// Best-effort: map successor agentId → ENS name.
		succ := strings.TrimSpace(material.SuccessorAgentID)
		if succ == "" {
			return ""
		}
		if material.SuccessorENSOK {
			return strings.TrimSpace(material.SuccessorENSName)
		}
		_ = ctx
		return ""
	default:
		return ""
	}
}

type ensGatewayMaterial struct {
	ENSName          string
	AgentID          string
	Wallet           string
	Status           string
	SuccessorAgentID string

	RegistrationURI string
	MCPEndpoint     string
	ActivityPubURI  string
	Email           string
	Phone           string
	Description     string

	SuccessorENSName string
	SuccessorENSOK   bool
}

type ensGatewayCacheEntry struct {
	material  ensGatewayMaterial
	ok        bool
	expiresAt time.Time
}

type ensGatewayCache struct {
	mu    sync.RWMutex
	items map[string]ensGatewayCacheEntry
}

func (c *ensGatewayCache) get(key string, now time.Time) (ensGatewayMaterial, bool, bool) {
	if c == nil {
		return ensGatewayMaterial{}, false, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.items == nil {
		return ensGatewayMaterial{}, false, false
	}
	entry, ok := c.items[key]
	if !ok || now.After(entry.expiresAt) {
		return ensGatewayMaterial{}, false, false
	}
	return entry.material, entry.ok, true
}

func (c *ensGatewayCache) put(key string, material ensGatewayMaterial, ok bool, expiresAt time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]ensGatewayCacheEntry{}
	}
	c.items[key] = ensGatewayCacheEntry{material: material, ok: ok, expiresAt: expiresAt}
}

func (s *Server) loadENSGatewayMaterial(ctx context.Context, ensName string) (ensGatewayMaterial, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return ensGatewayMaterial{}, false, fmt.Errorf("store not configured")
	}
	ensName = normalizeENSName(ensName)
	if ensName == "" {
		return ensGatewayMaterial{}, false, nil
	}

	now := time.Now().UTC()
	if s.ensCache != nil {
		if material, ok, hit := s.ensCache.get(ensName, now); hit {
			return material, ok, nil
		}
	}

	key := &models.SoulAgentENSResolution{ENSName: ensName}
	_ = key.UpdateKeys()

	var res models.SoulAgentENSResolution
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", key.PK).
		Where("SK", "=", "RESOLUTION").
		First(&res)
	if theoryErrors.IsNotFound(err) {
		s.cacheENSGatewayMaterial(ensName, ensGatewayMaterial{}, false, now)
		return ensGatewayMaterial{}, false, nil
	}
	if err != nil {
		return ensGatewayMaterial{}, false, err
	}

	agentID := strings.TrimSpace(res.AgentID)
	if agentID == "" {
		s.cacheENSGatewayMaterial(ensName, ensGatewayMaterial{}, false, now)
		return ensGatewayMaterial{}, false, nil
	}

	var identity models.SoulAgentIdentity
	err = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "IDENTITY").
		First(&identity)
	if theoryErrors.IsNotFound(err) {
		s.cacheENSGatewayMaterial(ensName, ensGatewayMaterial{}, false, now)
		return ensGatewayMaterial{}, false, nil
	}
	if err != nil {
		return ensGatewayMaterial{}, false, err
	}

	status := strings.TrimSpace(identity.LifecycleStatus)
	if status == "" {
		status = strings.TrimSpace(identity.Status)
	}
	if status == "" {
		status = strings.TrimSpace(res.Status)
	}

	material := ensGatewayMaterial{
		ENSName:          ensName,
		AgentID:          agentID,
		Wallet:           strings.TrimSpace(identity.Wallet),
		Status:           strings.TrimSpace(status),
		SuccessorAgentID: strings.TrimSpace(identity.SuccessorAgentId),

		RegistrationURI: strings.TrimSpace(res.SoulRegistrationURI),
		MCPEndpoint:     strings.TrimSpace(res.MCPEndpoint),
		ActivityPubURI:  strings.TrimSpace(res.ActivityPubURI),
		Email:           strings.TrimSpace(res.Email),
		Phone:           strings.TrimSpace(res.Phone),
		Description:     strings.TrimSpace(res.Description),
	}

	if strings.TrimSpace(material.SuccessorAgentID) != "" {
		succName, succOK, _ := s.bestEffortSuccessorENSName(ctx, material.SuccessorAgentID)
		material.SuccessorENSName = succName
		material.SuccessorENSOK = succOK
	}

	s.cacheENSGatewayMaterial(ensName, material, true, now)
	return material, true, nil
}

func (s *Server) cacheENSGatewayMaterial(ensName string, material ensGatewayMaterial, ok bool, now time.Time) {
	if s == nil || s.ensCache == nil {
		return
	}
	ttlSeconds := int64(30)
	if s.cfg.ENSGatewaySignatureTTLSeconds > 0 && s.cfg.ENSGatewaySignatureTTLSeconds < 30 {
		ttlSeconds = s.cfg.ENSGatewaySignatureTTLSeconds
	}
	s.ensCache.put(ensName, material, ok, now.Add(time.Duration(ttlSeconds)*time.Second))
}

func (s *Server) bestEffortSuccessorENSName(ctx context.Context, successorAgentID string) (string, bool, error) {
	successorAgentID = strings.ToLower(strings.TrimSpace(successorAgentID))
	if successorAgentID == "" || s == nil || s.store == nil || s.store.DB == nil {
		return "", false, nil
	}

	var ch models.SoulAgentChannel
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentChannel{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", successorAgentID)).
		Where("SK", "=", "CHANNEL#ens").
		First(&ch)
	if theoryErrors.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	name := normalizeENSName(strings.TrimSpace(ch.Identifier))
	if name == "" {
		return "", false, nil
	}
	return name, true, nil
}

func parseENSGatewayRequest(nameParam string, dataParam string) (callData []byte, encodedName []byte, innerData []byte, err error) {
	data, err := hexutil.Decode(dataParam)
	if err != nil || len(data) < 4 {
		return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid data").WithStatusCode(http.StatusBadRequest)
	}

	// Primary mode: EIP-3668 {data} substitution sends callData for resolve(bytes,bytes).
	if bytesEqual(data[:4], ensResolveSelector) {
		decoded, err := ensResolveInputs.Unpack(data[4:])
		if err != nil || len(decoded) != 2 {
			return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid calldata").WithStatusCode(http.StatusBadRequest)
		}
		nameBytes, ok1 := decoded[0].([]byte)
		dataBytes, ok2 := decoded[1].([]byte)
		if !ok1 || !ok2 {
			return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid calldata").WithStatusCode(http.StatusBadRequest)
		}
		return data, nameBytes, dataBytes, nil
	}

	// Compatibility mode: /resolve?name=<ensName|0xDNS>&data=<inner resolver call>.
	nameParam = strings.TrimSpace(nameParam)
	if nameParam == "" {
		return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "name is required when data is not calldata").WithStatusCode(http.StatusBadRequest)
	}

	var nameBytes []byte
	if strings.HasPrefix(strings.ToLower(nameParam), "0x") {
		nameBytes, err = hexutil.Decode(nameParam)
		if err != nil {
			return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid name").WithStatusCode(http.StatusBadRequest)
		}
	} else {
		nameBytes, err = encodeDNSName(nameParam)
		if err != nil {
			return nil, nil, nil, apptheory.NewAppTheoryError("ccip.bad_request", "invalid name").WithStatusCode(http.StatusBadRequest)
		}
	}

	packed, err := ensResolveInputs.Pack(nameBytes, data)
	if err != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	callData = append(append([]byte(nil), ensResolveSelector...), packed...)
	return callData, nameBytes, data, nil
}

func normalizeENSName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, ".")
	return name
}

func decodeDNSName(dns []byte) (string, error) {
	var labels []string
	for i := 0; ; {
		if i >= len(dns) {
			return "", fmt.Errorf("dns name: missing terminator")
		}
		l := int(dns[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 {
			return "", fmt.Errorf("dns name: label too long")
		}
		if i+l > len(dns) {
			return "", fmt.Errorf("dns name: truncated label")
		}
		labels = append(labels, string(dns[i:i+l]))
		i += l
	}
	return normalizeENSName(strings.Join(labels, ".")), nil
}

func encodeDNSName(name string) ([]byte, error) {
	name = normalizeENSName(name)
	if name == "" {
		return []byte{0}, nil
	}

	labels := strings.Split(name, ".")
	out := make([]byte, 0, len(name)+2)
	for _, label := range labels {
		if label == "" {
			return nil, fmt.Errorf("dns name: empty label")
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("dns name: label too long")
		}
		out = append(out, byte(len(label)))
		out = append(out, []byte(label)...)
	}
	out = append(out, 0)
	return out, nil
}

func ensNameHash(name string) common.Hash {
	name = normalizeENSName(name)
	if name == "" {
		return common.Hash{}
	}

	labels := strings.Split(name, ".")
	node := common.Hash{}
	var buf [64]byte
	for i := len(labels) - 1; i >= 0; i-- {
		labelHash := crypto.Keccak256Hash([]byte(labels[i]))
		copy(buf[:32], node[:])
		copy(buf[32:], labelHash[:])
		node = crypto.Keccak256Hash(buf[:])
	}
	return node
}

func makeENSSignatureHash(target common.Address, expires uint64, request []byte, result []byte) []byte {
	reqHash := crypto.Keccak256Hash(request)
	resHash := crypto.Keccak256Hash(result)

	b := make([]byte, 0, 2+20+8+32+32)
	b = append(b, 0x19, 0x00)
	b = append(b, target.Bytes()...)

	var exp [8]byte
	binary.BigEndian.PutUint64(exp[:], expires)
	b = append(b, exp[:]...)

	b = append(b, reqHash.Bytes()...)
	b = append(b, resHash.Bytes()...)
	return crypto.Keccak256(b)
}

func bytesEqual(a []byte, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truncateUTF8(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	// Walk runes until we fit.
	n := 0
	for i := range s {
		if i > max {
			break
		}
		n = i
	}
	if n == 0 {
		return ""
	}
	return s[:n]
}

var (
	ensABIsOnce sync.Once
	ensABIsErr  error

	ensResolveSelector     []byte
	ensAddrSelector        []byte
	ensAddrCoinSelector    []byte
	ensTextSelector        []byte
	ensContenthashSelector []byte
	ensResolveInputs       abi.Arguments
	ensAddrInputs          abi.Arguments
	ensAddrOutputs         abi.Arguments
	ensAddrCoinInputs      abi.Arguments
	ensTextInputs          abi.Arguments
	ensStringOutputs       abi.Arguments
	ensContenthashInputs   abi.Arguments
	ensBytesOutputs        abi.Arguments
	ensGatewayResponseABI  abi.Arguments
)

func initENSABIs() {
	ensABIsOnce.Do(func() {
		var err error

		ensResolveSelector = crypto.Keccak256([]byte("resolve(bytes,bytes)"))[:4]
		ensAddrSelector = crypto.Keccak256([]byte("addr(bytes32)"))[:4]
		ensAddrCoinSelector = crypto.Keccak256([]byte("addr(bytes32,uint256)"))[:4]
		ensTextSelector = crypto.Keccak256([]byte("text(bytes32,string)"))[:4]
		ensContenthashSelector = crypto.Keccak256([]byte("contenthash(bytes32)"))[:4]

		bytesT, err := abi.NewType("bytes", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}
		bytes32T, err := abi.NewType("bytes32", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}
		uint256T, err := abi.NewType("uint256", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}
		uint64T, err := abi.NewType("uint64", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}
		stringT, err := abi.NewType("string", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}
		addressT, err := abi.NewType("address", "", nil)
		if err != nil {
			ensABIsErr = err
			return
		}

		ensResolveInputs = abi.Arguments{{Type: bytesT}, {Type: bytesT}}

		ensAddrInputs = abi.Arguments{{Type: bytes32T}}
		ensAddrOutputs = abi.Arguments{{Type: addressT}}

		ensAddrCoinInputs = abi.Arguments{{Type: bytes32T}, {Type: uint256T}}

		ensTextInputs = abi.Arguments{{Type: bytes32T}, {Type: stringT}}
		ensStringOutputs = abi.Arguments{{Type: stringT}}

		ensContenthashInputs = abi.Arguments{{Type: bytes32T}}
		ensBytesOutputs = abi.Arguments{{Type: bytesT}}

		ensGatewayResponseABI = abi.Arguments{{Type: bytesT}, {Type: uint64T}, {Type: bytesT}}
	})
}

func init() {
	initENSABIs()
}
