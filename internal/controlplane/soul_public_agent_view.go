package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

var soulRendererUpdatedTopic = crypto.Keccak256Hash([]byte("RendererUpdated(uint8,address)"))

var soulAvatarStyleDefaults = []struct {
	StyleID   uint8
	StyleName string
}{
	{StyleID: 0, StyleName: "Ethereal Blob"},
	{StyleID: 1, StyleName: "Sacred Geometry"},
	{StyleID: 2, StyleName: "Sigil"},
}

const (
	soulPublicAvatarCacheMaxEntries = 512
	soulPublicAvatarCacheTTL        = 5 * time.Minute
)

type soulPublicAgentView struct {
	AgentID                          string                   `json:"agent_id,omitempty"`
	Domain                           string                   `json:"domain,omitempty"`
	LocalID                          string                   `json:"local_id,omitempty"`
	ENSName                          string                   `json:"ens_name,omitempty"`
	Wallet                           string                   `json:"wallet,omitempty"`
	AuthorityModel                   string                   `json:"authority_model,omitempty"`
	TokenID                          string                   `json:"token_id,omitempty"`
	MetaURI                          string                   `json:"meta_uri,omitempty"`
	Avatar                           *soulPublicAvatarView    `json:"avatar,omitempty"`
	AnchorAssurance                  *soulAnchorAssuranceView `json:"anchor_assurance,omitempty"`
	Capabilities                     []string                 `json:"capabilities,omitempty"`
	PrincipalAddress                 string                   `json:"principal_address,omitempty"`
	PrincipalSignature               string                   `json:"principal_signature,omitempty"`
	PrincipalDeclaration             string                   `json:"principal_declaration,omitempty"`
	PrincipalDeclaredAt              string                   `json:"principal_declared_at,omitempty"`
	SelfDescriptionVersion           int                      `json:"self_description_version,omitempty"`
	LifecycleStatus                  string                   `json:"lifecycle_status,omitempty"`
	LifecycleReason                  string                   `json:"lifecycle_reason,omitempty"`
	SuccessorAgentID                 string                   `json:"successor_agent_id,omitempty"`
	PredecessorAgentID               string                   `json:"predecessor_agent_id,omitempty"`
	Status                           string                   `json:"status,omitempty"`
	MintTxHash                       string                   `json:"mint_tx_hash,omitempty"`
	MintedAt                         *time.Time               `json:"minted_at,omitempty"`
	PolicyVersion                    string                   `json:"policy_version,omitempty"`
	AnchorState                      string                   `json:"anchor_state,omitempty"`
	OperationalBinding               string                   `json:"operational_binding,omitempty"`
	CapabilityPolicyVersion          string                   `json:"capability_policy_version,omitempty"`
	CallerAccessPaymentPolicyVersion string                   `json:"caller_access_payment_policy_version,omitempty"`
	EmailDefaultAllowed              *bool                    `json:"email_default_allowed,omitempty"`
	PhoneEntitlementStatus           string                   `json:"phone_entitlement_status,omitempty"`
	SMSAllowed                       *bool                    `json:"sms_allowed,omitempty"`
	VoiceAllowed                     *bool                    `json:"voice_allowed,omitempty"`
	PublicPaidCallerAccess           string                   `json:"public_paid_caller_access,omitempty"`
	PolicyMigrationState             string                   `json:"policy_migration_state,omitempty"`
	UpdatedAt                        time.Time                `json:"updated_at,omitempty"`
}

type soulPublicAvatarView struct {
	TokenURI               string                      `json:"token_uri,omitempty"`
	Image                  string                      `json:"image,omitempty"`
	CurrentStyleID         *int                        `json:"current_style_id,omitempty"`
	CurrentStyleName       string                      `json:"current_style_name,omitempty"`
	CurrentRendererAddress string                      `json:"current_renderer_address,omitempty"`
	Styles                 []soulPublicAvatarStyleView `json:"styles,omitempty"`
}

type soulPublicAvatarStyleView struct {
	StyleID         int    `json:"style_id"`
	StyleName       string `json:"style_name,omitempty"`
	RendererAddress string `json:"renderer_address,omitempty"`
	Image           string `json:"image,omitempty"`
	Selected        bool   `json:"selected,omitempty"`
}

