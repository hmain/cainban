package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// jwksTestKey builds a one-key JWKS document body for the given kid + key.
func jwksTestKeyBody(t *testing.T, kid string, pub *rsa.PublicKey) []byte {
	t.Helper()
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	doc := jwksDocument{Keys: []jwk{{
		Kty: "RSA",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
		Use: "sig",
		Alg: "RS256",
	}}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return b
}

// TestJWKSCache_MaxStaleCeiling is the RFC M2 guard. When the JWKS endpoint
// goes down after the cache is populated, a cached key is served only while the
// cache age is below the max-stale ceiling; once the cache is older than the
// ceiling AND refresh keeps failing, KeyByID fails CLOSED so a revoked/rotated
// key cannot validate tokens indefinitely.
func TestJWKSCache_MaxStaleCeiling(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	const kid = "kid-1"
	body := jwksTestKeyBody(t, kid, &key.PublicKey)

	var up bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up {
			http.Error(w, "jwks down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	base := time.Unix(1_700_000_000, 0)
	clock := base
	cache := NewJWKSCache(srv.URL, WithTTL(time.Hour), WithMaxStale(24*time.Hour))
	cache.now = func() time.Time { return clock }

	// First fetch succeeds and populates the cache.
	up = true
	if _, err := cache.KeyByID(kid); err != nil {
		t.Fatalf("initial KeyByID should succeed: %v", err)
	}

	// Endpoint goes down. Within the max-stale ceiling the cached key is still
	// served (rides out the outage).
	up = false
	clock = base.Add(2 * time.Hour) // past TTL (1h), within maxStale (24h)
	if _, err := cache.KeyByID(kid); err != nil {
		t.Fatalf("within max-stale, a cached key should still be served: %v", err)
	}

	// Past the ceiling with refresh still failing: must fail CLOSED.
	clock = base.Add(25 * time.Hour) // past maxStale (24h)
	if _, err := cache.KeyByID(kid); err == nil {
		t.Fatalf("past max-stale with refresh failing, KeyByID MUST fail closed, but returned a key")
	}

	// When the endpoint recovers, a fresh fetch restores service.
	up = true
	if _, err := cache.KeyByID(kid); err != nil {
		t.Fatalf("after recovery KeyByID should succeed: %v", err)
	}
}
