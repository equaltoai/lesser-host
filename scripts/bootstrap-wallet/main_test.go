package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestGenerateWalletPayloadDerivesAddress(t *testing.T) {
	payload, err := generateWalletPayload()
	if err != nil {
		t.Fatalf("generateWalletPayload() error = %v", err)
	}
	if !strings.HasPrefix(payload.privateKey, "0x") || len(payload.privateKey) != 66 {
		t.Fatalf("generated private key has unexpected shape")
	}
	if !common.IsHexAddress(payload.address) {
		t.Fatalf("generated address %q is not an EVM address", payload.address)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	address, err := addressFromPayload(encoded)
	if err != nil {
		t.Fatalf("addressFromPayload() error = %v", err)
	}
	if address != payload.address {
		t.Fatalf("addressFromPayload() = %s, want %s", address, payload.address)
	}
}

func TestAddressFromPayloadAcceptsAddressOnly(t *testing.T) {
	address, err := addressFromPayload([]byte(`{"address":"0x0000000000000000000000000000000000000001"}`))
	if err != nil {
		t.Fatalf("addressFromPayload() error = %v", err)
	}
	if address != "0x0000000000000000000000000000000000000001" {
		t.Fatalf("addressFromPayload() = %s", address)
	}
}

func TestAddressFromPayloadRejectsMismatchedAddress(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	payload := payloadFromPrivateKey(key)
	payload.address = "0x0000000000000000000000000000000000000001"
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := addressFromPayload(encoded); err == nil {
		t.Fatalf("addressFromPayload() succeeded for mismatched address")
	}
}

func TestAddressFromPayloadDerivesWhenStoredAddressOmitted(t *testing.T) {
	payload, err := generateWalletPayload()
	if err != nil {
		t.Fatalf("generateWalletPayload() error = %v", err)
	}
	encoded, err := json.Marshal(map[string]string{"private_key": payload.privateKey})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	address, err := addressFromPayload(encoded)
	if err != nil {
		t.Fatalf("addressFromPayload() error = %v", err)
	}
	if address != payload.address {
		t.Fatalf("addressFromPayload() = %s, want %s", address, payload.address)
	}
}

func TestNormalizeAddressRejectsPlaceholders(t *testing.T) {
	if _, err := normalizeAddress("<YOUR_BOOTSTRAP_WALLET_ADDRESS>"); err == nil {
		t.Fatalf("normalizeAddress() accepted placeholder")
	}
}

func TestRunGenerateAddressAndNormalize(t *testing.T) {
	generated, err := run([]string{"generate"}, nil)
	if err != nil {
		t.Fatalf("run(generate) error = %v", err)
	}
	address, err := addressFromPayload([]byte(generated))
	if err != nil {
		t.Fatalf("addressFromPayload(generated) error = %v", err)
	}

	addressOut, err := run([]string{"address"}, bytes.NewReader([]byte(generated)))
	if err != nil {
		t.Fatalf("run(address) error = %v", err)
	}
	if strings.TrimSpace(addressOut) != address {
		t.Fatalf("run(address) = %q, want %q", strings.TrimSpace(addressOut), address)
	}

	normalized, err := run([]string{"normalize-address", strings.ToLower(address)}, nil)
	if err != nil {
		t.Fatalf("run(normalize-address) error = %v", err)
	}
	if strings.TrimSpace(normalized) != address {
		t.Fatalf("run(normalize-address) = %q, want %q", strings.TrimSpace(normalized), address)
	}
}

func TestRunRejectsInvalidUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"missing command":         {},
		"unknown command":         {"bogus"},
		"generate extra args":     {"generate", "extra"},
		"address extra args":      {"address", "extra"},
		"normalize missing arg":   {"normalize-address"},
		"normalize extra args":    {"normalize-address", "0x0000000000000000000000000000000000000001", "extra"},
		"put missing parameter":   {"put-parameter-input"},
		"put unexpected argument": {"put-parameter-input", "--name", "/x", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(args, bytes.NewReader(nil)); err == nil {
				t.Fatalf("run(%v) succeeded", args)
			}
		})
	}
}

func TestPutParameterInputEncodesSecureStringRequest(t *testing.T) {
	payload, err := generateWalletPayload()
	if err != nil {
		t.Fatalf("generateWalletPayload() error = %v", err)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(payload) error = %v", err)
	}

	output, err := run(
		[]string{"put-parameter-input", "--name", "/lesser-host/lab/setup/bootstrap-wallet-private-key", "--description", "test"},
		bytes.NewReader(encodedPayload),
	)
	if err != nil {
		t.Fatalf("run(put-parameter-input) error = %v", err)
	}

	var input ssmPutParameterInput
	if err := json.Unmarshal([]byte(output), &input); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v", err)
	}
	if input.Name != "/lesser-host/lab/setup/bootstrap-wallet-private-key" {
		t.Fatalf("Name = %q", input.Name)
	}
	if input.Type != "SecureString" {
		t.Fatalf("Type = %q", input.Type)
	}
	if input.Value != string(encodedPayload) {
		t.Fatalf("Value did not preserve payload")
	}
	if input.Overwrite {
		t.Fatalf("Overwrite = true, want false")
	}
}

func TestPutParameterInputRejectsInvalidPayload(t *testing.T) {
	_, err := run(
		[]string{"put-parameter-input", "--name", "/lesser-host/lab/setup/bootstrap-wallet-private-key"},
		bytes.NewReader([]byte(`{"private_key":"not-hex"}`)),
	)
	if err == nil {
		t.Fatalf("run(put-parameter-input) accepted invalid payload")
	}
}

func TestAddressFromPayloadRejectsMalformedInputs(t *testing.T) {
	for name, payload := range map[string]string{
		"invalid json":           `{`,
		"empty object":           `{}`,
		"invalid private key":    `{"private_key":"0x1234"}`,
		"invalid stored address": `{"address":"not-an-address"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := addressFromPayload([]byte(payload)); err == nil {
				t.Fatalf("addressFromPayload(%s) succeeded", payload)
			}
		})
	}
}
