package attestations

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type KMSService struct {
	signingKeyID string
	publicKeyIDs []string

	once   sync.Once
	client *kms.Client
	err    error

	jwksMu        sync.Mutex
	jwksCached    JWKS
	jwksCachedAt  time.Time
	jwksCacheTTL  time.Duration
	publicKeyMu   sync.Mutex
	publicKeyMemo map[string]*rsa.PublicKey
}

func NewKMSService(signingKeyID string, publicKeyIDs []string) *KMSService {
	signingKeyID = strings.TrimSpace(signingKeyID)

	var keys []string
	seen := map[string]struct{}{}
	for _, k := range publicKeyIDs {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	if len(keys) == 0 && signingKeyID != "" {
		keys = []string{signingKeyID}
	}

	return &KMSService{
		signingKeyID:  signingKeyID,
		publicKeyIDs:  keys,
		jwksCacheTTL:  5 * time.Minute,
		publicKeyMemo: map[string]*rsa.PublicKey{},
	}
}

func (s *KMSService) Enabled() bool {
	return s != nil && strings.TrimSpace(s.signingKeyID) != ""
}

func (s *KMSService) kmsClient(ctx context.Context) (*kms.Client, error) {
	if s == nil {
		return nil, fmt.Errorf("kms service is nil")
	}
	s.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			s.err = err
			return
		}
		s.client = kms.NewFromConfig(cfg)
	})
	if s.err != nil {
		return nil, s.err
	}
	if s.client == nil {
		return nil, fmt.Errorf("kms client not initialized")
	}
	return s.client, nil
}

func (s *KMSService) SignPayloadJWS(ctx context.Context, payload []byte) (string, string, error) {
	if s == nil {
		return "", "", fmt.Errorf("kms service is nil")
	}
	keyID := strings.TrimSpace(s.signingKeyID)
	if keyID == "" {
		return "", "", fmt.Errorf("signing key not configured")
	}

	client, err := s.kmsClient(ctx)
	if err != nil {
		return "", "", err
	}

	signDigest := func(ctx context.Context, digest []byte) ([]byte, error) {
		out, err := client.Sign(ctx, &kms.SignInput{
			KeyId:            &keyID,
			Message:          digest,
			MessageType:      kmstypes.MessageTypeDigest,
			SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
		})
		if err != nil {
			return nil, err
		}
		return out.Signature, nil
	}

	jws, err := BuildCompactJWSRS256(ctx, keyID, payload, signDigest)
	if err != nil {
		return "", "", err
	}
	return jws, keyID, nil
}

func (s *KMSService) JWKS(ctx context.Context) (JWKS, error) {
	if s == nil {
		return JWKS{}, fmt.Errorf("kms service is nil")
	}
	if len(s.publicKeyIDs) == 0 {
		return JWKS{}, fmt.Errorf("public keys not configured")
	}

	s.jwksMu.Lock()
	cached := s.jwksCached
	cachedAt := s.jwksCachedAt
	ttl := s.jwksCacheTTL
	s.jwksMu.Unlock()

	if cachedAt.After(time.Time{}) && ttl > 0 && time.Since(cachedAt) < ttl && len(cached.Keys) > 0 {
		return cached, nil
	}

	keys := make([]JWK, 0, len(s.publicKeyIDs))
	for _, keyID := range s.publicKeyIDs {
		pub, err := s.rsaPublicKey(ctx, keyID)
		if err != nil {
			return JWKS{}, err
		}
		jwk, err := JWKFromRSAPublicKey(keyID, pub)
		if err != nil {
			return JWKS{}, err
		}
		keys = append(keys, jwk)
	}

	sort.Slice(keys, func(i, j int) bool { return strings.TrimSpace(keys[i].Kid) < strings.TrimSpace(keys[j].Kid) })
	out := JWKS{Keys: keys}

	s.jwksMu.Lock()
	s.jwksCached = out
	s.jwksCachedAt = time.Now().UTC()
	s.jwksMu.Unlock()

	return out, nil
}

func (s *KMSService) rsaPublicKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, fmt.Errorf("key id is required")
	}
	if s == nil {
		return nil, fmt.Errorf("kms service is nil")
	}

	s.publicKeyMu.Lock()
	if s.publicKeyMemo != nil {
		if cached, ok := s.publicKeyMemo[keyID]; ok && cached != nil {
			s.publicKeyMu.Unlock()
			return cached, nil
		}
	}
	s.publicKeyMu.Unlock()

	client, err := s.kmsClient(ctx)
	if err != nil {
		return nil, err
	}

	out, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &keyID})
	if err != nil {
		return nil, err
	}
	if len(out.PublicKey) == 0 {
		return nil, fmt.Errorf("kms returned empty public key")
	}

	pubAny, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		return nil, err
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("kms public key is not rsa")
	}

	s.publicKeyMu.Lock()
	if s.publicKeyMemo == nil {
		s.publicKeyMemo = map[string]*rsa.PublicKey{}
	}
	s.publicKeyMemo[keyID] = pub
	s.publicKeyMu.Unlock()

	return pub, nil
}
