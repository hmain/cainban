// Package secrets loads cainban's GitHub App credentials from AWS Secrets
// Manager at RUNTIME. No secret value ever lives in code, the repository, or
// the CDK — the CDK only creates a PLACEHOLDER secret (see infra/stack.go) that
// an operator fills in after deploy (see docs/github-app-setup.md). This
// package reads that filled-in secret.
//
// # Shape of the secret
//
// The secret is a single JSON document holding every value the GitHub App
// needs, so one GetSecretValue call loads all of it:
//
//	{
//	  "app_id": "123456",              // GitHub App id (string or number)
//	  "client_id": "Iv1.abc123",       // App OAuth client id (for the P4.3 OAuth leg)
//	  "client_secret": "…",            // App OAuth client secret
//	  "private_key": "-----BEGIN RSA PRIVATE KEY-----\n…\n-----END RSA PRIVATE KEY-----\n"
//	}
//
// The secret NAME comes from an env var (default CAINBAN_GITHUB_APP_SECRET),
// set by the CDK stack. The private key is the App's PEM; app_id is accepted as
// a JSON string or number.
//
// # Mockable seam
//
// All Secrets Manager access goes through the API interface (exactly the one
// GetSecretValue method this package uses), so tests inject an in-memory fake
// and NO live AWS call is made in unit tests. This mirrors the grants package's
// dynamo API-interface pattern.
//
// It is PURE GO (no CGO).
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/hmain/cainban/src/systems/github"
)

// EnvSecretName is the env var naming the Secrets Manager secret that holds the
// GitHub App credentials. The CDK stack exports the placeholder secret's name
// and sets this on the Lambda(s) that need it.
const EnvSecretName = "CAINBAN_GITHUB_APP_SECRET"

// EnvRegion optionally overrides the region used to reach Secrets Manager;
// absent, standard AWS region resolution applies.
const EnvRegion = "CAINBAN_GITHUB_APP_SECRET_REGION"

// API is the subset of the Secrets Manager client this package uses — exactly
// GetSecretValue. Declaring it as an interface lets tests inject an in-memory
// fake (no live AWS), and keeps the production dependency to one call.
type API interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// Loader reads and parses the GitHub App credentials secret.
type Loader struct {
	client     API
	secretName string
}

// New builds a Loader from a live (or fake) Secrets Manager client and the
// secret name. An empty name is rejected on Load (not here) so a nil-config
// path is still constructible for tests.
func New(client API, secretName string) *Loader {
	return &Loader{client: client, secretName: secretName}
}

// NewFromEnv builds a production Loader: it reads the secret name from
// EnvSecretName, loads AWS config (honoring EnvRegion), and constructs a real
// Secrets Manager client. It returns an error when the secret name env is unset
// so a caller that REQUIRES the App creds fails loudly rather than silently
// loading nothing.
func NewFromEnv(ctx context.Context) (*Loader, error) {
	name := strings.TrimSpace(os.Getenv(EnvSecretName))
	if name == "" {
		return nil, fmt.Errorf("secrets: %s is not set", EnvSecretName)
	}
	var optFns []func(*awsconfig.LoadOptions) error
	if region := strings.TrimSpace(os.Getenv(EnvRegion)); region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("secrets: load AWS config: %w", err)
	}
	return New(secretsmanager.NewFromConfig(cfg), name), nil
}

// secretPayload is the on-the-wire JSON shape of the secret. app_id is decoded
// via json.Number so it accepts both a quoted string and a bare number.
type secretPayload struct {
	AppID        json.Number `json:"app_id"`
	ClientID     string      `json:"client_id"`
	ClientSecret string      `json:"client_secret"`
	PrivateKey   string      `json:"private_key"`
}

// Load fetches the secret and parses it into a github.AppConfig. It returns an
// error on a missing secret name, a Secrets Manager error, malformed JSON, or a
// missing required field (app_id, private_key). The returned AppConfig carries
// the private key as bytes ready for github.NewClient.
func (l *Loader) Load(ctx context.Context) (github.AppConfig, error) {
	var zero github.AppConfig
	if strings.TrimSpace(l.secretName) == "" {
		return zero, errors.New("secrets: empty secret name")
	}
	out, err := l.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &l.secretName,
	})
	if err != nil {
		return zero, fmt.Errorf("secrets: get secret value: %w", err)
	}
	raw, err := secretString(out)
	if err != nil {
		return zero, err
	}

	var p secretPayload
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&p); err != nil {
		return zero, fmt.Errorf("secrets: parse secret JSON: %w", err)
	}

	appIDStr := strings.TrimSpace(p.AppID.String())
	if appIDStr == "" {
		return zero, errors.New("secrets: secret is missing app_id")
	}
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return zero, fmt.Errorf("secrets: app_id %q is not an integer: %w", appIDStr, err)
	}
	if appID <= 0 {
		return zero, fmt.Errorf("secrets: app_id must be positive, got %d", appID)
	}
	if strings.TrimSpace(p.PrivateKey) == "" {
		return zero, errors.New("secrets: secret is missing private_key")
	}

	return github.AppConfig{
		AppID:         appID,
		PrivateKeyPEM: []byte(p.PrivateKey),
		ClientID:      p.ClientID,
		ClientSecret:  p.ClientSecret,
	}, nil
}

// secretString extracts the secret's string payload. Secrets Manager returns
// either SecretString (our case) or a binary SecretBinary; a secret with
// neither is malformed.
func secretString(out *secretsmanager.GetSecretValueOutput) (string, error) {
	if out == nil {
		return "", errors.New("secrets: nil GetSecretValue output")
	}
	if out.SecretString != nil && *out.SecretString != "" {
		return *out.SecretString, nil
	}
	if len(out.SecretBinary) > 0 {
		return string(out.SecretBinary), nil
	}
	return "", errors.New("secrets: secret has neither SecretString nor SecretBinary")
}
