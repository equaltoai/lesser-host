package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestSoulPublicAvatarViewHelpers(t *testing.T) {
	t.Parallel()

	styleID := 1
	view := newSoulPublicAvatarView("token-uri", &soulAvatarTokenMetadata{
		Image: " data:image/svg+xml;base64,abc ",
		Attributes: []soulAvatarTokenMetadataAttribute{
			{TraitType: "Style", Value: " Sacred Geometry "},
		},
	})
	if view == nil || view.TokenURI != "token-uri" || view.Image != "data:image/svg+xml;base64,abc" || view.CurrentStyleName != "Sacred Geometry" {
		t.Fatalf("unexpected view: %#v", view)
	}
	if soulPublicAvatarViewEmpty(view) {
		t.Fatalf("expected populated view to be non-empty")
	}

	emptyView := &soulPublicAvatarView{}
	if !soulPublicAvatarViewEmpty(emptyView) {
		t.Fatalf("expected empty view to be empty")
	}

	item := soulPublicAvatarStyleView{
		StyleID:         styleID,
		StyleName:       "Sacred Geometry",
		RendererAddress: "0xabc",
		Image:           "data:image/svg+xml;base64,abc",
	}
	selectCurrentSoulAvatarStyle(view, &item)
	if view.CurrentStyleID == nil || *view.CurrentStyleID != styleID || !item.Selected {
		t.Fatalf("expected style selection to be recorded: view=%#v item=%#v", view, item)
	}
	if !currentStyleMatches("Sacred Geometry", "", item) {
		t.Fatalf("expected name-based style match")
	}
	if !currentStyleMatches("", "data:image/svg+xml;base64,abc", item) {
		t.Fatalf("expected image-based style match")
	}
	if currentStyleMatches("Sigil", "", item) {
		t.Fatalf("did not expect mismatched style name to match")
	}
}

