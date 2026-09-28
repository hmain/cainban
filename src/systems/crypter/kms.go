// Package crypter provides the small-secret encryption seam the connect flow
// uses to store a user's GitHub OAuth refresh token WITHOUT ever writing the
// plaintext to DynamoDB. The grants store depends on the grants.Crypter
// interface (Encrypt/Decrypt over bytes with an encryption context); this
// package supplies the production KMS implementation.
//
// # Why KMS direct-encrypt (not envelope-in-code)
//
// The refresh token is small (well under KMS's 4 KB direct-encrypt limit), so a
// single kms:Encrypt / kms:Decrypt round-trip is the simplest correct option:
// no data-key management in code, the key never leaves KMS, and the connect
// Lambda's IAM is scoped to exactly kms:Encrypt + kms:Decrypt on ONE key ARN.
// The subject is passed as KMS EncryptionContext (AAD), so a ciphertext is
// cryptographically bound to the subject it was written for — a ciphertext
// copied onto another subject's item fails to decrypt.
//
// It is PURE GO (no CGO). All KMS access goes through the API interface (the
// two calls this package makes), so tests inject an in-memory fake and no live
// AWS call is made.
package crypter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// API is the subset of the KMS client this package uses — exactly Encrypt and
// Decrypt. Declaring it as an interface lets tests inject an in-memory fake.
type API interface {
	Encrypt(ctx context.Context, in *kms.EncryptInput, optFns ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// KMS encrypts/decrypts small secrets with a single KMS customer-managed key
// via direct kms:Encrypt / kms:Decrypt. It satisfies grants.Crypter.
type KMS struct {
	client API
	keyID  string
}

// NewKMS builds a KMS crypter from a KMS client and a key id/ARN. An empty key
// id is rejected — there is no unkeyed/plaintext path.
func NewKMS(client API, keyID string) (*KMS, error) {
	if client == nil {
		return nil, errors.New("crypter: nil KMS client")
	}
	if strings.TrimSpace(keyID) == "" {
		return nil, errors.New("crypter: empty KMS key id")
	}
	return &KMS{client: client, keyID: keyID}, nil
}

// Encrypt returns the KMS ciphertext blob for plaintext, bound to
// encryptionContext (KMS EncryptionContext / AAD). The plaintext is never
// logged.
func (k *KMS) Encrypt(ctx context.Context, plaintext []byte, encryptionContext map[string]string) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("crypter: encrypt: empty plaintext")
	}
	out, err := k.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:             aws.String(k.keyID),
		Plaintext:         plaintext,
		EncryptionContext: encryptionContext,
	})
	if err != nil {
		return nil, fmt.Errorf("crypter: kms encrypt: %w", err)
	}
	if len(out.CiphertextBlob) == 0 {
		return nil, errors.New("crypter: kms encrypt returned empty ciphertext")
	}
	return out.CiphertextBlob, nil
}

// Decrypt returns the plaintext for a KMS ciphertext blob. The same
// encryptionContext used at Encrypt time MUST be supplied or KMS refuses the
// decrypt (this is what binds a ciphertext to its subject). The plaintext is
// never logged.
func (k *KMS) Decrypt(ctx context.Context, ciphertext []byte, encryptionContext map[string]string) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("crypter: decrypt: empty ciphertext")
	}
	out, err := k.client.Decrypt(ctx, &kms.DecryptInput{
		KeyId:             aws.String(k.keyID),
		CiphertextBlob:    ciphertext,
		EncryptionContext: encryptionContext,
	})
	if err != nil {
		return nil, fmt.Errorf("crypter: kms decrypt: %w", err)
	}
	if len(out.Plaintext) == 0 {
		return nil, errors.New("crypter: kms decrypt returned empty plaintext")
	}
	return out.Plaintext, nil
}
