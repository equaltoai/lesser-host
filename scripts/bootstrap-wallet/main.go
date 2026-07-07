package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type walletPayload struct {
	privateKey string
	address    string
}

type ssmPutParameterInput struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Value       string `json:"Value"`
	Description string `json:"Description,omitempty"`
	Overwrite   bool   `json:"Overwrite"`
}

func main() {
	output, err := run(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap-wallet: %v\n", err)
		os.Exit(1)
	}
	if output != "" {
		if _, err := os.Stdout.WriteString(output + "\n"); err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap-wallet: write output: %v\n", err)
			os.Exit(1)
		}
	}
}

func run(args []string, stdin io.Reader) (string, error) {
	if len(args) == 0 {
		return "", errors.New("usage: bootstrap-wallet <generate|address|normalize-address|put-parameter-input>")
	}

	switch args[0] {
	case "generate":
		return runGenerate(args[1:])
	case "address":
		return runAddress(args[1:], stdin)
	case "normalize-address":
		return runNormalizeAddress(args[1:])
	case "put-parameter-input":
		return runPutParameterInput(args[1:], stdin)
	default:
		return "", fmt.Errorf("unknown command %q", args[0])
	}
}

func runGenerate(args []string) (string, error) {
	if len(args) != 0 {
		return "", errors.New("usage: bootstrap-wallet generate")
	}
	payload, err := generateWalletPayload()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal generated payload: %w", err)
	}
	return string(encoded), nil
}

func runAddress(args []string, stdin io.Reader) (string, error) {
	if len(args) != 0 {
		return "", errors.New("usage: bootstrap-wallet address < payload.json")
	}
	address, err := addressFromReader(stdin)
	if err != nil {
		return "", err
	}
	return address, nil
}

func runNormalizeAddress(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("usage: bootstrap-wallet normalize-address <0x-address>")
	}
	address, err := normalizeAddress(args[0])
	if err != nil {
		return "", err
	}
	return address, nil
}

func runPutParameterInput(args []string, stdin io.Reader) (string, error) {
	name, description, err := parsePutParameterFlags(args)
	if err != nil {
		return "", err
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}
	if _, payloadErr := addressFromPayload(payload); payloadErr != nil {
		return "", payloadErr
	}
	input := ssmPutParameterInput{
		Name:        name,
		Type:        "SecureString",
		Value:       strings.TrimSpace(string(payload)),
		Description: description,
		Overwrite:   false,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal ssm put-parameter input: %w", err)
	}
	return string(encoded), nil
}

func parsePutParameterFlags(args []string) (string, string, error) {
	fs := flag.NewFlagSet("put-parameter-input", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "SSM parameter name")
	description := fs.String("description", "", "SSM parameter description")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*name) == "" {
		return "", "", errors.New("usage: bootstrap-wallet put-parameter-input --name <ssm-name> [--description <text>] < payload.json")
	}
	return strings.TrimSpace(*name), strings.TrimSpace(*description), nil
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

func (p walletPayload) MarshalJSON() ([]byte, error) {
	fields := map[string]string{}
	if strings.TrimSpace(p.privateKey) != "" {
		fields["private_key"] = strings.TrimSpace(p.privateKey)
	}
	if strings.TrimSpace(p.address) != "" {
		fields["address"] = strings.TrimSpace(p.address)
	}
	return json.Marshal(fields)
}

func addressFromReader(stdin io.Reader) (string, error) {
	payload, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}
	return addressFromPayload(payload)
}

func addressFromPayload(raw []byte) (string, error) {
	payload, err := parseWalletPayload(raw)
	if err != nil {
		return "", err
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

func parseWalletPayload(raw []byte) (walletPayload, error) {
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return walletPayload{}, fmt.Errorf("parse bootstrap wallet payload JSON: %w", err)
	}
	return walletPayload{
		privateKey: fields["private_key"],
		address:    fields["address"],
	}, nil
}

func addressFromPrivateKeyPayload(privateKey, storedAddress string) (string, error) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	derivedAddress := crypto.PubkeyToAddress(key.PublicKey).Hex()
	if strings.TrimSpace(storedAddress) == "" {
		return derivedAddress, nil
	}
	if err := requireMatchingAddress(storedAddress, derivedAddress); err != nil {
		return "", err
	}
	return derivedAddress, nil
}

func requireMatchingAddress(storedAddress, derivedAddress string) error {
	normalized, err := normalizeAddress(storedAddress)
	if err != nil {
		return fmt.Errorf("invalid bootstrap wallet address in stored payload: %w", err)
	}
	if !strings.EqualFold(derivedAddress, normalized) {
		return fmt.Errorf("stored bootstrap wallet address %s does not match private key-derived address %s", normalized, derivedAddress)
	}
	return nil
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
		return "", fmt.Errorf("%q is not a valid EVM 0x address", trimmed)
	}
	return common.HexToAddress(trimmed).Hex(), nil
}