func TestDecodeSoulAvatarMetadataAndDataURIs(t *testing.T) {
	t.Parallel()

	rawJSON, err := json.Marshal(soulAvatarTokenMetadata{
		Image: "data:image/svg+xml;base64,xyz",
		Attributes: []soulAvatarTokenMetadataAttribute{
			{TraitType: "Style", Value: "Sigil"},
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	base64URI := "data:application/json;base64," + base64.StdEncoding.EncodeToString(rawJSON)
	plainURI := "data:text/plain," + url.PathEscape("hello world")

	metadata, err := decodeSoulAvatarTokenMetadata(base64URI)
	if err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata == nil || metadata.Image != "data:image/svg+xml;base64,xyz" || soulAvatarMetadataStyleName(metadata) != "Sigil" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if soulAvatarMetadataStyleName(&soulAvatarTokenMetadata{
		Attributes: []soulAvatarTokenMetadataAttribute{{TraitType: "Style", Value: "   "}},
	}) != "" {
		t.Fatalf("expected blank style metadata to collapse to empty")
	}

	body, contentType, err := decodeDataURI(plainURI)
	if err != nil {
		t.Fatalf("decode plain data uri: %v", err)
	}
	if string(body) != "hello world" || contentType != "text/plain" {
		t.Fatalf("unexpected decoded payload: body=%q contentType=%q", string(body), contentType)
	}
	if _, _, err := decodeDataURI("not-a-data-uri"); err == nil {
		t.Fatalf("expected invalid scheme to fail")
	}
	if _, err := decodeSoulAvatarTokenMetadata("data:text/plain,hello"); err == nil {
		t.Fatalf("expected non-json metadata to fail")
	}
}

func TestDecodeSoulRendererUpdatedLog(t *testing.T) {
	t.Parallel()

	renderer := common.HexToAddress("0x0000000000000000000000000000000000000abc")
	entry := types.Log{
		Topics: []common.Hash{
			soulRendererUpdatedTopic,
			common.BigToHash(big.NewInt(2)),
		},
		Data: common.LeftPadBytes(renderer.Bytes(), 32),
	}
	styleID, decodedRenderer, ok := decodeSoulRendererUpdatedLog(entry)
	if !ok || styleID != 2 || decodedRenderer != renderer {
		t.Fatalf("expected valid renderer log decode, got styleID=%d renderer=%s ok=%v", styleID, decodedRenderer.Hex(), ok)
	}

	overflow := types.Log{
		Topics: []common.Hash{
			soulRendererUpdatedTopic,
			common.BigToHash(big.NewInt(256)),
		},
		Data: common.LeftPadBytes(renderer.Bytes(), 32),
	}
	if _, _, ok := decodeSoulRendererUpdatedLog(overflow); ok {
		t.Fatalf("expected overflow style id to be rejected")
	}
	if _, _, ok := decodeSoulRendererUpdatedLog(types.Log{}); ok {
		t.Fatalf("expected empty log to be rejected")
	}
}

func TestLoadSoulPublicAvatarViewShortCircuit(t *testing.T) {
	t.Parallel()

	view, err := loadSoulPublicAvatarViewFromChain(context.Background(), nil, common.Address{}, big.NewInt(1))
	if err != nil || view != nil {
		t.Fatalf("expected nil client to short-circuit, got view=%#v err=%v", view, err)
	}
	view, err = loadSoulPublicAvatarViewFromChain(context.Background(), &fakeSoulPublicEthClient{}, common.Address{}, nil)
	if err != nil || view != nil {
		t.Fatalf("expected nil token to short-circuit, got view=%#v err=%v", view, err)
	}
}

func TestLoadSoulAvatarRendererHelpers(t *testing.T) {
	t.Parallel()

	tokenID := big.NewInt(7)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000def")
	rendererAddr := common.HexToAddress("0x0000000000000000000000000000000000000abc")

	styleNameCall, err := soul.EncodeRendererStyleNameCall()
	if err != nil {
		t.Fatalf("encode styleName: %v", err)
	}
	renderAvatarCall, err := soul.EncodeRendererRenderAvatarCall(tokenID)
	if err != nil {
		t.Fatalf("encode renderAvatar: %v", err)
	}

	client := &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			switch {
			case msg.To != nil && *msg.To == rendererAddr && bytes.Equal(msg.Data, styleNameCall):
				return packSingleStringResult(t, "Sigil"), nil
			case msg.To != nil && *msg.To == rendererAddr && bytes.Equal(msg.Data, renderAvatarCall):
				return packSingleStringResult(t, "<svg>sigil</svg>"), nil
			default:
				t.Fatalf("unexpected call contract request: %#v", msg)
				return nil, nil
			}
		},
		filterLogs: func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
			return []types.Log{
				{},
				rendererUpdatedLog(2, rendererAddr),
			}, nil
		},
	}

	renderers, err := loadSoulAvatarRenderers(context.Background(), client, contractAddr)
	if err != nil {
		t.Fatalf("load renderers: %v", err)
	}
	if renderers[2] != rendererAddr {
		t.Fatalf("expected renderer map to include style 2, got %#v", renderers)
	}

	styleName, err := loadRendererStyleName(context.Background(), client, rendererAddr)
	if err != nil || styleName != "Sigil" {
		t.Fatalf("expected style name helper to decode, got styleName=%q err=%v", styleName, err)
	}

	image, err := loadRendererAvatarImage(context.Background(), client, rendererAddr, tokenID)
	if err != nil || !strings.HasPrefix(image, "data:image/svg+xml;base64,") {
		t.Fatalf("expected avatar image helper to base64-encode SVG, got image=%q err=%v", image, err)
	}
}

func TestLoadSoulAvatarTokenMetadataFallsBackForNonJSON(t *testing.T) {
	t.Parallel()

	tokenID := big.NewInt(9)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000aaa")
	tokenURICall, err := soul.EncodeTokenURICall(tokenID)
	if err != nil {
		t.Fatalf("encode tokenURI: %v", err)
	}

	client := &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			if msg.To == nil || *msg.To != contractAddr || !bytes.Equal(msg.Data, tokenURICall) {
				t.Fatalf("unexpected tokenURI call: %#v", msg)
			}
			return packSingleStringResult(t, "data:text/plain,hello"), nil
		},
	}

	tokenURI, metadata, err := loadSoulAvatarTokenMetadata(context.Background(), client, contractAddr, tokenID)
	if err != nil {
		t.Fatalf("load token metadata: %v", err)
	}
	if tokenURI != "data:text/plain,hello" || metadata != nil {
		t.Fatalf("expected non-json metadata to fall back cleanly, got tokenURI=%q metadata=%#v", tokenURI, metadata)
	}
}

