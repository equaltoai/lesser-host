package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type ensGatewaySigner interface {
	Address() common.Address
	SignDigest(ctx context.Context, digest [32]byte) ([]byte, error) // returns an EIP-2098 compact signature (r,vs) as 64 bytes
}

type localENSGatewaySigner struct {
	key     *ecdsa.PrivateKey
	address common.Address
}

func newLocalENSGatewaySigner(privateKeyHex string) (*localENSGatewaySigner, error) {
	privateKeyHex = strings.TrimSpace(privateKeyHex)
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
	if privateKeyHex == "" {
		return nil, fmt.Errorf("ens gateway signer: empty private key")
	}

	key, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("ens gateway signer: invalid private key: %w", err)
	}

	return &localENSGatewaySigner{
		key:     key,
		address: crypto.PubkeyToAddress(key.PublicKey),
	}, nil
}

func (s *localENSGatewaySigner) Address() common.Address {
	if s == nil {
		return common.Address{}
	}
	return s.address
}

func (s *localENSGatewaySigner) SignDigest(ctx context.Context, digest [32]byte) ([]byte, error) {
	_ = ctx
	if s == nil || s.key == nil {
		return nil, fmt.Errorf("ens gateway signer: not configured")
	}

	sig65, err := crypto.Sign(digest[:], s.key)
	if err != nil {
		return nil, fmt.Errorf("ens gateway signer: sign failed: %w", err)
	}
	return sig65ToCompact(sig65)
}

type kmsENSGatewaySigner struct {
	keyID   string
	client  *kms.Client
	address common.Address
}

type ensGatewaySubjectPublicKeyInfo struct {
	Algorithm        pkix.AlgorithmIdentifier
	SubjectPublicKey asn1.BitString
}

func parseENSGatewayPublicKey(der []byte) (*ecdsa.PublicKey, error) {
	if len(der) == 0 {
		return nil, fmt.Errorf("ens gateway signer: empty public key")
	}

	parsed, err := x509.ParsePKIXPublicKey(der)
	if err == nil {
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok || pub == nil {
			return nil, fmt.Errorf("ens gateway signer: unsupported public key type %T", parsed)
		}
		return pub, nil
	}

	var spki ensGatewaySubjectPublicKeyInfo
	if _, unmarshalErr := asn1.Unmarshal(der, &spki); unmarshalErr != nil {
		return nil, fmt.Errorf("ens gateway signer: parse public key: %w", err)
	}
	if len(spki.SubjectPublicKey.Bytes) == 0 {
		return nil, fmt.Errorf("ens gateway signer: empty subject public key")
	}

	pub, unmarshalErr := crypto.UnmarshalPubkey(spki.SubjectPublicKey.Bytes)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("ens gateway signer: parse public key: %w", err)
	}
	return pub, nil
}

func newKMSENSGatewaySigner(ctx context.Context, keyID string) (*kmsENSGatewaySigner, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, fmt.Errorf("ens gateway signer: empty KMS key id")
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("ens gateway signer: load AWS config: %w", err)
	}

	client := kms.NewFromConfig(awsCfg)
	pubOut, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return nil, fmt.Errorf("ens gateway signer: get public key: %w", err)
	}
	if len(pubOut.PublicKey) == 0 {
		return nil, fmt.Errorf("ens gateway signer: empty public key")
	}

	pub, err := parseENSGatewayPublicKey(pubOut.PublicKey)
	if err != nil {
		return nil, err
	}

	return &kmsENSGatewaySigner{
		keyID:   keyID,
		client:  client,
		address: crypto.PubkeyToAddress(*pub),
	}, nil
}

func (s *kmsENSGatewaySigner) Address() common.Address {
	if s == nil {
		return common.Address{}
	}
	return s.address
}

func (s *kmsENSGatewaySigner) SignDigest(ctx context.Context, digest [32]byte) ([]byte, error) {
	if s == nil || s.client == nil || strings.TrimSpace(s.keyID) == "" {
		return nil, fmt.Errorf("ens gateway signer: not configured")
	}

	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            aws.String(s.keyID),
		Message:          digest[:],
		MessageType:      kmstypes.MessageTypeDigest,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		return nil, fmt.Errorf("ens gateway signer: KMS sign failed: %w", err)
	}
	if len(out.Signature) == 0 {
		return nil, fmt.Errorf("ens gateway signer: KMS returned empty signature")
	}

	r, sigS, err := parseECDSADER(out.Signature)
	if err != nil {
		return nil, err
	}

	compact, err := signatureToCompactWithRecovery(digest, r, sigS, s.address)
	if err != nil {
		return nil, err
	}
	return compact, nil
}

