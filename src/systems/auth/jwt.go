package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// KeySource resolves a signing key by its JWKS `kid`. The production
// implementation is a JWKS HTTP fetcher (jwks.go); tests inject a static source
// backed by a self-signed key. Keeping this an interface is what lets the
// signature-first logic be unit-tested with no network and no real IdP.
type KeySource interface {
	// KeyByID returns the RSA public key for the given key id, or an error if
	// no such key is known.
	KeyByID(kid string) (*rsa.PublicKey, error)
}

// Config pins the trust anchors a token is validated against. A token is only
// accepted if its `iss` equals Issuer and its `aud` contains Audience.
type Config struct {
	// Issuer is the expected `iss` claim (e.g. the Cognito user-pool issuer
	// URL). Required.
	Issuer string
	// Audience is the expected `aud` claim (the Cognito app client id).
	// Required.
	Audience string
	// Keys resolves signing keys by kid. Required.
	Keys KeySource
	// Now allows tests to control the clock for expiry checks. Defaults to
	// time.Now.
	Now func() time.Time
	// Leeway tolerates small clock skew on exp/nbf/iat. Defaults to 60s.
	Leeway time.Duration
}

// Validator validates bearer JWTs signature-first and derives the caller
// identity. It performs NO authorization; that is Resolver's job, and it only
// ever runs on the Identity a successful Validate returns.
type Validator struct {
	cfg Config
}

// NewValidator builds a Validator. Issuer, Audience and Keys are required.
func NewValidator(cfg Config) (*Validator, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, fmt.Errorf("auth: Issuer is required")
	}
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, fmt.Errorf("auth: Audience is required")
	}
	if cfg.Keys == nil {
		return nil, fmt.Errorf("auth: Keys (JWKS source) is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 60 * time.Second
	}
	return &Validator{cfg: cfg}, nil
}

// jwtHeader is the decoded JOSE header.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// claims is the decoded payload. `aud` may be a string or an array in the JWT
// spec, so it is decoded permissively via audienceClaim.
type claims struct {
	Issuer      string        `json:"iss"`
	Subject     string        `json:"sub"`
	Audience    audienceClaim `json:"aud"`
	Expiry      int64         `json:"exp"`
	NotBefore   int64         `json:"nbf"`
	IssuedAt    int64         `json:"iat"`
	Repos       []string      `json:"repos"`
	DefaultRepo string        `json:"default_repo"`
}