func TestBuildSoulPublicAgentViewHandlesNilAndStrictIntegrity(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if view := s.buildSoulPublicAgentView(context.Background(), nil); view.AgentID != "" || view.Avatar != nil {
		t.Fatalf("expected nil identity to return zero view, got %#v", view)
	}

	s = &Server{
		cfg: config.Config{
			SoulV2StrictIntegrity:       true,
			SoulRPCURL:                  "http://rpc",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000abc",
		},
	}
	view := s.buildSoulPublicAgentView(context.Background(), &models.SoulAgentIdentity{AgentID: "not-hex"})
	if view.AgentID != "not-hex" || view.Avatar != nil {
		t.Fatalf("expected strict-integrity enrichment failure to preserve base identity, got %#v", view)
	}
}

func TestLoadSoulPublicAgentAvatarGuardClauses(t *testing.T) {
	t.Parallel()

	validIdentity := &models.SoulAgentIdentity{AgentID: "0x" + strings.Repeat("12", 32)}

	s := &Server{cfg: config.Config{SoulRegistryContractAddress: "0x0000000000000000000000000000000000000abc"}, dialEVM: func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		t.Fatalf("dialEVM should not be called when rpc url is blank")
		return nil, nil
	}}
	if view, err := s.loadSoulPublicAgentAvatar(context.Background(), validIdentity); err != nil || view != nil {
		t.Fatalf("expected blank rpc url to short-circuit, got view=%#v err=%v", view, err)
	}

	s.cfg.SoulRPCURL = "http://rpc"
	s.cfg.SoulRegistryContractAddress = "not-an-address"
	if view, err := s.loadSoulPublicAgentAvatar(context.Background(), validIdentity); err != nil || view != nil {
		t.Fatalf("expected invalid contract to short-circuit, got view=%#v err=%v", view, err)
	}

	s.cfg.SoulRegistryContractAddress = "0x0000000000000000000000000000000000000abc"
	if view, err := s.loadSoulPublicAgentAvatar(context.Background(), &models.SoulAgentIdentity{}); err != nil || view != nil {
		t.Fatalf("expected blank agent id to short-circuit, got view=%#v err=%v", view, err)
	}
}

func TestLoadSoulPublicAgentAvatarErrorPaths(t *testing.T) {
	t.Parallel()

	s := &Server{
		cfg: config.Config{
			SoulRPCURL:                  "http://rpc",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000abc",
		},
		dialEVM: func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
			return nil, errors.New("dial failed")
		},
	}
	if _, err := s.loadSoulPublicAgentAvatar(context.Background(), &models.SoulAgentIdentity{AgentID: "0x" + strings.Repeat("13", 32)}); err == nil {
		t.Fatalf("expected dial error to propagate")
	}

	tokenID := new(big.Int).SetUint64(14)
	tokenURICall, err := soul.EncodeTokenURICall(tokenID)
	if err != nil {
		t.Fatalf("encode tokenURI: %v", err)
	}
	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeSoulPublicEthClient{
			callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
				return packSingleStringResult(t, ""), nil
			},
			filterLogs: func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
				return nil, nil
			},
		}, nil
	}
	view, err := s.loadSoulPublicAgentAvatar(context.Background(), &models.SoulAgentIdentity{AgentID: "0x000000000000000000000000000000000000000000000000000000000000000e"})
	if err != nil {
		t.Fatalf("expected empty avatar view to be non-fatal, got err=%v", err)
	}
	if view != nil {
		t.Fatalf("expected empty avatar view to collapse to nil, got %#v", view)
	}
	_ = tokenURICall
}

func TestLoadSoulPublicAvatarViewFromChainErrorPaths(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000abc")
	tokenID := big.NewInt(21)
	tokenURICall, err := soul.EncodeTokenURICall(tokenID)
	if err != nil {
		t.Fatalf("encode tokenURI: %v", err)
	}

	client := &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			return []byte("bad"), nil
		},
	}
	if _, err := loadSoulPublicAvatarViewFromChain(context.Background(), client, contractAddr, tokenID); err == nil {
		t.Fatalf("expected invalid tokenURI response to fail")
	}

	client = &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			if !bytes.Equal(msg.Data, tokenURICall) {
				t.Fatalf("unexpected call: %#v", msg)
			}
			return packSingleStringResult(t, "data:text/plain,hello"), nil
		},
		filterLogs: func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
			return nil, errors.New("filter failed")
		},
	}
	if _, err := loadSoulPublicAvatarViewFromChain(context.Background(), client, contractAddr, tokenID); err == nil {
		t.Fatalf("expected renderer lookup error to fail")
	}
}