type ecdsaDER struct {
	R *big.Int
	S *big.Int
}

func parseECDSADER(der []byte) (*big.Int, *big.Int, error) {
	var sig ecdsaDER
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return nil, nil, fmt.Errorf("ens gateway signer: parse DER signature: %w", err)
	}
	if sig.R == nil || sig.S == nil || sig.R.Sign() <= 0 || sig.S.Sign() <= 0 {
		return nil, nil, fmt.Errorf("ens gateway signer: invalid DER signature")
	}
	return sig.R, sig.S, nil
}

func signatureToCompactWithRecovery(digest [32]byte, r *big.Int, sigS *big.Int, expected common.Address) ([]byte, error) {
	if r == nil || sigS == nil {
		return nil, fmt.Errorf("ens gateway signer: invalid signature")
	}

	// Normalize s to low-s to satisfy OpenZeppelin's ECDSA requirements.
	curveN := crypto.S256().Params().N
	halfN := new(big.Int).Rsh(new(big.Int).Set(curveN), 1)
	s := new(big.Int).Set(sigS)
	if s.Cmp(halfN) > 0 {
		s.Sub(curveN, s)
	}

	rb := leftPad32(r.Bytes())
	sb := leftPad32(s.Bytes())

	var v byte
	var found bool
	for rec := byte(0); rec <= 1; rec++ {
		sig65 := make([]byte, 65)
		copy(sig65[:32], rb)
		copy(sig65[32:64], sb)
		sig65[64] = rec

		pub, err := crypto.SigToPub(digest[:], sig65)
		if err != nil || pub == nil {
			continue
		}
		if crypto.PubkeyToAddress(*pub) == expected {
			v = rec
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("ens gateway signer: failed to recover signature")
	}

	sig65 := make([]byte, 65)
	copy(sig65[:32], rb)
	copy(sig65[32:64], sb)
	sig65[64] = v

	return sig65ToCompact(sig65)
}

func sig65ToCompact(sig65 []byte) ([]byte, error) {
	if len(sig65) != 65 {
		return nil, fmt.Errorf("ens gateway signer: invalid signature length")
	}

	v := sig65[64]
	if v == 27 || v == 28 {
		v -= 27
	}
	if v != 0 && v != 1 {
		return nil, fmt.Errorf("ens gateway signer: invalid signature recovery id")
	}

	// Ensure s is low and fits into EIP-2098 compact encoding.
	curveN := crypto.S256().Params().N
	halfN := new(big.Int).Rsh(new(big.Int).Set(curveN), 1)
	s := new(big.Int).SetBytes(sig65[32:64])
	if s.Sign() <= 0 || s.Cmp(halfN) > 0 {
		return nil, fmt.Errorf("ens gateway signer: non-canonical signature (high-s)")
	}

	out := make([]byte, 64)
	copy(out[:32], sig65[:32])   // r
	copy(out[32:], sig65[32:64]) // s
	if v == 1 {
		out[32] |= 0x80 // set highest bit in s to encode v
	}
	return out, nil
}

func compactToSig65(compact []byte) ([]byte, error) {
	if len(compact) != 64 {
		return nil, fmt.Errorf("ens gateway signer: invalid compact signature length")
	}

	sig65 := make([]byte, 65)
	copy(sig65[:32], compact[:32]) // r

	vs := append([]byte(nil), compact[32:]...)
	v := byte(0)
	if vs[0]&0x80 != 0 {
		v = 1
		vs[0] &= 0x7f
	}
	copy(sig65[32:64], vs)
	sig65[64] = v
	return sig65, nil
}

func leftPad32(b []byte) []byte {
	if len(b) == 32 {
		return b
	}
	if len(b) > 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func (s *Server) ensureENSGatewaySigner(ctx context.Context) (ensGatewaySigner, error) {
	if s == nil {
		return nil, fmt.Errorf("ens gateway signer: server is nil")
	}

	s.ensSignerOnce.Do(func() {
		keyID := strings.TrimSpace(s.cfg.ENSGatewaySigningKeyID)
		privateKey := strings.TrimSpace(s.cfg.ENSGatewaySigningPrivateKey)

		switch {
		case keyID != "":
			signer, err := newKMSENSGatewaySigner(ctx, keyID)
			if err != nil {
				s.ensSignerErr = err
				return
			}
			s.ensSigner = signer
		case privateKey != "":
			signer, err := newLocalENSGatewaySigner(privateKey)
			if err != nil {
				s.ensSignerErr = err
				return
			}
			s.ensSigner = signer
		default:
			s.ensSigner = nil
		}
	})

	return s.ensSigner, s.ensSignerErr
}
