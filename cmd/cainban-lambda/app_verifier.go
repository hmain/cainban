package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/github"
)

// appVerifier adapts a *github.Verifier to the auth.RepoVerifier interface the
// resolver depends on. The resolver cannot import src/systems/github (which
// imports auth for NormalizeRepo — that would cycle), so this adapter lives in
// the Lambda main package, which may import both. It translates the resolver's
// login string into a github.Principal and delegates to VerifyRepoAccess.
type appVerifier struct {
	v *github.Verifier
}

// VerifyRepoAccess satisfies auth.RepoVerifier. FAIL CLOSED is preserved: the
// underlying Verifier returns (false, err) on any GitHub error, which the
// resolver maps to a 401 deny.
func (a *appVerifier) VerifyRepoAccess(ctx context.Context, owner, repo, login string) (bool, error) {
	return a.v.VerifyRepoAccess(ctx, owner, repo, github.Principal{Login: login})
}

// appVerifierFromSecret builds the App-backed RepoVerifier from the
// cainban/github-app secret map. It parses the numeric app_id and the PEM
// private key up front so a malformed secret fails at COLD START (not at the
// first request). A timeout-bounded *http.Client is used for the live GitHub
// calls. Returns an error the caller must treat as "App authz unavailable".
func appVerifierFromSecret(secret map[string]string) (*appVerifier, error) {
	appIDRaw := strings.TrimSpace(secret["app_id"])
	if appIDRaw == "" {
		return nil, fmt.Errorf("github app secret: missing app_id")
	}
	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("github app secret: app_id %q is not numeric: %w", appIDRaw, err)
	}
	key := strings.TrimSpace(secret["private_key"])
	if key == "" {
		return nil, fmt.Errorf("github app secret: missing private_key")
	}

	client, err := github.NewClient(github.AppConfig{
		AppID:         appID,
		PrivateKeyPEM: []byte(key),
		ClientID:      strings.TrimSpace(secret["client_id"]),
		ClientSecret:  strings.TrimSpace(secret["client_secret"]),
	}, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("github app secret: build client: %w", err)
	}
	return &appVerifier{v: github.NewVerifier(client)}, nil
}
