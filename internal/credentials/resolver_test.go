package credentials

import (
	"context"
	"testing"

	"follower/internal/domain"
)

func TestResolveFromEnvReference(t *testing.T) {
	t.Parallel()

	resolver := NewResolverWithDeps(func(key string) (string, bool) {
		switch key {
		case "FOLLOWER_BOOTSTRAP_USER":
			return "user@example.com", true
		case "FOLLOWER_BOOTSTRAP_PASSWORD":
			return "super-secret", true
		default:
			return "", false
		}
	}, nil)

	credentials, err := resolver.Resolve(
		context.Background(),
		domain.CredentialSourceEnv,
		"env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if credentials.Username != "user@example.com" {
		t.Fatalf("expected username user@example.com, got %s", credentials.Username)
	}
	if credentials.Password != "super-secret" {
		t.Fatalf("expected resolved password, got %s", credentials.Password)
	}
}

func TestResolveManualSourceReturnsChallengeBlocked(t *testing.T) {
	t.Parallel()

	resolver := NewResolverWithDeps(nil, nil)
	_, err := resolver.Resolve(
		context.Background(),
		domain.CredentialSourceManual,
		"manual://legacy",
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAuthChallengeBlocked) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeAuthChallengeBlocked, err)
	}
}

func TestResolveFromFileReference(t *testing.T) {
	t.Parallel()

	resolver := NewResolverWithDeps(nil, func(path string) ([]byte, error) {
		return []byte(`{"username":"file-user","password":"file-pass"}`), nil
	})

	credentials, err := resolver.Resolve(
		context.Background(),
		domain.CredentialSourceFile,
		"file:///tmp/bootstrap-credentials.json",
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if credentials.Username != "file-user" {
		t.Fatalf("expected username file-user, got %s", credentials.Username)
	}
	if credentials.Password != "file-pass" {
		t.Fatalf("expected password file-pass, got %s", credentials.Password)
	}
}
