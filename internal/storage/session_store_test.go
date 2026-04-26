package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestSessionStoreSaveAndLoad(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	accountLogin := "gm-liker@yandex.ru"
	payload := []byte(`{"cookies":[{"name":"sid","value":"abc"}]}`)
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	objectKey, err := store.Save(context.Background(), accountID, accountLogin, 1, payload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load(context.Background(), accountID, objectKey)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if string(got) != string(payload) {
		t.Fatalf("expected payload %q, got %q", payload, got)
	}
}

func TestSessionStoreSaveArchivesPreviousLatest(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	accountLogin := "gm-liker@yandex.ru"
	oldPayload := []byte(`{"cookies":[{"name":"sid","value":"old"}]}`)
	newPayload := []byte(`{"cookies":[{"name":"sid","value":"new"}]}`)
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	latestKey := SessionPayloadObjectKey(accountLogin)
	client.objects[latestKey] = oldPayload

	objectKey, err := store.Save(context.Background(), accountID, accountLogin, 2, newPayload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if objectKey != latestKey {
		t.Fatalf("expected latest key %q, got %q", latestKey, objectKey)
	}

	historyPayload, historyCount := findHistoryPayloads(t, client.objects, "gmliker_yandexru/history/")
	if historyCount != 1 {
		t.Fatalf("expected 1 history object, got %d", historyCount)
	}
	if string(historyPayload) != string(oldPayload) {
		t.Fatalf("expected archived payload %q, got %q", oldPayload, historyPayload)
	}

	latestPayload, ok := client.objects[latestKey]
	if !ok {
		t.Fatalf("expected latest object %q to be saved", latestKey)
	}
	if string(latestPayload) != string(newPayload) {
		t.Fatalf("expected latest payload %q, got %q", newPayload, latestPayload)
	}
}

func TestSessionStoreSaveDoesNotArchiveWhenLatestMissing(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	accountLogin := "gm-liker@yandex.ru"
	payload := []byte(`{"cookies":[{"name":"sid","value":"new"}]}`)
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	if _, err := store.Save(context.Background(), accountID, accountLogin, 1, payload); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	_, historyCount := findHistoryPayloads(t, client.objects, "gmliker_yandexru/history/")
	if historyCount != 0 {
		t.Fatalf("expected no history objects when no previous latest exists, got %d", historyCount)
	}
}

func findHistoryPayloads(t *testing.T, objects map[string][]byte, historyPrefix string) ([]byte, int) {
	t.Helper()

	var payload []byte
	count := 0
	for key, value := range objects {
		if strings.HasPrefix(key, historyPrefix) && strings.HasSuffix(key, ".json") {
			copied := make([]byte, len(value))
			copy(copied, value)
			payload = copied
			count++
		}
	}

	return payload, count
}

func TestSessionStoreSaveRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	store := NewSessionStore(newFakeSessionObjectClient(), "artifacts")
	_, err := store.Save(context.Background(), uuid.New(), "", 1, []byte("{not-json"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadInvalid) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadInvalid, err)
	}
}

func TestSessionStoreLoadRejectsOwnershipMismatch(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	_, err := store.Load(context.Background(), accountID, "other_user/history/2026-04-23-101112.json")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionOwnershipMismatch) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionOwnershipMismatch, err)
	}
}

func TestSessionStoreLoadMissingObject(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	client := newFakeSessionObjectClient()
	client.getErr = domain.NewDomainError(
		domain.ErrorCodeSessionPayloadMissing,
		"missing payload",
	)
	store := NewSessionStore(client, "artifacts")

	_, err := store.Load(
		context.Background(),
		accountID,
		SessionPayloadObjectKey("test.user@example.com"),
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
}

func TestSessionStoreLoadCorruptedObject(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	objectKey := SessionPayloadObjectKey("user@example.com")
	client.objects[objectKey] = []byte("{broken")

	_, err := store.Load(context.Background(), accountID, objectKey)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
}

func TestSessionPayloadObjectKeyLayout(t *testing.T) {
	t.Parallel()

	got := SessionPayloadObjectKey("gm-liker@yandex.ru")
	want := "gmliker_yandexru/latest.json"

	if got != want {
		t.Fatalf("expected key %q, got %q", want, got)
	}
}

func TestSessionHistoryObjectKeyLayout(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 4, 23, 10, 11, 12, 0, time.UTC)
	got := SessionHistoryObjectKey("gm-liker@yandex.ru", at)
	want := "gmliker_yandexru/history/2026-04-23-101112.json"

	if got != want {
		t.Fatalf("expected key %q, got %q", want, got)
	}
}

func TestContentTypeForObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		object   string
		payload  []byte
		expected string
	}{
		{
			name:     "json by extension",
			object:   "a/latest.json",
			payload:  []byte(`{"ok":true}`),
			expected: "application/json",
		},
		{
			name:     "png by extension",
			object:   "a/screenshot/2026-04-23-100000.png",
			payload:  []byte("not-png-but-extension"),
			expected: "image/png",
		},
		{
			name:     "png by signature",
			object:   "a/screenshot/2026-04-23-100000.bin",
			payload:  []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
			expected: "image/png",
		},
		{
			name:     "octet default",
			object:   "a/raw.dat",
			payload:  []byte("raw"),
			expected: "application/octet-stream",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := contentTypeForObject(tt.object, tt.payload)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

type fakeSessionObjectClient struct {
	mu      sync.RWMutex
	objects map[string][]byte
	getErr  error
}

func newFakeSessionObjectClient() *fakeSessionObjectClient {
	return &fakeSessionObjectClient{
		objects: map[string][]byte{},
	}
}

func (c *fakeSessionObjectClient) Put(ctx context.Context, bucket string, objectKey string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := make([]byte, len(payload))
	copy(copied, payload)
	c.objects[objectKey] = copied
	return nil
}

func (c *fakeSessionObjectClient) Get(ctx context.Context, bucket string, objectKey string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.getErr != nil {
		return nil, c.getErr
	}

	payload, ok := c.objects[objectKey]
	if !ok {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionPayloadMissing,
			"missing payload",
		)
	}

	copied := make([]byte, len(payload))
	copy(copied, payload)
	return copied, nil
}

func (c *fakeSessionObjectClient) Delete(ctx context.Context, bucket string, objectKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.objects[objectKey]; !ok {
		return errors.New("missing object")
	}
	delete(c.objects, objectKey)
	return nil
}