type soulPublicAvatarCacheEntry struct {
	view      soulPublicAvatarView
	expiresAt time.Time
}

type soulPublicAvatarCache struct {
	mu    sync.Mutex
	items map[string]soulPublicAvatarCacheEntry
}

type soulAvatarTokenMetadata struct {
	Image      string                             `json:"image"`
	Attributes []soulAvatarTokenMetadataAttribute `json:"attributes"`
}

type soulAvatarTokenMetadataAttribute struct {
	TraitType string `json:"trait_type"`
	Value     any    `json:"value"`
}

func (s *Server) buildSoulPublicAgentView(ctx context.Context, identity *models.SoulAgentIdentity) soulPublicAgentView {
	view := soulPublicAgentView{}
	if identity == nil {
		return view
	}

	chainID := int64(0)
	if s != nil {
		chainID = s.cfg.SoulChainID
	}
	view = buildSoulPublicAgentIdentityView(identity, chainID)
	if strings.TrimSpace(identity.LocalID) != "" {
		if ensName, err := s.loadSoulPublicAgentENSName(ctx, identity.AgentID); err == nil {
			view.ENSName = ensName
		}
	}
	if avatar, err := s.loadSoulPublicAgentAvatar(ctx, identity); err == nil {
		view.Avatar = avatar
	} else if err != nil && s != nil && s.cfg.SoulV2StrictIntegrity {
		log.Printf("controlplane: soul_public_avatar_enrichment_failed agent=%s err=%v", strings.TrimSpace(identity.AgentID), err)
	}

	return view
}

func buildSoulPublicAgentIdentityView(identity *models.SoulAgentIdentity, chainID int64) soulPublicAgentView {
	if identity == nil {
		return soulPublicAgentView{}
	}
	anchorAssurance := buildSoulAnchorAssuranceFromIdentity(identity, chainID)
	view := soulPublicAgentView{
		AgentID:                          identity.AgentID,
		Domain:                           identity.Domain,
		LocalID:                          identity.LocalID,
		Wallet:                           identity.Wallet,
		AuthorityModel:                   identity.AuthorityModel,
		TokenID:                          identity.TokenID,
		MetaURI:                          identity.MetaURI,
		AnchorAssurance:                  &anchorAssurance,
		Capabilities:                     append([]string(nil), identity.Capabilities...),
		PrincipalAddress:                 identity.PrincipalAddress,
		PrincipalSignature:               identity.PrincipalSignature,
		PrincipalDeclaration:             identity.PrincipalDeclaration,
		PrincipalDeclaredAt:              identity.PrincipalDeclaredAt,
		SelfDescriptionVersion:           identity.SelfDescriptionVersion,
		LifecycleStatus:                  identity.LifecycleStatus,
		LifecycleReason:                  identity.LifecycleReason,
		SuccessorAgentID:                 identity.SuccessorAgentID,
		PredecessorAgentID:               identity.PredecessorAgentID,
		Status:                           identity.Status,
		MintTxHash:                       identity.MintTxHash,
		MintedAt:                         timePtrIfNonZero(identity.MintedAt),
		PolicyVersion:                    identity.PolicyVersion,
		AnchorState:                      identity.AnchorState,
		OperationalBinding:               identity.OperationalBinding,
		CapabilityPolicyVersion:          identity.CapabilityPolicyVersion,
		CallerAccessPaymentPolicyVersion: identity.CallerAccessPaymentPolicyVersion,
		PhoneEntitlementStatus:           identity.PhoneEntitlementStatus,
		PublicPaidCallerAccess:           identity.PublicPaidCallerAccess,
		PolicyMigrationState:             identity.PolicyMigrationState,
		UpdatedAt:                        identity.UpdatedAt,
	}
	if soulIdentityHasHostedPolicyVocabulary(identity) {
		view.EmailDefaultAllowed = boolPtr(identity.EmailDefaultAllowed)
		view.SMSAllowed = boolPtr(identity.SMSAllowed)
		view.VoiceAllowed = boolPtr(identity.VoiceAllowed)
	}
	return view
}

