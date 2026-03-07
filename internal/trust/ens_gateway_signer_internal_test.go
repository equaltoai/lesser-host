package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/equaltoai/lesser-host/internal/config"
)

func mustTrustTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}

func TestNewLocalENSGatewaySigner_ValidatesAndSigns(t *testing.T) {
	key := mustTrustTestKey(t)
	privateKeyHex := common.Bytes2Hex(crypto.FromECDSA(key))

	if _, err := newLocalENSGatewaySigner(""); err == nil {
		t.Fatalf("expected error for empty private key")
	}
	if _, err := newLocalENSGatewaySigner("0xnothex"); err == nil {
		t.Fatalf("expected error for invalid private key")
	}

	signer, err := newLocalENSGatewaySigner("0x" + privateKeyHex)
	if err != nil {
		t.Fatalf("newLocalENSGatewaySigner: %v", err)
	}
	if signer.Address() != crypto.PubkeyToAddress(key.PublicKey) {
		t.Fatalf("unexpected signer address: %s", signer.Address())
	}

	var digest [32]byte
	copy(digest[:], crypto.Keccak256([]byte("ens-gateway")))

	compact, err := signer.SignDigest(context.Background(), digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	if len(compact) != 64 {
		t.Fatalf("expected compact signature, got %d bytes", len(compact))
	}

	sig65, err := compactToSig65(compact)
	if err != nil {
		t.Fatalf("compactToSig65: %v", err)
	}
	pub, err := crypto.SigToPub(digest[:], sig65)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if crypto.PubkeyToAddress(*pub) != signer.Address() {
		t.Fatalf("unexpected recovered address")
	}

	var nilSigner *localENSGatewaySigner
	if _, err := nilSigner.SignDigest(context.Background(), digest); err == nil {
		t.Fatalf("expected error for nil signer")
	}
	if nilSigner.Address() != (common.Address{}) {
		t.Fatalf("expected zero address for nil signer")
	}
}

func TestKMSAndServerSignerGuards(t *testing.T) {
	if _, err := newKMSENSGatewaySigner(context.Background(), ""); err == nil {
		t.Fatalf("expected empty key id error")
	}

	var nilKMSSigner *kmsENSGatewaySigner
	var digest [32]byte
	if _, err := nilKMSSigner.SignDigest(context.Background(), digest); err == nil {
		t.Fatalf("expected error for nil KMS signer")
	}
	if nilKMSSigner.Address() != (common.Address{}) {
		t.Fatalf("expected zero address for nil KMS signer")
	}

	if _, err := (*Server)(nil).ensureENSGatewaySigner(context.Background()); err == nil {
		t.Fatalf("expected error for nil server")
	}

	s := &Server{}
	signer, err := s.ensureENSGatewaySigner(context.Background())
	if err != nil || signer != nil {
		t.Fatalf("expected nil signer without config, got signer=%#v err=%v", signer, err)
	}

	key := mustTrustTestKey(t)
	s = &Server{cfg: config.Config{ENSGatewaySigningPrivateKey: common.Bytes2Hex(crypto.FromECDSA(key))}}
	signer, err = s.ensureENSGatewaySigner(context.Background())
	if err != nil || signer == nil {
		t.Fatalf("expected local signer, got signer=%#v err=%v", signer, err)
	}
}

func TestParseENSGatewayPublicKey_Secp256k1SPKI(t *testing.T) {
	der, err := base64.StdEncoding.DecodeString("MFYwEAYHKoZIzj0CAQYFK4EEAAoDQgAEHwo2cCum7SQHk2xNugV8uDmUbjszh8lFKA35vFCg2Rk8v+Dv7Km6LDGSuIrElqAuHaQbhZk6PAbSNVoN9Jz3/A==")
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}

	pub, err := parseENSGatewayPublicKey(der)
	if err != nil {
		t.Fatalf("parseENSGatewayPublicKey: %v", err)
	}

	got := crypto.PubkeyToAddress(*pub)
	want := common.HexToAddress("0xFD4450A49cA55Cc155075dA46E07fAd2e383429B")
	if got != want {
		t.Fatalf("unexpected address: got %s want %s", got, want)
	}
}

