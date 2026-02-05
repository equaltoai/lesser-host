package attestations

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
)

// JWKS is a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK is a JSON Web Key.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid,omitempty"`

	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
}

// JWKFromRSAPublicKey converts an RSA public key into a JWK.
func JWKFromRSAPublicKey(kid string, key *rsa.PublicKey) (JWK, error) {
	if key == nil {
		return JWK{}, fmt.Errorf("public key is nil")
	}
	if key.N == nil || key.N.Sign() <= 0 {
		return JWK{}, fmt.Errorf("invalid rsa modulus")
	}
	if key.E <= 0 {
		return JWK{}, fmt.Errorf("invalid rsa exponent")
	}

	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())

	// Per JWK spec, exponent is base64url-encoded unsigned big-endian.
	eBig := big.NewInt(int64(key.E))
	e := base64.RawURLEncoding.EncodeToString(eBig.Bytes())

	return JWK{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: kid,
		N:   n,
		E:   e,
	}, nil
}
