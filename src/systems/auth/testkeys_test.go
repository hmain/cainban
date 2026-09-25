package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"testing"
	"time"
)

// testSigner mints self-signed JWTs for tests, and exposes a KeySource backed
// by its own public key. This is the "mock JWKS / self-signed test key" the
// brief calls for: the Validator's signature-first logic runs against these
// tokens with no network and no real IdP.
type testSigner struct {
	kid  string
	key  *rsa.PrivateKey
	keys KeySource
}

func newTestSigner(t *testing.T, kid string) *testSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testSigner{
		kid:  kid,
		key:  key,
		keys: NewStaticKeySource(map[string]*rsa.PublicKey{kid: &key.PublicKey}),
	}
}

// tokenClaims is the minimal claim set the tests set.
type tokenClaims struct {
	Issuer      string   `json:"iss,omitempty"`
	Subject     string   `json:"sub,omitempty"`
	Audience    string   `json:"aud,omitempty"`
	Expiry      int64    `json:"exp,omitempty"`
	NotBefore   int64    `json:"nbf,omitempty"`
	IssuedAt    int64    `json:"iat,omitempty"`
	Repos       []string `json:"repos,omitempty"`
	DefaultRepo string   `json:"default_repo,omitempty"`
}

// sign builds a signed JWT with the given alg (default RS256) and claims.
func (ts *testSigner) sign(t *testing.T, alg string, c tokenClaims) string {
	t.Helper()
	if alg == "" {
		alg = "RS256"
	}
	header := map[string]string{"alg": alg, "kid": ts.kid, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(c)
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)

	var digest []byte
	var h crypto.Hash
	switch alg {
	case "RS256":
		d := sha256.Sum256([]byte(signingInput))
		digest, h = d[:], crypto.SHA256
	case "RS512":
		d := sha512.Sum512([]byte(signingInput))
		digest, h = d[:], crypto.SHA512
	default:
		// For unsupported algs the test does not need a real signature.
		return signingInput + ".AAAA"
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, ts.key, h, digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + b64.EncodeToString(sig)
}

// unsignedToken builds an alg=none token (the classic downgrade attack) with an
// empty signature segment.
func (ts *testSigner) unsignedToken(t *testing.T, c tokenClaims) string {
	t.Helper()
	header := map[string]string{"alg": "none", "kid": ts.kid, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(c)
	return b64.EncodeToString(hb) + "." + b64.EncodeToString(pb) + "."
}

// tamperedToken signs a token, then mutates the payload so the signature no
// longer matches (a forged-claims attack).
func (ts *testSigner) tamperedToken(t *testing.T, c tokenClaims) string {
	t.Helper()
	good := ts.sign(t, "RS256", c)
	// Replace the payload segment with one granting a repo the signature never
	// covered.
	forged := c
	forged.Repos = []string{"attacker/pwned"}
	pb, _ := json.Marshal(forged)
	// good = header.payload.sig ; swap payload, keep original signature.
	var header, _payload, sig string
	parts := splitThree(good)
	header, _payload, sig = parts[0], parts[1], parts[2]
	_ = _payload
	return header + "." + b64.EncodeToString(pb) + "." + sig
}

func splitThree(s string) [3]string {
	var out [3]string
	i := 0
	start := 0
	for j := 0; j < len(s) && i < 2; j++ {
		if s[j] == '.' {
			out[i] = s[start:j]
			i++
			start = j + 1
		}
	}
	out[2] = s[start:]
	return out
}

// baseConfig returns a Validator Config trusting ts, with a fixed clock.
func (ts *testSigner) baseConfig(now time.Time) Config {
	return Config{
		Issuer:   "https://issuer.test/pool",
		Audience: "test-audience",
		Keys:     ts.keys,
		Now:      func() time.Time { return now },
		Leeway:   0,
	}
}
