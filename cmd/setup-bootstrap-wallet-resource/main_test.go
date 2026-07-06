package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/ethereum/go-ethereum/common"
)

type fakeSSM struct {
	putInputs    []*ssm.PutParameterInput
	getInputs    []*ssm.GetParameterInput
	deleteInputs []*ssm.DeleteParameterInput

	getValue  string
	getErr    error
	deleteErr error
}

func (f *fakeSSM) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.putInputs = append(f.putInputs, input)
	return &ssm.PutParameterOutput{}, nil
}

func (f *fakeSSM) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.getInputs = append(f.getInputs, input)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &ssm.GetParameterOutput{
		Parameter: &types.Parameter{Value: aws.String(f.getValue)},
	}, nil
}

func (f *fakeSSM) DeleteParameter(_ context.Context, input *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	f.deleteInputs = append(f.deleteInputs, input)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &ssm.DeleteParameterOutput{}, nil
}

func TestHandleCreateGeneratesAndOverwritesStackOwnedParameter(t *testing.T) {
	t.Parallel()

	ssmClient := &fakeSSM{}
	handler := &resourceHandler{
		ssm:       ssmClient,
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}

	resp, err := handler.handle(context.Background(), customResourceEvent{
		RequestType: requestCreate,
		ResourceProperties: map[string]interface{}{
			dataBootstrapWalletSSMParamName: handler.paramName,
		},
	})
	if err != nil {
		t.Fatalf("handle(Create) error = %v", err)
	}

	if len(ssmClient.getInputs) != 0 {
		t.Fatalf("Create must not read/reuse a stale SSM value")
	}
	if len(ssmClient.putInputs) != 1 {
		t.Fatalf("PutParameter calls = %d, want 1", len(ssmClient.putInputs))
	}
	input := ssmClient.putInputs[0]
	if input.Name == nil || *input.Name != handler.paramName {
		t.Fatalf("PutParameter name = %v", input.Name)
	}
	if input.Type != types.ParameterTypeSecureString {
		t.Fatalf("PutParameter type = %s, want SecureString", input.Type)
	}
	if input.Overwrite == nil || !*input.Overwrite {
		t.Fatalf("Create must overwrite any stale pre-fix parameter")
	}
	if input.Value == nil || strings.TrimSpace(*input.Value) == "" {
		t.Fatalf("PutParameter value missing")
	}

	var payload walletPayload
	if err := json.Unmarshal([]byte(*input.Value), &payload); err != nil {
		t.Fatalf("stored payload is not JSON: %v", err)
	}
	if payload.PrivateKey == "" || payload.Address == "" {
		t.Fatalf("stored payload missing private key or address")
	}
	if !common.IsHexAddress(payload.Address) {
		t.Fatalf("stored address %q is not an EVM address", payload.Address)
	}
	derived, err := addressFromPayload([]byte(*input.Value))
	if err != nil {
		t.Fatalf("addressFromPayload(stored) error = %v", err)
	}
	if resp.Data[dataBootstrapWalletAddress] != derived {
		t.Fatalf("response address = %s, want %s", resp.Data[dataBootstrapWalletAddress], derived)
	}
	if resp.Data[dataBootstrapWalletSSMParamName] != handler.paramName {
		t.Fatalf("response param = %s, want %s", resp.Data[dataBootstrapWalletSSMParamName], handler.paramName)
	}
	if strings.Contains(fmt.Sprintf("%#v", resp), payload.PrivateKey) {
		t.Fatalf("response leaked private key")
	}
}