func soulIdentityHasHostedPolicyVocabulary(identity *models.SoulAgentIdentity) bool {
	if identity == nil {
		return false
	}
	return strings.TrimSpace(identity.PolicyVersion) != "" ||
		strings.TrimSpace(identity.CapabilityPolicyVersion) != "" ||
		strings.TrimSpace(identity.CallerAccessPaymentPolicyVersion) != "" ||
		strings.TrimSpace(identity.PhoneEntitlementStatus) != "" ||
		strings.TrimSpace(identity.PublicPaidCallerAccess) != "" ||
		strings.TrimSpace(identity.PolicyMigrationState) != ""
}

func (s *Server) loadSoulPublicAgentENSName(ctx context.Context, agentIDHex string) (string, error) {
	ens, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, agentIDHex, "CHANNEL#ens")
	if theoryErrors.IsNotFound(err) || ens == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	ensName := strings.TrimSpace(ens.Identifier)
	if soul.IsLegacyBareManagedENSName(ensName) {
		return "", nil
	}
	return ensName, nil
}

func (s *Server) loadSoulPublicAgentAvatar(ctx context.Context, identity *models.SoulAgentIdentity) (*soulPublicAvatarView, error) {
	if s == nil || identity == nil || s.dialEVM == nil || !s.cfg.SoulPublicOnChainAvatarEnabled {
		return nil, nil
	}
	rpcURL := strings.TrimSpace(s.cfg.SoulRPCURL)
	if rpcURL == "" {
		return nil, nil
	}
	contractAddrRaw := strings.TrimSpace(s.cfg.SoulRegistryContractAddress)
	if !common.IsHexAddress(contractAddrRaw) {
		return nil, nil
	}
	agentIDHex := strings.TrimSpace(identity.AgentID)
	if agentIDHex == "" {
		return nil, nil
	}

	tokenID, ok := new(big.Int).SetString(strings.TrimPrefix(strings.ToLower(agentIDHex), "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid token id for agent %s", agentIDHex)
	}

	now := time.Now().UTC()
	if s.soulAvatarCache != nil {
		if cached, ok := s.soulAvatarCache.get(agentIDHex, now); ok {
			return cached, nil
		}
	}

	client, err := s.dialEVM(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	contractAddr := common.HexToAddress(contractAddrRaw)
	view, err := loadSoulPublicAvatarTokenViewFromChain(ctx, client, contractAddr, tokenID)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil
	}
	if s.soulAvatarCache != nil {
		s.soulAvatarCache.put(agentIDHex, view, now.Add(soulPublicAvatarCacheTTL), now)
	}
	return view, nil
}

func (c *soulPublicAvatarCache) get(key string, now time.Time) (*soulPublicAvatarView, bool) {
	if c == nil {
		return nil, false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		return nil, false
	}
	entry, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		delete(c.items, key)
		return nil, false
	}
	return cloneSoulPublicAvatarView(&entry.view), true
}

func (c *soulPublicAvatarCache) put(key string, view *soulPublicAvatarView, expiresAt time.Time, now time.Time) {
	if c == nil || view == nil || soulPublicAvatarViewEmpty(view) {
		return
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]soulPublicAvatarCacheEntry{}
	}
	for k, entry := range c.items {
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			delete(c.items, k)
		}
	}
	if len(c.items) >= soulPublicAvatarCacheMaxEntries {
		return
	}
	c.items[key] = soulPublicAvatarCacheEntry{view: *cloneSoulPublicAvatarView(view), expiresAt: expiresAt}
}

func cloneSoulPublicAvatarView(view *soulPublicAvatarView) *soulPublicAvatarView {
	if view == nil {
		return nil
	}
	out := *view
	if view.CurrentStyleID != nil {
		id := *view.CurrentStyleID
		out.CurrentStyleID = &id
	}
	if view.Styles != nil {
		out.Styles = append([]soulPublicAvatarStyleView(nil), view.Styles...)
	}
	return &out
}

func loadSoulPublicAvatarTokenViewFromChain(ctx context.Context, client ethRPCClient, contractAddr common.Address, tokenID *big.Int) (*soulPublicAvatarView, error) {
	if client == nil || tokenID == nil {
		return nil, nil
	}

	tokenURI, metadata, err := loadSoulAvatarTokenMetadata(ctx, client, contractAddr, tokenID)
	if err != nil {
		return nil, err
	}
	view := newSoulPublicAvatarView(tokenURI, metadata)
	if soulPublicAvatarViewEmpty(view) {
		return nil, nil
	}
	return view, nil
}

