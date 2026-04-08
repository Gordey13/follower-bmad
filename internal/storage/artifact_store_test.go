package storage

import (
	"context"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestArtifactStoreSaveAndDelete(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	taskID := uuid.New()
	payload := []byte(`{"result":"ok"}`)

	client := newFakeSessionObjectClient()
	store := NewArtifactStore(client, "artifacts")

	objectKey, err := store.Save(context.Background(), accountID, taskID, 1, "execution.json", payload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !IsTaskArtifactOwnedByAccount(accountID, objectKey) {
		t.Fatalf("expected artifact key %q to be owned by account %s", objectKey, accountID.String())
	}

	if err := store.Delete(context.Background(), objectKey); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestArtifactStoreRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	store := NewArtifactStore(newFakeSessionObjectClient(), "artifacts")

	_, err := store.Save(context.Background(), uuid.New(), uuid.New(), 1, "execution.json", nil)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeArtifactPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeArtifactPersistFailed, err)
	}
}

func TestArtifactStoreRejectsUnsafeArtifactName(t *testing.T) {
	t.Parallel()

	store := NewArtifactStore(newFakeSessionObjectClient(), "artifacts")

	_, err := store.Save(context.Background(), uuid.New(), uuid.New(), 1, "../escape.json", []byte("{}"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeArtifactPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeArtifactPersistFailed, err)
	}
}
