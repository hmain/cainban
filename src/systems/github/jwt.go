package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// parseRSAPrivateKey decodes a PEM-encoded RSA private key. GitHub issues App
// keys in PKCS#1 ("RSA PRIVATE KEY"); we also accept PKCS#8 ("PRIVATE KEY") so
// a key round-tripped through openssl still loads. A non-RSA PKCS#8 key is
// rejected — the App JWT must be RS256.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: no PEM block found in private key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("github: parse PKCS#1 private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("github: parse PKCS#8 private key: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("github: PKCS#8 key is %T, want *rsa.PrivateKey", parsed)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("github: unsupported PEM block type %q (want RSA/PKCS#8 private key)", block.Type)
	}
}

// b64url is unpadded base64url, the JWS encoding for header/payload/signature.
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// mintAppJWT builds a signed GitHub App JWT (RS256) from the App id and RSA
// private key, valid for jwtTTL. Hand-rolled with the standard library so no
// third-party JWT dependency is added:
//
//	header  = {"alg":"RS256","typ":"JWT"}
//	claims  = {"iat":<now-skew>, "exp":<now+ttl>, "iss":"<appID>"}
//	signature = RSASSA-PKCS1-v1_5(SHA-256, base64url(header) "." base64url(claims))
//
// `iat` is back-dated by jwtClockSkew to tolerate a fast GitHub clock; `exp` is
// kept under GitHub's 10-minute ceiling. `iss` is the App id as a string, which
// GitHub accepts (it also accepts the numeric client id, but the App id is the
// canonical documented value). now is a parameter so tests are deterministic.
func mintAppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	if appID <= 0 {
		return "", errors.New("github: app id must be positive")
	}
	if key == nil {
		return "", errors.New("github: nil private key")
	}

	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("github: marshal jwt header: %w", err)
	}

	claims := map[string]any{
		"iat": now.Add(-jwtClockSkew).Unix(),
		"exp": now.Add(jwtTTL).Unix(),
		"iss": fmt.Sprintf("%d", appID),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("github: marshal jwt claims: %w", err)
	}

	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("github: sign jwt: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}
