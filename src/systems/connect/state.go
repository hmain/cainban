// Package connect is cainban's Phase 4, step 3 human GitHub-connect API: the
// Cognito-authenticated routes that identify a user's GitHub login (via the
// App's user-OAuth leg), verify their access to owner/repo server-side (via the
// github.Verifier), and write/read/revoke the resulting grants (via the grants
// store).
//
// # Security model (carried from Phase 3/4)
//
//   - Every route validates the Cognito JWT SIGNATURE-FIRST (via the auth
//     Validator) before any GitHub call, secret read, or grant write. A
//     missing/invalid/expired token is a 401 and nothing else runs.
//   - IDENTITY (who the connecting user is on GitHub) originates ONLY from the
//     OAuth leg: the callback exchanges a real code for the user's login via
//     GitHub, and persists it for the validated Cognito sub. A client can never
//     supply "I am login X".
//   - AUTHORIZATION (may this sub touch owner/repo) originates ONLY from
//     github.VerifyRepoAccess run against the OAuth-derived login. A client
//     claim of access is never sufficient; a verify error FAILS CLOSED (no
//     grant is written).
//   - Grants are written/read ONLY for the validated sub — a caller can never
//     address another subject's grants.
//   - Anti-CSRF: the OAuth `state` is an HMAC-signed, sub-bound, expiring value
//     (see below). The callback rejects any state that does not verify or whose
//     bound sub does not match the caller's validated sub.
//
// # Mockable seams
//
// The handler depends on interfaces (Authenticator, OAuthLeg, Verifier, grant
// Store) so it is unit-tested end to end with a self-signed Cognito JWT, a fake
// OAuth leg, a mock verifier, and an in-memory grant store — NO live GitHub or
// AWS call in tests or CI.
package connect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// stateTTL bounds how long an issued OAuth state is accepted, limiting the
// window for a replayed/forged callback.
const stateTTL = 10 * time.Minute

// StateSigner mints and verifies opaque anti-CSRF OAuth state values. A state
// is HMAC-SHA256 signed over "<sub>|<expiryUnix>|<nonce>" with a per-deployment
// key, so it is:
//
//   - SELF-CONTAINED (no server-side store needed): the callback verifies it
//     with the same key — a stateless, replay-bounded token.
//   - BOUND to the Cognito sub that started the flow: the callback checks the
//     state's sub equals the caller's validated sub, so a state minted for one
//     user cannot complete another user's link.
//   - EXPIRING: an expired state is rejected.
//   - UNFORGEABLE without the key: any tampering breaks the HMAC.
type StateSigner struct {
	key []byte
	now func() time.Time
	// randNonce returns a fresh unguessable nonce per Issue; overridable in
	// tests for determinism.
	randNonce func() (string, error)
}

// NewStateSigner builds a signer from a secret key (derive it from the App's
// OAuth client secret, which lives only in Secrets Manager). An empty key is
// rejected — there is no unsigned/fail-open state path.
func NewStateSigner(key []byte) (*StateSigner, error) {
	if len(key) == 0 {
		return nil, errors.New("connect: state signer requires a non-empty key")
	}
	// Copy the key so a caller mutating its slice cannot change our signing key.
	k := make([]byte, len(key))
	copy(k, key)
	return &StateSigner{key: k, now: time.Now, randNonce: defaultNonce}, nil
}

// Issue mints a signed state bound to sub, valid for stateTTL.
func (s *StateSigner) Issue(sub string) (string, error) {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", errors.New("connect: cannot issue state for empty subject")
	}
	if strings.Contains(sub, "|") {
		// The payload is pipe-delimited; a sub containing '|' would corrupt
		// parsing. Cognito subs are UUIDs, so this never happens in practice,
		// but reject rather than mis-sign.
		return "", errors.New("connect: subject contains reserved delimiter")
	}
	nonce, err := s.randNonce()
	if err != nil {
		return "", fmt.Errorf("connect: generate state nonce: %w", err)
	}
	exp := s.now().Add(stateTTL).Unix()
	payload := sub + "|" + strconv.FormatInt(exp, 10) + "|" + nonce
	mac := s.sign(payload)
	// Encode payload and mac separately so verification is a constant-time
	// compare of the mac over the exact payload bytes.
	token := b64.EncodeToString([]byte(payload)) + "." + b64.EncodeToString(mac)
	return token, nil
}

// Verify checks a state token: it must have a valid HMAC, be unexpired, and be
// bound to expectSub (the caller's validated Cognito sub). It returns an error
// on ANY failure (bad format, bad signature, expired, sub mismatch) — the
// callback treats any error as "reject the callback".
func (s *StateSigner) Verify(token, expectSub string) error {
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 {
		return errors.New("connect: malformed state")
	}
	payloadBytes, err := b64.DecodeString(parts[0])
	if err != nil {
		return errors.New("connect: malformed state payload")
	}
	gotMAC, err := b64.DecodeString(parts[1])
	if err != nil {
		return errors.New("connect: malformed state signature")
	}
	// Recompute the MAC over the exact payload bytes and constant-time compare
	// BEFORE trusting any field inside the payload.
	wantMAC := s.sign(string(payloadBytes))
	if !hmac.Equal(gotMAC, wantMAC) {
		return errors.New("connect: state signature mismatch")
	}
	fields := strings.Split(string(payloadBytes), "|")
	if len(fields) != 3 {
		return errors.New("connect: malformed state fields")
	}
	sub, expStr := fields[0], fields[1]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("connect: malformed state expiry")
	}
	if s.now().After(time.Unix(exp, 0)) {
		return errors.New("connect: state expired")
	}
	if sub != strings.TrimSpace(expectSub) {
		// The state was minted for a DIFFERENT subject than the caller now
		// presenting it — reject (this is the anti-CSRF binding).
		return errors.New("connect: state subject mismatch")
	}
	return nil
}

// sign computes the HMAC-SHA256 of payload with the signer's key.
func (s *StateSigner) sign(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// b64 is unpadded base64url (URL-safe, so the state is query-string safe).
var b64 = base64.RawURLEncoding
