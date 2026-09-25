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
	"strconv"
	"strings"
	"testing"
	"time"
)

// testKey generates a small RSA key for deterministic tests (2048 is plenty for
// verifying the JWS shape; test speed matters more than production key size).
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return key
}

func pkcs1PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestParseRSAPrivateKey(t *testing.T) {
	key := testKey(t)

	t.Run("pkcs1", func(t *testing.T) {
		got, err := parseRSAPrivateKey(pkcs1PEM(t, key))
		if err != nil {
			t.Fatalf("pkcs1: %v", err)
		}
		if got.N.Cmp(key.N) != 0 {
			t.Fatal("pkcs1: parsed key differs from original")
		}
	})

	t.Run("pkcs8", func(t *testing.T) {
		got, err := parseRSAPrivateKey(pkcs8PEM(t, key))
		if err != nil {
			t.Fatalf("pkcs8: %v", err)
		}
		if got.N.Cmp(key.N) != 0 {
			t.Fatal("pkcs8: parsed key differs from original")
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		if _, err := parseRSAPrivateKey([]byte("not a pem")); err == nil {
			t.Fatal("expected error for non-PEM input")
		}
	})

	t.Run("unsupported block type rejected", func(t *testing.T) {
		bad := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte{1, 2, 3}})
		if _, err := parseRSAPrivateKey(bad); err == nil {
			t.Fatal("expected error for EC block type")
		}
	})
}

// TestMintAppJWTShape verifies the minted token is a valid RS256 JWS with the
// documented header and claim shape, a verifiable signature, and a TTL under
// GitHub's 10-minute ceiling. NO live GitHub call.
func TestMintAppJWTShape(t *testing.T) {
	key := testKey(t)
	const appID int64 = 424242
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	tok, err := mintAppJWT(appID, key, now)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS segments, got %d", len(parts))
	}

	// Header.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["alg"] != "RS256" {
		t.Errorf("alg = %q, want RS256", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("typ = %q, want JWT", header["typ"])
	}

	// Claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if iss, _ := claims["iss"].(string); iss != strconv.FormatInt(appID, 10) {
		t.Errorf("iss = %v, want %d", claims["iss"], appID)
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		t.Fatalf("iat missing/not a number: %v", claims["iat"])
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("exp missing/not a number: %v", claims["exp"])
	}
	// iat is back-dated by the clock skew.
	if want := float64(now.Add(-jwtClockSkew).Unix()); iat != want {
		t.Errorf("iat = %v, want %v (now - skew)", iat, want)
	}
	// TTL must be under GitHub's 10-minute maximum.
	if ttl := time.Duration(int64(exp)-int64(iat)) * time.Second; ttl > 10*time.Minute {
		t.Errorf("token TTL %v exceeds GitHub's 10-minute ceiling", ttl)
	}

	// Signature verifies against the public key.
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestMintAppJWTRejectsBadInput(t *testing.T) {
	key := testKey(t)
	if _, err := mintAppJWT(0, key, time.Now()); err == nil {
		t.Error("expected error for non-positive app id")
	}
	if _, err := mintAppJWT(1, nil, time.Now()); err == nil {
		t.Error("expected error for nil key")
	}
}
