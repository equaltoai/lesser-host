package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const walletChallengeDuration = 5 * time.Minute

var errInvalidWalletSignature = errors.New("invalid wallet signature")

type walletChallengeRequest struct {
	Address  string `json:"address"`
	ChainID  int    `json:"chainId,omitempty"`
	Username string `json:"username"`
}

type walletVerifyRequest struct {
	ChallengeID string `json:"challengeId"`
	Address     string `json:"address"`
	Signature   string `json:"signature"`
	Message     string `json:"message"`
}

type walletChallengeResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username,omitempty"`
	Address   string    `json:"address"`
	ChainID   int       `json:"chainId"`
	Nonce     string    `json:"nonce"`
	Message   string    `json:"message"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func buildWalletAuthMessage(domain, address string, chainID int, nonce, username string, issuedAt, expiresAt time.Time) string {
	var sb strings.Builder

	address = strings.ToLower(strings.TrimSpace(address))

	sb.WriteString(domain)
	sb.WriteString(" wants you to sign in with your Ethereum account:\n")
	sb.WriteString(address)
	sb.WriteString("\n\n")
	sb.WriteString("Sign this message to authenticate with ")
	sb.WriteString(domain)
	sb.WriteString(" as '")
	sb.WriteString(username)
	sb.WriteString("'\n\n")
	sb.WriteString("URI: https://")
	sb.WriteString(domain)
	sb.WriteString("\nVersion: 1\nChain ID: ")
	sb.WriteString(strconv.Itoa(chainID))
	sb.WriteString("\nNonce: ")
	sb.WriteString(nonce)
	sb.WriteString("\nIssued At: ")
	sb.WriteString(issuedAt.UTC().Format(time.RFC3339))
	sb.WriteString("\nExpiration Time: ")
	sb.WriteString(expiresAt.UTC().Format(time.RFC3339))

	return sb.String()
}

func generateNonce() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *Server) createWalletChallenge(ctx context.Context, address string, chainID int, username string) (*models.WalletChallenge, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}

	address = strings.ToLower(strings.TrimSpace(address))
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	if address == "" {
		return nil, errors.New("address is required")
	}
	if chainID == 0 {
		chainID = 1
	}

	nonce, err := generateNonce()
	if err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	id, err := newToken(16)
	if err != nil {
		return nil, fmt.Errorf("generate id: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(walletChallengeDuration)

	domain := strings.TrimSpace(s.cfg.WebAuthnRPID)
	if domain == "" {
		domain = "lesser.host"
	}
	message := buildWalletAuthMessage(domain, address, chainID, nonce, username, now, expiresAt)

	challenge := &models.WalletChallenge{
		ID:        id,
		Username:  username,
		Address:   address,
		ChainID:   chainID,
		Nonce:     nonce,
		Message:   message,
		IssuedAt:  now,
		ExpiresAt: expiresAt,
	}
	if err := challenge.UpdateKeys(); err != nil {
		return nil, fmt.Errorf("update keys: %w", err)
	}

	if err := s.store.DB.WithContext(ctx).Model(challenge).Create(); err != nil {
		return nil, err
	}

	return challenge, nil
}

func (s *Server) getWalletChallenge(ctx context.Context, id string) (*models.WalletChallenge, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, errors.New("store not configured")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("challenge id is required")
	}

	var challenge models.WalletChallenge
	err := s.store.DB.WithContext(ctx).
		Model(&models.WalletChallenge{}).
		Where("PK", "=", fmt.Sprintf("WALLET_CHALLENGE#%s", id)).
		Where("SK", "=", "CHALLENGE").
		First(&challenge)
	if err != nil {
		return nil, err
	}

	if !challenge.ExpiresAt.IsZero() && time.Now().After(challenge.ExpiresAt) {
		_ = s.deleteWalletChallenge(ctx, id)
		return nil, theoryErrors.ErrItemNotFound
	}
	if challenge.Spent {
		return nil, theoryErrors.ErrItemNotFound
	}

	return &challenge, nil
}

func (s *Server) deleteWalletChallenge(ctx context.Context, id string) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return errors.New("store not configured")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	return s.store.DB.WithContext(ctx).
		Model(&models.WalletChallenge{}).
		Where("PK", "=", fmt.Sprintf("WALLET_CHALLENGE#%s", id)).
		Where("SK", "=", "CHALLENGE").
		Delete()
}

func verifyEthereumSignature(address, message, signature string) error {
	sig, err := hexutil.Decode(signature)
	if err != nil {
		return errors.Join(errInvalidWalletSignature, err)
	}
	if len(sig) != 65 {
		return errInvalidWalletSignature
	}

	if sig[64] == 27 || sig[64] == 28 {
		sig[64] -= 27
	}

	msgHash := accounts.TextHash([]byte(message))
	pubKey, err := crypto.SigToPub(msgHash, sig)
	if err != nil {
		return errors.Join(errInvalidWalletSignature, err)
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)

	recoveredHex := strings.ToLower(strings.TrimPrefix(recoveredAddr.Hex(), "0x"))
	expectedHex := strings.ToLower(strings.TrimPrefix(address, "0x"))

	if recoveredHex != expectedHex {
		return errInvalidWalletSignature
	}

	return nil
}

func verifyEthereumSignatureBytes(address string, message []byte, signature string) error {
	sig, err := hexutil.Decode(signature)
	if err != nil {
		return errors.Join(errInvalidWalletSignature, err)
	}
	if len(sig) != 65 {
		return errInvalidWalletSignature
	}

	if sig[64] == 27 || sig[64] == 28 {
		sig[64] -= 27
	}

	msgHash := accounts.TextHash(message)
	pubKey, err := crypto.SigToPub(msgHash, sig)
	if err != nil {
		return errors.Join(errInvalidWalletSignature, err)
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)

	recoveredHex := strings.ToLower(strings.TrimPrefix(recoveredAddr.Hex(), "0x"))
	expectedHex := strings.ToLower(strings.TrimPrefix(address, "0x"))

	if recoveredHex != expectedHex {
		return errInvalidWalletSignature
	}

	return nil
}
