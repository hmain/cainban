package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// LoadStringSecret reads a plain string secret from Secrets Manager by name.
// It caches the result so repeated calls don't hit AWS.
var (
	stringSecretCache   = make(map[string]string)
	stringSecretCacheMu sync.RWMutex
)

// GetStringSecret returns a cached string secret. It first checks if an env var
// with "_NAME" suffix is set, and if so, loads the secret from Secrets Manager.
// Otherwise it falls back to the direct env var without "_NAME" suffix (for local
// development and testing). E.g. for nameEnvVar="CAINBAN_BADGE_SECRET_NAME":
//  1. If CAINBAN_BADGE_SECRET_NAME is set → load from Secrets Manager
//  2. Else if CAINBAN_BADGE_SECRET is set → use that value directly
//  3. Else → return empty (feature disabled)
func GetStringSecret(ctx context.Context, nameEnvVar string) string {
	secretName := strings.TrimSpace(os.Getenv(nameEnvVar))

	// Fallback: if no _NAME env var, check for direct value env var (local dev)
	if secretName == "" {
		fallbackVar := strings.TrimSuffix(nameEnvVar, "_NAME")
		if fallbackVar != nameEnvVar {
			return strings.TrimSpace(os.Getenv(fallbackVar))
		}
		return ""
	}

	stringSecretCacheMu.RLock()
	if v, ok := stringSecretCache[secretName]; ok {
		stringSecretCacheMu.RUnlock()
		return v
	}
	stringSecretCacheMu.RUnlock()

	// Load from Secrets Manager
	stringSecretCacheMu.Lock()
	defer stringSecretCacheMu.Unlock()

	// Double-check after acquiring write lock
	if v, ok := stringSecretCache[secretName]; ok {
		return v
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Printf("secrets: failed to load AWS config for %s: %v\n", nameEnvVar, err)
		stringSecretCache[secretName] = ""
		return ""
	}

	client := secretsmanager.NewFromConfig(cfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		fmt.Printf("secrets: failed to get secret %s: %v\n", secretName, err)
		stringSecretCache[secretName] = ""
		return ""
	}

	value := ""
	if out.SecretString != nil {
		value = *out.SecretString
	}
	stringSecretCache[secretName] = value
	return value
}
