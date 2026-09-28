package crypter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// fakeKMS is an in-memory KMS API: Encrypt prefixes a marker and records the
// encryption context; Decrypt reverses it and verifies the SAME context is
// supplied (mirroring KMS's AAD binding). No live AWS.
type fakeKMS struct {
	encErr  error
	decErr  error
	lastCtx map[string]string
}

func (f *fakeKMS) Encrypt(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	if f.encErr != nil {
		return nil, f.encErr
	}
	f.lastCtx = in.EncryptionContext
	blob := append([]byte("kms:"), in.Plaintext...)
	return &kms.EncryptOutput{CiphertextBlob: blob}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if f.decErr != nil {
		return nil, f.decErr
	}
	if !reflect.DeepEqual(in.EncryptionContext, f.lastCtx) {
		return nil, errors.New("fakeKMS: encryption context mismatch")
	}
	s := in.CiphertextBlob
	return &kms.DecryptOutput{Plaintext: s[len("kms:"):]}, nil
}

func TestNewKMS_Rejections(t *testing.T) {
	if _, err := NewKMS(nil, "key"); err == nil {
		t.Error("nil client must error")
	}
	if _, err := NewKMS(&fakeKMS{}, "  "); err == nil {
		t.Error("empty key id must error")
	}
}

func TestKMS_RoundTrip_WithContext(t *testing.T) {
	ctx := context.Background()
	k, err := NewKMS(&fakeKMS{}, "arn:aws:kms:eu-north-1:123:key/abc")
	if err != nil {
		t.Fatalf("NewKMS: %v", err)
	}
	ec := map[string]string{"subject": "sub-1", "purpose": "cainban-github-refresh-token"}
	ct, err := k.Encrypt(ctx, []byte("refresh-secret"), ec)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(ct) == "refresh-secret" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	pt, err := k.Decrypt(ctx, ct, ec)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(pt) != "refresh-secret" {
		t.Fatalf("plaintext = %q, want refresh-secret", pt)
	}
}

func TestKMS_DecryptWrongContext_Fails(t *testing.T) {
	ctx := context.Background()
	k, _ := NewKMS(&fakeKMS{}, "key")
	ct, _ := k.Encrypt(ctx, []byte("secret"), map[string]string{"subject": "sub-1"})
	if _, err := k.Decrypt(ctx, ct, map[string]string{"subject": "sub-2"}); err == nil {
		t.Fatal("decrypt with a different context must fail (AAD binding)")
	}
}

func TestKMS_ErrorsSurface(t *testing.T) {
	ctx := context.Background()
	k, _ := NewKMS(&fakeKMS{encErr: errors.New("kms boom")}, "key")
	if _, err := k.Encrypt(ctx, []byte("x"), nil); err == nil {
		t.Error("encrypt error must surface")
	}
	k2, _ := NewKMS(&fakeKMS{decErr: errors.New("kms boom")}, "key")
	if _, err := k2.Decrypt(ctx, []byte("kms:x"), nil); err == nil {
		t.Error("decrypt error must surface")
	}
}

func TestKMS_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	k, _ := NewKMS(&fakeKMS{}, "key")
	if _, err := k.Encrypt(ctx, nil, nil); err == nil {
		t.Error("empty plaintext must error")
	}
	if _, err := k.Decrypt(ctx, nil, nil); err == nil {
		t.Error("empty ciphertext must error")
	}
}
