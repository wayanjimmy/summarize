package mcpauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
)

// parseJWK converts a JWK's raw fields into a crypto.PublicKey.
// Supports RSA (kty=RSA) and EC (kty=EC) keys.
func parseJWK(kty, n, e, x, y, crv string) (any, error) {
	switch kty {
	case "RSA":
		return parseRSAKey(n, e)
	case "EC":
		return parseECKey(x, y, crv)
	default:
		return nil, fmt.Errorf("unsupported kty %q", kty)
	}
}

func parseRSAKey(nStr, eStr string) (any, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsUint64() {
		return nil, fmt.Errorf("exponent too large")
	}
	return &rsa.PublicKey{N: n, E: int(e.Uint64())}, nil
}

func parseECKey(xStr, yStr, crv string) (any, error) {
	xBytes, err := base64.RawURLEncoding.DecodeString(xStr)
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(yStr)
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	var curve elliptic.Curve
	switch crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported crv %q", crv)
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	if !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("EC point not on curve %s", crv)
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}
