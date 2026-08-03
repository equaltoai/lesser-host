package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-host/internal/observability"
)

const (
	serviceName = "setup-bootstrap-wallet-resource"

	paramNameEnv = "BOOTSTRAP_WALLET_SSM_PARAM_NAME"

	requestCreate = "Create"
	requestUpdate = "Update"
	requestDelete = "Delete"

	dataBootstrapWalletAddress      = "BootstrapWalletAddress"
	dataBootstrapWalletSSMParamName = "BootstrapWalletSSMParamName"
)

type customResourceEvent struct {
	RequestType           string                 `json:"RequestType"`
	PhysicalResourceID    string                 `json:"PhysicalResourceId,omitempty"`
	ResourceProperties    map[string]interface{} `json:"ResourceProperties,omitempty"`
	OldResourceProperties map[string]interface{} `json:"OldResourceProperties,omitempty"`
}

type customResourceResponse struct {
	PhysicalResourceID string            `json:"PhysicalResourceId,omitempty"`
	Data               map[string]string `json:"Data,omitempty"`
}

type walletPayload struct {
	privateKey string
	address    string
}

type ssmAPI interface {
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	DeleteParameter(context.Context, *ssm.DeleteParameterInput, ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

type resourceHandler struct {
	ssm       ssmAPI
	paramName string
	obs       apptheory.ObservabilityHooks
}

func main() {
	obsHooks := observability.New(serviceName)
	// AppTheory does not yet natively dispatch CloudFormation custom-resource
	// events, but we still instantiate the standard observability hook set so
	// this Lambda follows the same entrypoint contract as the rest of the
	// control plane.
	obsApp := apptheory.New(
		apptheory.WithObservability(observability.New(serviceName)),
	)
	_ = obsApp

	lambda.Start(func(ctx context.Context, event customResourceEvent) (customResourceResponse, error) {
		handler, err := newDefaultHandler(ctx, obsHooks)
		if err != nil {
			return customResourceResponse{}, err
		}
		return handler.handle(ctx, event)
	})
}

func newDefaultHandler(ctx context.Context, obs apptheory.ObservabilityHooks) (*resourceHandler, error) {
	paramName := strings.TrimSpace(os.Getenv(paramNameEnv))
	if paramName == "" {
		return nil, fmt.Errorf("%s is required", paramNameEnv)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &resourceHandler{
		ssm:       ssm.NewFromConfig(cfg),
		paramName: paramName,
		obs:       obs,
	}, nil
}

func (h *resourceHandler) handle(ctx context.Context, event customResourceEvent) (customResourceResponse, error) {
	if h == nil || h.ssm == nil {
		return customResourceResponse{}, errors.New("resource handler is not configured")
	}
	if strings.TrimSpace(h.paramName) == "" {
		return customResourceResponse{}, errors.New("bootstrap wallet SSM parameter name is required")
	}
	if err := h.requireMatchingParamProperty(event.ResourceProperties); err != nil {
		return customResourceResponse{}, err
	}

	physicalID := event.PhysicalResourceID
	if strings.TrimSpace(physicalID) == "" {
		physicalID = fmt.Sprintf("setup-bootstrap-wallet:%s", h.paramName)
	}

	switch event.RequestType {
	case requestCreate:
		address, err := h.generateAndStore(ctx)
		if err != nil {
			return customResourceResponse{}, err
		}
		h.logLifecycle(ctx, requestCreate, "setup_bootstrap_wallet.created")
		return h.response(physicalID, address), nil
	case requestUpdate:
		address, err := h.addressFromStoredParameter(ctx)
		if isParameterNotFound(err) {
			address, err = h.generateAndStore(ctx)
			if err == nil {
				h.logLifecycle(ctx, requestUpdate, "setup_bootstrap_wallet.regenerated")
			}
		}
		if err != nil {
			return customResourceResponse{}, err
		}
		h.logLifecycle(ctx, requestUpdate, "setup_bootstrap_wallet.retained")
		return h.response(physicalID, address), nil
	case requestDelete:
		if err := h.deleteParameter(ctx); err != nil {
			return customResourceResponse{}, err
		}
		h.logLifecycle(ctx, requestDelete, "setup_bootstrap_wallet.deleted")
		return h.response(physicalID, ""), nil
	default:
		return customResourceResponse{}, fmt.Errorf("unsupported RequestType %q", event.RequestType)
	}
}

// logLifecycle emits the custom-resource lifecycle outcome through the
// standard apptheory observability hooks. The derived wallet address is
// intentionally not logged; it is surfaced through the CloudFormation
// response data instead.
func (h *resourceHandler) logLifecycle(ctx context.Context, requestType string, event string) {
	if h.obs.Log == nil {
		return
	}
	requestID := ""
	if lc, ok := lambdacontext.FromContext(ctx); ok {
		requestID = strings.TrimSpace(lc.AwsRequestID)
	}
	h.obs.Log(apptheory.LogRecord{
		Level:     "info",
		Event:     event,
		RequestID: requestID,
		Method:    requestType,
		Path:      h.paramName,
		Status:    200,
	})
}

func (h *resourceHandler) requireMatchingParamProperty(properties map[string]interface{}) error {
	if len(properties) == 0 {
		return nil
	}
	value, ok := properties[dataBootstrapWalletSSMParamName]
	if !ok {
		return nil
	}
	propValue, ok := value.(string)
	if !ok || strings.TrimSpace(propValue) == "" {
		return errors.New("BootstrapWalletSSMParamName custom-resource property must be a non-empty string")
	}
	if strings.TrimSpace(propValue) != h.paramName {
		return errors.New("BootstrapWalletSSMParamName custom-resource property does not match Lambda configuration")
	}
	return nil
}

func (h *resourceHandler) response(physicalID string, address string) customResourceResponse {
	data := map[string]string{
		dataBootstrapWalletSSMParamName: h.paramName,
	}
	if strings.TrimSpace(address) != "" {
		data[dataBootstrapWalletAddress] = address
	}
	return customResourceResponse{
		PhysicalResourceID: physicalID,
		Data:               data,
	}
}

func (h *resourceHandler) generateAndStore(ctx context.Context) (string, error) {
	payload, err := generateWalletPayload()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal generated bootstrap wallet payload: %w", err)
	}
	value := string(encoded)
	_, err = h.ssm.PutParameter(ctx, &ssm.PutParameterInput{
		Name:        aws.String(h.paramName),
		Value:       aws.String(value),
		Type:        types.ParameterTypeSecureString,
		Overwrite:   aws.Bool(true),
		Description: aws.String("CDK-owned lesser-host one-time setup bootstrap wallet private key"),
		Tier:        types.ParameterTierStandard,
	})
	if err != nil {
		return "", fmt.Errorf("store bootstrap wallet SecureString: %w", err)
	}
	return payload.address, nil
}

func (h *resourceHandler) addressFromStoredParameter(ctx context.Context) (string, error) {
	out, err := h.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(h.paramName),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	value := ""
	if out.Parameter != nil && out.Parameter.Value != nil {
		value = strings.TrimSpace(*out.Parameter.Value)
	}
	if value == "" {
		return "", errors.New("bootstrap wallet SSM parameter is empty")
	}
	address, err := addressFromPayload([]byte(value))
	if err != nil {
		return "", fmt.Errorf("derive bootstrap wallet address from SecureString: %w", err)
	}
	return address, nil
}

func (h *resourceHandler) deleteParameter(ctx context.Context) error {
	_, err := h.ssm.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String(h.paramName)})
	if isParameterNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete bootstrap wallet SecureString: %w", err)
	}
	return nil
}