func TestParseECDSADER_ValidAndInvalid(t *testing.T) {
	key := mustTrustTestKey(t)
	digest := crypto.Keccak256([]byte("der-signature"))

	der, err := ecdsa.SignASN1(rand.Reader, key, digest)
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}
	r, s, err := parseECDSADER(der)
	if err != nil || r == nil || s == nil {
		t.Fatalf("parseECDSADER: r=%v s=%v err=%v", r, s, err)
	}
	if _, _, parseErr := parseECDSADER([]byte("bad")); parseErr == nil {
		t.Fatalf("expected parse error for invalid DER")
	}

	emptyDER, marshalErr := asn1.Marshal(ecdsaDER{R: big.NewInt(0), S: big.NewInt(1)})
	if marshalErr != nil {
		t.Fatalf("Marshal empty DER: %v", marshalErr)
	}
	if _, _, parseErr := parseECDSADER(emptyDER); parseErr == nil {
		t.Fatalf("expected invalid DER signature error")
	}
}

func TestCompactHelpers_RoundTripAndValidation(t *testing.T) {
	key := mustTrustTestKey(t)
	digest := crypto.Keccak256([]byte("der-signature"))

	sig65, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	compact, err := sig65ToCompact(sig65)
	if err != nil {
		t.Fatalf("sig65ToCompact: %v", err)
	}
	roundTrip, err := compactToSig65(compact)
	if err != nil {
		t.Fatalf("compactToSig65: %v", err)
	}
	if len(roundTrip) != 65 || roundTrip[64] != sig65[64] {
		t.Fatalf("unexpected round-trip signature: %x", roundTrip)
	}

	if _, err := sig65ToCompact([]byte("short")); err == nil {
		t.Fatalf("expected signature length error")
	}
	badRecovery := append([]byte(nil), sig65...)
	badRecovery[64] = 3
	if _, err := sig65ToCompact(badRecovery); err == nil {
		t.Fatalf("expected invalid recovery id error")
	}
	highS := append([]byte(nil), sig65...)
	highSValue := new(big.Int).Sub(crypto.S256().Params().N, new(big.Int).SetBytes(sig65[32:64]))
	copy(highS[32:64], leftPad32(highSValue.Bytes()))
	if _, err := sig65ToCompact(highS); err == nil {
		t.Fatalf("expected high-s rejection")
	}
	if _, err := compactToSig65([]byte("short")); err == nil {
		t.Fatalf("expected compact signature length error")
	}
}

func TestLeftPad32(t *testing.T) {
	if got := leftPad32([]byte{1, 2, 3}); len(got) != 32 || got[29] != 1 || got[31] != 3 {
		t.Fatalf("unexpected padded bytes: %x", got)
	}
	if got := leftPad32(make([]byte, 40)); len(got) != 32 {
		t.Fatalf("expected truncation to 32 bytes, got %d", len(got))
	}
}

func TestSignatureToCompactWithRecovery(t *testing.T) {
	key := mustTrustTestKey(t)
	address := crypto.PubkeyToAddress(key.PublicKey)
	digestBytes := crypto.Keccak256([]byte("recovery"))
	var digest [32]byte
	copy(digest[:], digestBytes)

	sig65, err := crypto.Sign(digest[:], key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	r := new(big.Int).SetBytes(sig65[:32])
	s := new(big.Int).SetBytes(sig65[32:64])

	compact, err := signatureToCompactWithRecovery(digest, r, s, address)
	if err != nil {
		t.Fatalf("signatureToCompactWithRecovery: %v", err)
	}
	roundTrip, err := compactToSig65(compact)
	if err != nil {
		t.Fatalf("compactToSig65: %v", err)
	}
	pub, err := crypto.SigToPub(digest[:], roundTrip)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if crypto.PubkeyToAddress(*pub) != address {
		t.Fatalf("unexpected recovered address")
	}

	if _, err := signatureToCompactWithRecovery(digest, nil, s, address); err == nil {
		t.Fatalf("expected invalid signature error for nil r")
	}
	if _, err := signatureToCompactWithRecovery(digest, r, nil, address); err == nil {
		t.Fatalf("expected invalid signature error for nil s")
	}

	otherKey := mustTrustTestKey(t)
	if _, err := signatureToCompactWithRecovery(digest, r, s, crypto.PubkeyToAddress(otherKey.PublicKey)); err == nil {
		t.Fatalf("expected recovery failure for wrong address")
	}
}