func loadSoulPublicAvatarViewFromChain(ctx context.Context, client ethRPCClient, contractAddr common.Address, tokenID *big.Int) (*soulPublicAvatarView, error) {
	if client == nil || tokenID == nil {
		return nil, nil
	}

	tokenURI, metadata, err := loadSoulAvatarTokenMetadata(ctx, client, contractAddr, tokenID)
	if err != nil {
		return nil, err
	}
	renderers, err := loadSoulAvatarRenderers(ctx, client, contractAddr)
	if err != nil {
		return nil, err
	}

	view := newSoulPublicAvatarView(tokenURI, metadata)
	view.Styles = loadSoulPublicAvatarStyles(ctx, client, tokenID, renderers, view)

	if soulPublicAvatarViewEmpty(view) {
		return nil, nil
	}
	return view, nil
}

func loadSoulAvatarTokenMetadata(ctx context.Context, client ethRPCClient, contractAddr common.Address, tokenID *big.Int) (string, *soulAvatarTokenMetadata, error) {
	callData, err := soul.EncodeTokenURICall(tokenID)
	if err != nil {
		return "", nil, err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &contractAddr, Data: callData}, nil)
	if err != nil {
		return "", nil, err
	}
	tokenURI, err := soul.DecodeTokenURIResult(ret)
	if err != nil {
		return "", nil, err
	}
	metadata, err := decodeSoulAvatarTokenMetadata(tokenURI)
	if err != nil {
		return tokenURI, nil, nil
	}
	return tokenURI, metadata, nil
}

func loadSoulAvatarRenderers(ctx context.Context, client ethRPCClient, contractAddr common.Address) (map[uint8]common.Address, error) {
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{contractAddr},
		Topics:    [][]common.Hash{{soulRendererUpdatedTopic}},
	})
	if err != nil {
		return nil, err
	}

	renderers := make(map[uint8]common.Address, len(soulAvatarStyleDefaults))
	for _, entry := range logs {
		styleID, renderer, ok := decodeSoulRendererUpdatedLog(entry)
		if !ok {
			continue
		}
		renderers[styleID] = renderer
	}
	return renderers, nil
}

func decodeSoulRendererUpdatedLog(entry types.Log) (uint8, common.Address, bool) {
	if len(entry.Topics) < 2 || entry.Topics[0] != soulRendererUpdatedTopic || len(entry.Data) < 32 {
		return 0, common.Address{}, false
	}
	styleValue := entry.Topics[1].Big()
	if !styleValue.IsUint64() {
		return 0, common.Address{}, false
	}
	rawStyleID := styleValue.Uint64()
	if rawStyleID > 255 {
		return 0, common.Address{}, false
	}
	styleID := uint8(rawStyleID)
	renderer := common.BytesToAddress(entry.Data[len(entry.Data)-20:])
	return styleID, renderer, true
}

func loadRendererStyleName(ctx context.Context, client ethRPCClient, rendererAddr common.Address) (string, error) {
	callData, err := soul.EncodeRendererStyleNameCall()
	if err != nil {
		return "", err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &rendererAddr, Data: callData}, nil)
	if err != nil {
		return "", err
	}
	return soul.DecodeRendererStyleNameResult(ret)
}

func loadRendererAvatarImage(ctx context.Context, client ethRPCClient, rendererAddr common.Address, tokenID *big.Int) (string, error) {
	callData, err := soul.EncodeRendererRenderAvatarCall(tokenID)
	if err != nil {
		return "", err
	}
	ret, err := client.CallContract(ctx, ethereum.CallMsg{To: &rendererAddr, Data: callData}, nil)
	if err != nil {
		return "", err
	}
	svg, err := soul.DecodeRendererRenderAvatarResult(ret)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(svg) == "" {
		return "", nil
	}
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

func decodeSoulAvatarTokenMetadata(tokenURI string) (*soulAvatarTokenMetadata, error) {
	body, contentType, err := decodeDataURI(tokenURI)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(contentType, "application/json") {
		return nil, fmt.Errorf("unsupported content type %q", contentType)
	}
	var metadata soulAvatarTokenMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func decodeDataURI(raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return nil, "", fmt.Errorf("not a data uri")
	}
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return nil, "", fmt.Errorf("invalid data uri")
	}
	meta := strings.TrimPrefix(parts[0], "data:")
	payload := parts[1]
	segments := strings.Split(meta, ";")
	contentType := strings.TrimSpace(segments[0])
	isBase64 := false
	for _, segment := range segments[1:] {
		if strings.EqualFold(strings.TrimSpace(segment), "base64") {
			isBase64 = true
		}
	}
	if isBase64 {
		body, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", err
		}
		return body, contentType, nil
	}
	body, err := url.PathUnescape(payload)
	if err != nil {
		return nil, "", err
	}
	return []byte(body), contentType, nil
}