func TestHandleUpdateRetainsExistingStackOwnedParameter(t *testing.T) {
	t.Parallel()

	payload, err := generateWalletPayload()
	if err != nil {
		t.Fatalf("generateWalletPayload() error = %v", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload) error = %v", err)
	}

	ssmClient := &fakeSSM{getValue: string(encoded)}
	handler := &resourceHandler{
		ssm:       ssmClient,
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}

	resp, err := handler.handle(context.Background(), customResourceEvent{
		RequestType:        requestUpdate,
		PhysicalResourceID: "setup-bootstrap-wallet:/lesser-host/lab/setup/bootstrap-wallet-private-key",
		ResourceProperties: map[string]interface{}{
			dataBootstrapWalletSSMParamName: handler.paramName,
		},
	})
	if err != nil {
		t.Fatalf("handle(Update) error = %v", err)
	}
	if len(ssmClient.getInputs) != 1 {
		t.Fatalf("GetParameter calls = %d, want 1", len(ssmClient.getInputs))
	}
	if len(ssmClient.putInputs) != 0 {
		t.Fatalf("Update must not rotate an existing setup wallet")
	}
	if resp.Data[dataBootstrapWalletAddress] != payload.Address {
		t.Fatalf("response address = %s, want %s", resp.Data[dataBootstrapWalletAddress], payload.Address)
	}
}

func TestHandleUpdateRegeneratesOnlyWhenStackOwnedParameterIsMissing(t *testing.T) {
	t.Parallel()

	ssmClient := &fakeSSM{getErr: &types.ParameterNotFound{}}
	handler := &resourceHandler{
		ssm:       ssmClient,
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}

	resp, err := handler.handle(context.Background(), customResourceEvent{
		RequestType: requestUpdate,
		ResourceProperties: map[string]interface{}{
			dataBootstrapWalletSSMParamName: handler.paramName,
		},
	})
	if err != nil {
		t.Fatalf("handle(Update missing) error = %v", err)
	}
	if len(ssmClient.getInputs) != 1 {
		t.Fatalf("GetParameter calls = %d, want 1", len(ssmClient.getInputs))
	}
	if len(ssmClient.putInputs) != 1 {
		t.Fatalf("missing parameter update must generate one replacement, got %d puts", len(ssmClient.putInputs))
	}
	if !common.IsHexAddress(resp.Data[dataBootstrapWalletAddress]) {
		t.Fatalf("response address %q is not an EVM address", resp.Data[dataBootstrapWalletAddress])
	}
}

func TestHandleDeleteRemovesStackOwnedParameterIdempotently(t *testing.T) {
	t.Parallel()

	ssmClient := &fakeSSM{deleteErr: &types.ParameterNotFound{}}
	handler := &resourceHandler{
		ssm:       ssmClient,
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}

	resp, err := handler.handle(context.Background(), customResourceEvent{RequestType: requestDelete})
	if err != nil {
		t.Fatalf("handle(Delete) error = %v", err)
	}
	if len(ssmClient.deleteInputs) != 1 {
		t.Fatalf("DeleteParameter calls = %d, want 1", len(ssmClient.deleteInputs))
	}
	if resp.Data[dataBootstrapWalletAddress] != "" {
		t.Fatalf("delete response must not include bootstrap address")
	}
}

func TestHandleRejectsParamPropertyMismatch(t *testing.T) {
	t.Parallel()

	handler := &resourceHandler{
		ssm:       &fakeSSM{},
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}

	_, err := handler.handle(context.Background(), customResourceEvent{
		RequestType: requestCreate,
		ResourceProperties: map[string]interface{}{
			dataBootstrapWalletSSMParamName: "/lesser-host/live/setup/bootstrap-wallet-private-key",
		},
	})
	if err == nil {
		t.Fatalf("handle accepted mismatched param property")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteReturnsNonNotFoundErrors(t *testing.T) {
	t.Parallel()

	handler := &resourceHandler{
		ssm:       &fakeSSM{deleteErr: errors.New("boom")},
		paramName: "/lesser-host/lab/setup/bootstrap-wallet-private-key",
	}
	_, err := handler.handle(context.Background(), customResourceEvent{RequestType: requestDelete})
	if err == nil {
		t.Fatalf("expected delete error")
	}
}
