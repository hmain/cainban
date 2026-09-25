package secrets

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// fakeSM is an in-memory Secrets Manager: it returns a canned SecretString (or
// error) and records the requested SecretId. No live AWS.
type fakeSM struct {
	secretString string
	binary       []byte
	err          error
	gotSecretID  string
}

func (f *fakeSM) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if in.SecretId != nil {
		f.gotSecretID = *in.SecretId
	}
	if f.err != nil {
		return nil, f.err
	}
	out := &secretsmanager.GetSecretValueOutput{}
	if f.secretString != "" {
		out.SecretString = aws.String(f.secretString)
	}
	if len(f.binary) > 0 {
		out.SecretBinary = f.binary
	}
	return out, nil
}

func testPEM(t *testing.T) string {
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

func TestLoad_Success_StringAppID(t *testing.T) {
	keyPEM := testPEM(t)
	// app_id as a quoted string, private_key with escaped newlines survives JSON.
	sm := &fakeSM{secretString: `{
		"app_id": "424242",
		"client_id": "Iv1.abc",
		"client_secret": "shh",
		"private_key": ` + jsonQuote(keyPEM) + `
	}`}
	cfg, err := New(sm, "cainban/github-app").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppID != 424242 {
		t.Errorf("AppID = %d, want 424242", cfg.AppID)
	}
	if cfg.ClientID != "Iv1.abc" || cfg.ClientSecret != "shh" {
		t.Errorf("client creds mismatch: %q / %q", cfg.ClientID, cfg.ClientSecret)
	}
	if string(cfg.PrivateKeyPEM) != keyPEM {
		t.Error("private key PEM did not round-trip")
	}
	if sm.gotSecretID != "cainban/github-app" {
		t.Errorf("requested secret id = %q, want cainban/github-app", sm.gotSecretID)
	}
}

func TestLoad_Success_NumericAppID(t *testing.T) {
	keyPEM := testPEM(t)
	// app_id as a bare JSON number.
	sm := &fakeSM{secretString: `{"app_id": 777, "private_key": ` + jsonQuote(keyPEM) + `}`}
	cfg, err := New(sm, "s").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppID != 777 {
		t.Errorf("AppID = %d, want 777", cfg.AppID)
	}
}

func TestLoad_Errors(t *testing.T) {
	keyPEM := testPEM(t)
	cases := []struct {
		name  string
		sm    *fakeSM
		name2 string
	}{
		{"sm error", &fakeSM{err: errors.New("access denied")}, "s"},
		{"malformed json", &fakeSM{secretString: `{not json`}, "s"},
		{"missing app_id", &fakeSM{secretString: `{"private_key": ` + jsonQuote(keyPEM) + `}`}, "s"},
		{"missing private_key", &fakeSM{secretString: `{"app_id":"1"}`}, "s"},
		{"non-integer app_id", &fakeSM{secretString: `{"app_id":"abc","private_key": ` + jsonQuote(keyPEM) + `}`}, "s"},
		{"non-positive app_id", &fakeSM{secretString: `{"app_id":"0","private_key": ` + jsonQuote(keyPEM) + `}`}, "s"},
		{"neither string nor binary", &fakeSM{}, "s"},
		{"empty secret name", &fakeSM{secretString: `{"app_id":"1"}`}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.sm, tc.name2).Load(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoad_SecretBinary(t *testing.T) {
	keyPEM := testPEM(t)
	sm := &fakeSM{binary: []byte(`{"app_id":"9","private_key": ` + jsonQuote(keyPEM) + `}`)}
	cfg, err := New(sm, "s").Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppID != 9 {
		t.Errorf("AppID = %d, want 9", cfg.AppID)
	}
}

// jsonQuote wraps a string as a JSON string literal (escaping newlines etc.)
// without pulling in json.Marshal at every call site.
func jsonQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, string(r)...)
		}
	}
	out = append(out, '"')
	return string(out)
}