func TestLoadRendererHelperErrorPaths(t *testing.T) {
	t.Parallel()

	rendererAddr := common.HexToAddress("0x0000000000000000000000000000000000000abc")
	if _, err := loadRendererStyleName(context.Background(), &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			return nil, errors.New("style name failed")
		},
	}, rendererAddr); err == nil {
		t.Fatalf("expected style name call failure")
	}

	tokenID := big.NewInt(33)
	if _, err := loadRendererAvatarImage(context.Background(), &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			return nil, errors.New("render failed")
		},
	}, rendererAddr, tokenID); err == nil {
		t.Fatalf("expected render call failure")
	}

	if _, err := loadRendererAvatarImage(context.Background(), &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			return []byte("bad"), nil
		},
	}, rendererAddr, tokenID); err == nil {
		t.Fatalf("expected decode failure for malformed render result")
	}

	image, err := loadRendererAvatarImage(context.Background(), &fakeSoulPublicEthClient{
		callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
			return packSingleStringResult(t, "   "), nil
		},
	}, rendererAddr, tokenID)
	if err != nil || image != "" {
		t.Fatalf("expected blank svg to produce empty image, got image=%q err=%v", image, err)
	}
}

func TestDecodeMetadataHelperErrorPaths(t *testing.T) {
	t.Parallel()

	if _, err := decodeSoulAvatarTokenMetadata("not-a-data-uri"); err == nil {
		t.Fatalf("expected invalid metadata uri to fail")
	}
	if _, err := decodeSoulAvatarTokenMetadata("data:application/json,{"); err == nil {
		t.Fatalf("expected malformed json metadata to fail")
	}
	if _, _, err := decodeDataURI("data:text/plain"); err == nil {
		t.Fatalf("expected missing comma to fail")
	}
	if _, _, err := decodeDataURI("data:text/plain;base64,@@@"); err == nil {
		t.Fatalf("expected invalid base64 payload to fail")
	}
	if _, _, err := decodeDataURI("data:text/plain,%zz"); err == nil {
		t.Fatalf("expected invalid percent-encoding to fail")
	}
}

func TestAvatarMetadataAndStyleEmptyCases(t *testing.T) {
	t.Parallel()

	if soulAvatarMetadataStyleName(nil) != "" {
		t.Fatalf("expected nil metadata to have empty style name")
	}
	if soulAvatarMetadataStyleName(&soulAvatarTokenMetadata{
		Attributes: []soulAvatarTokenMetadataAttribute{{TraitType: "Mood", Value: "calm"}},
	}) != "" {
		t.Fatalf("expected non-style attributes to be ignored")
	}

	view := newSoulPublicAvatarView("token-uri", nil)
	if view == nil || view.TokenURI != "token-uri" || view.Image != "" {
		t.Fatalf("expected nil metadata to preserve only token uri, got %#v", view)
	}

	style := buildSoulPublicAvatarStyleView(context.Background(), &fakeSoulPublicEthClient{}, big.NewInt(1), 9, "Custom", map[uint8]common.Address{})
	if style.StyleID != 9 || style.StyleName != "Custom" || style.RendererAddress != "" {
		t.Fatalf("expected missing renderer to keep defaults, got %#v", style)
	}

	selectView := &soulPublicAvatarView{CurrentStyleName: "Sigil"}
	selectItem := soulPublicAvatarStyleView{StyleID: 2, StyleName: "Sigil", Image: "data:image/svg+xml;base64,abc"}
	selectCurrentSoulAvatarStyle(selectView, &selectItem)
	if selectView.Image != selectItem.Image {
		t.Fatalf("expected selected style to backfill missing image, got %#v", selectView)
	}

	if !soulPublicAvatarViewEmpty(nil) {
		t.Fatalf("expected nil view to be empty")
	}
}