func generateWalletPayload() (walletPayload, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return walletPayload{}, fmt.Errorf("generate EVM private key: %w", err)
	}
	return payloadFromPrivateKey(key), nil
}

func payloadFromPrivateKey(key *ecdsa.PrivateKey) walletPayload {
	return walletPayload{
		privateKey: "0x" + hex.EncodeToString(crypto.FromECDSA(key)),
		address:    crypto.PubkeyToAddress(key.PublicKey).Hex(),
	}
}

func addressFromPayload(raw []byte) (string, error) {
	var payload walletPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("parse bootstrap wallet payload JSON: %w", err)
	}
	privateKey := strings.TrimSpace(payload.privateKey)
	if privateKey != "" {
		return addressFromPrivateKeyPayload(privateKey, payload.address)
	}
	if strings.TrimSpace(payload.address) != "" {
		return normalizeAddress(payload.address)
	}
	return "", errors.New("bootstrap wallet payload must include private_key or address")
}

func (p walletPayload) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"private_key": p.privateKey,
		"address":     p.address,
	})
}

func (p *walletPayload) UnmarshalJSON(raw []byte) error {
	fields := map[string]string{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	p.privateKey = fields["private_key"]
	p.address = fields["address"]
	return nil
}

func addressFromPrivateKeyPayload(privateKey string, storedAddress string) (string, error) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	derivedAddress := crypto.PubkeyToAddress(key.PublicKey).Hex()
	if strings.TrimSpace(storedAddress) == "" {
		return derivedAddress, nil
	}
	normalized, err := normalizeAddress(storedAddress)
	if err != nil {
		return "", fmt.Errorf("invalid bootstrap wallet address in stored payload: %w", err)
	}
	if !strings.EqualFold(derivedAddress, normalized) {
		return "", fmt.Errorf("stored bootstrap wallet address does not match private key-derived address %s", derivedAddress)
	}
	return derivedAddress, nil
}

func parsePrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "0x"), "0X")
	if len(trimmed) != 64 {
		return nil, errors.New("bootstrap wallet private_key must be 32 bytes of hex")
	}
	key, err := crypto.HexToECDSA(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parse bootstrap wallet private_key: %w", err)
	}
	return key, nil
}

func normalizeAddress(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !common.IsHexAddress(trimmed) {
		return "", errors.New("not a valid EVM 0x address")
	}
	return common.HexToAddress(trimmed).Hex(), nil
}

func isParameterNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nf *types.ParameterNotFound
	if errors.As(err, &nf) {
		return true
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "ParameterNotFound"
}