func soulAvatarMetadataStyleName(metadata *soulAvatarTokenMetadata) string {
	if metadata == nil {
		return ""
	}
	for _, attr := range metadata.Attributes {
		if !strings.EqualFold(strings.TrimSpace(attr.TraitType), "Style") {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(attr.Value))
		if value == "" || strings.EqualFold(value, "<nil>") {
			return ""
		}
		return value
	}
	return ""
}

func currentStyleMatches(currentStyleName string, currentImage string, item soulPublicAvatarStyleView) bool {
	if currentStyleName != "" && strings.EqualFold(strings.TrimSpace(currentStyleName), strings.TrimSpace(item.StyleName)) {
		return true
	}
	if currentImage != "" && strings.TrimSpace(currentImage) == strings.TrimSpace(item.Image) {
		return true
	}
	return false
}

func newSoulPublicAvatarView(tokenURI string, metadata *soulAvatarTokenMetadata) *soulPublicAvatarView {
	view := &soulPublicAvatarView{TokenURI: tokenURI}
	if metadata == nil {
		return view
	}
	view.Image = strings.TrimSpace(metadata.Image)
	view.CurrentStyleName = soulAvatarMetadataStyleName(metadata)
	return view
}

func loadSoulPublicAvatarStyles(ctx context.Context, client ethRPCClient, tokenID *big.Int, renderers map[uint8]common.Address, view *soulPublicAvatarView) []soulPublicAvatarStyleView {
	styles := make([]soulPublicAvatarStyleView, 0, len(soulAvatarStyleDefaults))
	for _, def := range soulAvatarStyleDefaults {
		item := buildSoulPublicAvatarStyleView(ctx, client, tokenID, def.StyleID, def.StyleName, renderers)
		selectCurrentSoulAvatarStyle(view, &item)
		styles = append(styles, item)
	}
	return styles
}

func buildSoulPublicAvatarStyleView(ctx context.Context, client ethRPCClient, tokenID *big.Int, styleID uint8, defaultName string, renderers map[uint8]common.Address) soulPublicAvatarStyleView {
	item := soulPublicAvatarStyleView{
		StyleID:   int(styleID),
		StyleName: defaultName,
	}
	rendererAddr, ok := renderers[styleID]
	if !ok || rendererAddr == (common.Address{}) {
		return item
	}
	item.RendererAddress = strings.ToLower(rendererAddr.Hex())

	if styleName, err := loadRendererStyleName(ctx, client, rendererAddr); err == nil && strings.TrimSpace(styleName) != "" {
		item.StyleName = strings.TrimSpace(styleName)
	}
	if image, err := loadRendererAvatarImage(ctx, client, rendererAddr, tokenID); err == nil {
		item.Image = image
	}
	return item
}

func selectCurrentSoulAvatarStyle(view *soulPublicAvatarView, item *soulPublicAvatarStyleView) {
	if view == nil || item == nil || !currentStyleMatches(view.CurrentStyleName, view.Image, *item) {
		return
	}
	item.Selected = true
	currentStyleID := item.StyleID
	view.CurrentStyleID = &currentStyleID
	view.CurrentStyleName = item.StyleName
	view.CurrentRendererAddress = item.RendererAddress
	if strings.TrimSpace(view.Image) == "" {
		view.Image = item.Image
	}
}

func soulPublicAvatarViewEmpty(view *soulPublicAvatarView) bool {
	if view == nil {
		return true
	}
	return view.CurrentStyleID == nil &&
		strings.TrimSpace(view.CurrentStyleName) == "" &&
		strings.TrimSpace(view.Image) == "" &&
		strings.TrimSpace(view.TokenURI) == ""
}