// audienceClaim decodes an `aud` that is either a JSON string or a JSON array
// of strings.
type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// Validate parses a raw bearer token, verifies its SIGNATURE against the JWKS
// FIRST, then checks issuer/audience/expiry, and only then reads identity
// claims. Any failure up to and including signature/standard-claim validation
// returns a KindUnauthenticated error and NO Identity, so a caller can never
// reach a store on a bad token.
//
// The ordering here is deliberate and load-bearing: nothing about the token's
// claims (issuer/audience/repos/sub) is trusted or acted upon until the
// cryptographic signature over the header+payload has been verified with a key
// from the trusted JWKS.
func (v *Validator) Validate(rawToken string) (*Identity, error) {
	tok := strings.TrimSpace(rawToken)
	if tok == "" {
		return nil, unauthenticated("missing bearer token", nil)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, unauthenticated("malformed token: expected 3 JWT segments", nil)
	}
	headerSeg, payloadSeg, sigSeg := parts[0], parts[1], parts[2]

	headerBytes, err := b64.DecodeString(headerSeg)
	if err != nil {
		return nil, unauthenticated("malformed token header", err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, unauthenticated("malformed token header", err)
	}

	// --- STEP 1: SIGNATURE VALIDATION (before ANY claim is read/acted on) ---
	hashAlg, cryptoHash, err := rsaAlg(hdr.Alg)
	if err != nil {
		return nil, unauthenticated("unsupported token algorithm", err)
	}
	pub, err := v.cfg.Keys.KeyByID(hdr.Kid)
	if err != nil {
		return nil, unauthenticated("unknown signing key", err)
	}
	sig, err := b64.DecodeString(sigSeg)
	if err != nil {
		return nil, unauthenticated("malformed token signature", err)
	}
	signingInput := headerSeg + "." + payloadSeg
	digest := hashAlg([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, cryptoHash, digest, sig); err != nil {
		return nil, unauthenticated("invalid token signature", err)
	}

	// --- STEP 2: standard-claim validation (iss / aud / exp), still no
	//             identity/authorization decision ---
	payloadBytes, err := b64.DecodeString(payloadSeg)
	if err != nil {
		return nil, unauthenticated("malformed token payload", err)
	}
	var c claims
	if err := json.Unmarshal(payloadBytes, &c); err != nil {
		return nil, unauthenticated("malformed token payload", err)
	}

	if c.Issuer != v.cfg.Issuer {
		return nil, unauthenticated("token issuer not trusted", nil)
	}
	if !containsString(c.Audience, v.cfg.Audience) {
		return nil, unauthenticated("token audience mismatch", nil)
	}

	now := v.cfg.Now()
	if c.Expiry == 0 {
		return nil, unauthenticated("token missing exp", nil)
	}
	if now.After(time.Unix(c.Expiry, 0).Add(v.cfg.Leeway)) {
		return nil, unauthenticated("token expired", nil)
	}
	if c.NotBefore != 0 && now.Before(time.Unix(c.NotBefore, 0).Add(-v.cfg.Leeway)) {
		return nil, unauthenticated("token not yet valid", nil)
	}
	if strings.TrimSpace(c.Subject) == "" {
		return nil, unauthenticated("token missing sub", nil)
	}

	// --- STEP 3: only now, on a fully validated token, build the identity ---
	repos := make(map[string]struct{}, len(c.Repos))
	for _, r := range c.Repos {
		if norm, err := NormalizeRepo(r); err == nil {
			repos[norm] = struct{}{}
		}
	}
	defaultRepo := ""
	if strings.TrimSpace(c.DefaultRepo) != "" {
		if norm, err := NormalizeRepo(c.DefaultRepo); err == nil {
			defaultRepo = norm
		}
	}
	return &Identity{
		Subject:     c.Subject,
		Repos:       repos,
		DefaultRepo: defaultRepo,
	}, nil
}

// b64 is base64url without padding, the JWT segment encoding.
var b64 = base64.RawURLEncoding

// rsaAlg maps a JOSE alg to a hashing function + crypto.Hash. Only RSA PKCS1v15
// (RS256/384/512) is supported — the alg is taken from the header ONLY to
// select the hash, never to decide whether to skip verification, and an
// unknown/`none` alg is rejected (defeating the classic alg=none downgrade).
func rsaAlg(alg string) (func([]byte) []byte, crypto.Hash, error) {
	switch alg {
	case "RS256":
		return func(b []byte) []byte { h := sha256.Sum256(b); return h[:] }, crypto.SHA256, nil
	case "RS384":
		return func(b []byte) []byte { h := sha512.Sum384(b); return h[:] }, crypto.SHA384, nil
	case "RS512":
		return func(b []byte) []byte { h := sha512.Sum512(b); return h[:] }, crypto.SHA512, nil
	default:
		return nil, 0, fmt.Errorf("alg %q not supported (want RS256/RS384/RS512)", alg)
	}
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// jwk is one key in a JWKS document (RSA only).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// toRSAPublicKey converts a JWK to an *rsa.PublicKey.
func (k jwk) toRSAPublicKey() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type %q (want RSA)", k.Kty)
	}
	nBytes, err := b64.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("invalid modulus: %w", err)
	}
	eBytes, err := b64.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("invalid exponent: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 2 {
		return nil, fmt.Errorf("invalid exponent value")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
