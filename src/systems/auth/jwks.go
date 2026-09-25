package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// jwksDocument is the top-level JWKS response shape.
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// JWKSCache is a production KeySource that fetches and caches an IdP's JWKS over
// HTTP. It is safe for concurrent use. Keys are cached for the TTL; a lookup for
// an unknown kid triggers at most one refresh (a rotated signing key appears
// after the refresh) rather than a fetch per request.
//
// It satisfies KeySource, so the Validator's signature-first logic is identical
// whether keys come from here or from a test's static source.
type JWKSCache struct {
	url        string
	httpClient *http.Client
	ttl        time.Duration
	now        func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// JWKSCacheOption configures a JWKSCache.
type JWKSCacheOption func(*JWKSCache)

// WithHTTPClient overrides the HTTP client (e.g. a shorter-timeout client).
func WithHTTPClient(c *http.Client) JWKSCacheOption {
	return func(j *JWKSCache) { j.httpClient = c }
}

// WithTTL overrides the cache TTL (default 1h).
func WithTTL(ttl time.Duration) JWKSCacheOption {
	return func(j *JWKSCache) { j.ttl = ttl }
}

// NewJWKSCache builds a JWKS cache for the given JWKS URL. For a Cognito user
// pool the URL is
// https://cognito-idp.<region>.amazonaws.com/<userPoolId>/.well-known/jwks.json.
func NewJWKSCache(jwksURL string, opts ...JWKSCacheOption) *JWKSCache {
	j := &JWKSCache{
		url:        jwksURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		ttl:        time.Hour,
		now:        time.Now,
		keys:       map[string]*rsa.PublicKey{},
	}
	for _, o := range opts {
		o(j)
	}
	return j
}

// KeyByID returns the RSA public key for kid, refreshing the JWKS if the key is
// unknown or the cache is stale.
func (j *JWKSCache) KeyByID(kid string) (*rsa.PublicKey, error) {
	j.mu.RLock()
	key, ok := j.keys[kid]
	fresh := j.now().Sub(j.fetchedAt) < j.ttl
	j.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}

	if err := j.refresh(context.Background()); err != nil {
		// If the refresh failed but we still hold a cached key, use it rather
		// than failing closed on a transient JWKS outage.
		j.mu.RLock()
		key, ok = j.keys[kid]
		j.mu.RUnlock()
		if ok {
			return key, nil
		}
		return nil, fmt.Errorf("jwks refresh failed and key %q not cached: %w", kid, err)
	}

	j.mu.RLock()
	key, ok = j.keys[kid]
	j.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no key %q in JWKS", kid)
	}
	return key, nil
}

// refresh fetches the JWKS and replaces the cached key set.
func (j *JWKSCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return fmt.Errorf("build jwks request: %w", err)
	}
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read jwks body: %w", err)
	}
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse jwks: %w", err)
	}
	next := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		pub, err := k.toRSAPublicKey()
		if err != nil {
			// Skip keys we cannot use (e.g. non-RSA) rather than failing the
			// whole set.
			continue
		}
		next[k.Kid] = pub
	}
	if len(next) == 0 {
		return fmt.Errorf("jwks contained no usable RSA keys")
	}

	j.mu.Lock()
	j.keys = next
	j.fetchedAt = j.now()
	j.mu.Unlock()
	return nil
}

// staticKeySource is a KeySource backed by an in-memory map. It is exported via
// NewStaticKeySource for tests and for a future "keys pinned in config" mode.
type staticKeySource struct {
	keys map[string]*rsa.PublicKey
}

// NewStaticKeySource builds a KeySource from a kid->key map (used by tests with
// a self-signed key, and usable for pinned keys).
func NewStaticKeySource(keys map[string]*rsa.PublicKey) KeySource {
	cp := make(map[string]*rsa.PublicKey, len(keys))
	for k, v := range keys {
		cp[k] = v
	}
	return &staticKeySource{keys: cp}
}

func (s *staticKeySource) KeyByID(kid string) (*rsa.PublicKey, error) {
	k, ok := s.keys[kid]
	if !ok {
		return nil, fmt.Errorf("no key %q", kid)
	}
	return k, nil
}
