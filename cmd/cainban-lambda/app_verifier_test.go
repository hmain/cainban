package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// testRSAKeyPEM generates a throwaway PKCS#1 RSA private key in PEM form so the
// App client can be constructed without a real GitHub App key.
func testRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// TestAppVerifierFromSecret_Construction proves the App RepoVerifier is built
// from the secret map (app_id + private_key) and satisfies auth.RepoVerifier.
// It uses a generated RSA key PEM so no live GitHub or AWS call happens — only
// construction and interface satisfaction are exercised here (VerifyRepoAccess
// itself is covered by src/systems/github/verify_test.go).
func TestAppVerifierFromSecret_Construction(t *testing.T) {
	secret := map[string]string{
		"app_id":      "123456",
		"private_key": testRSAKeyPEM(t),
		"client_id":   "Iv1.test",
	}
	v, err := appVerifierFromSecret(secret)
	if err != nil {
		t.Fatalf("appVerifierFromSecret: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil verifier")
	}
	// It must satisfy the resolver's interface shape: a login-based call. With a
	// fake login and a key that cannot reach GitHub, the call fails closed
	// (error) — we only assert it compiles to the interface and returns, not the
	// outcome.
	_, _ = v.VerifyRepoAccess(context.Background(), "acme", "widgets", "octocat")
}

// TestAppVerifierFromSecret_BadAppID proves a non-numeric app_id is a
// construction error (fail at cold start, not at request time).
func TestAppVerifierFromSecret_BadAppID(t *testing.T) {
	_, err := appVerifierFromSecret(map[string]string{
		"app_id":      "not-a-number",
		"private_key": testRSAKeyPEM(t),
	})
	if err == nil {
		t.Fatal("expected error for non-numeric app_id")
	}
}

// TestAppVerifierFromSecret_MissingKey proves a missing private key is a
// construction error.
func TestAppVerifierFromSecret_MissingKey(t *testing.T) {
	_, err := appVerifierFromSecret(map[string]string{"app_id": "123456"})
	if err == nil {
		t.Fatal("expected error for missing private_key")
	}
}
