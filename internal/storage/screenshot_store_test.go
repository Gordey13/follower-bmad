package storage

import (
	"context"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestScreenshotStoreSaveAndLoad(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	taskID := uuid.New()
	payload := []byte("fake-png")

	client := newFakeSessionObjectClient()
	store := NewScreenshotStore(client, "artifacts")

	objectKey, err := store.Save(context.Background(), accountID, taskID, 1, payload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !IsTaskArtifactOwnedByAccount(accountID, objectKey) {
		t.Fatalf("expected screenshot key %q to be owned by account %s", objectKey, accountID.String())
	}

	loaded, err := store.Load(context.Background(), accountID, objectKey)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(loaded) != string(payload) {
		t.Fatalf("expected payload %q, got %q", payload, loaded)
	}
}

func TestScreenshotStoreRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	store := NewScreenshotStore(newFakeSessionObjectClient(), "artifacts")

	_, err := store.Save(context.Background(), uuid.Nil, uuid.New(), 1, []byte("png"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidAccountIdentifier) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidAccountIdentifier, err)
	}

	_, err = store.Save(context.Background(), uuid.New(), uuid.Nil, 1, []byte("png"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskIdentifier) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidTaskIdentifier, err)
	}

	_, err = store.Save(context.Background(), uuid.New(), uuid.New(), 0, []byte("png"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskTransition) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidTaskTransition, err)
	}
}

func TestTaskArtifactObjectKeyLayout(t *testing.T) {
	t.Parallel()

	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000111")
	taskID := uuid.MustParse("00000000-0000-0000-0000-000000000222")

	gotScreenshot := ScreenshotObjectKey(accountID, taskID, 3)
	wantScreenshot := "accounts/00000000-0000-0000-0000-000000000111/tasks/00000000-0000-0000-0000-000000000222/attempts/3/screenshots/follow.png"
	if gotScreenshot != wantScreenshot {
		t.Fatalf("expected screenshot key %q, got %q", wantScreenshot, gotScreenshot)
	}

	gotArtifact := ExecutionArtifactObjectKey(accountID, taskID, 3, "execution.json")
	wantArtifact := "accounts/00000000-0000-0000-0000-000000000111/tasks/00000000-0000-0000-0000-000000000222/attempts/3/artifacts/execution.json"
	if gotArtifact != wantArtifact {
		t.Fatalf("expected artifact key %q, got %q", wantArtifact, gotArtifact)
	}
}
