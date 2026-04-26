package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestScreenshotStoreSaveAndLoad(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	payload := []byte("fake-png")

	client := newFakeSessionObjectClient()
	store := NewScreenshotStore(client, "artifacts")

	objectKey, err := store.Save(context.Background(), accountID, "gm-liker@yandex.ru", payload)
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

	_, err := store.Save(context.Background(), uuid.Nil, "user@example.com", []byte("png"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidAccountIdentifier) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidAccountIdentifier, err)
	}

	_, err = store.Save(context.Background(), uuid.New(), "user@example.com", nil)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeArtifactPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeArtifactPersistFailed, err)
	}
}

func TestTaskArtifactObjectKeyLayout(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC)

	gotScreenshot := ScreenshotObjectKey("gm-liker@yandex.ru", at)
	wantScreenshot := "gmliker_yandexru/screenshot/2026-04-23-101112.png"
	if gotScreenshot != wantScreenshot {
		t.Fatalf("expected screenshot key %q, got %q", wantScreenshot, gotScreenshot)
	}

	gotArtifact := ExecutionArtifactObjectKey("gm-liker@yandex.ru", at)
	wantArtifact := "gmliker_yandexru/artifacts/2026-04-23-101112.json"
	if gotArtifact != wantArtifact {
		t.Fatalf("expected artifact key %q, got %q", wantArtifact, gotArtifact)
	}
}

func TestNormalizeOSKELLYLoginKey(t *testing.T) {
	t.Parallel()

	got := NormalizeOSKELLYLoginKey("gm-liker@yandex.ru")
	if got != "gmliker_yandexru" {
		t.Fatalf("expected normalized login key gmliker_yandexru, got %q", got)
	}
}

func TestScreenshotAndArtifactParallelWritesHaveNoCollisions(t *testing.T) {
	t.Parallel()

	const workers = 20
	accountID := uuid.New()
	accountLogin := "parallel.user+1@yandex.ru"
	client := newFakeSessionObjectClient()
	screenshotStore := NewScreenshotStore(client, "artifacts")
	artifactStore := NewArtifactStore(client, "artifacts")

	screenshotKeys := make(chan string, workers)
	artifactKeys := make(chan string, workers)
	errCh := make(chan error, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			screenshotKey, screenshotErr := screenshotStore.Save(
				context.Background(),
				accountID,
				accountLogin,
				[]byte("fake-png"),
			)
			if screenshotErr != nil {
				errCh <- screenshotErr
				return
			}
			screenshotKeys <- screenshotKey

			artifactKey, artifactErr := artifactStore.Save(
				context.Background(),
				accountID,
				accountLogin,
				[]byte(`{"result":"ok"}`),
			)
			if artifactErr != nil {
				errCh <- artifactErr
				return
			}
			artifactKeys <- artifactKey
		}()
	}

	wg.Wait()
	close(errCh)
	close(screenshotKeys)
	close(artifactKeys)

	for err := range errCh {
		if err != nil {
			t.Fatalf("parallel save failed: %v", err)
		}
	}

	seenScreenshot := make(map[string]struct{}, workers)
	for key := range screenshotKeys {
		seenScreenshot[key] = struct{}{}
	}
	if len(seenScreenshot) != workers {
		t.Fatalf("expected %d unique screenshot keys, got %d", workers, len(seenScreenshot))
	}

	seenArtifact := make(map[string]struct{}, workers)
	for key := range artifactKeys {
		seenArtifact[key] = struct{}{}
	}
	if len(seenArtifact) != workers {
		t.Fatalf("expected %d unique artifact keys, got %d", workers, len(seenArtifact))
	}
}
