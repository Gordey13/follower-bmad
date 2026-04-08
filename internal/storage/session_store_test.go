package storage

import (
	"context"
	"errors"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestSessionStoreSaveAndLoad(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	payload := []byte(`{"cookies":[{"name":"sid","value":"abc"}]}`)
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	objectKey, err := store.Save(context.Background(), accountID, 1, payload)
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

func TestSessionStoreSaveRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	store := NewSessionStore(newFakeSessionObjectClient(), "artifacts")
	_, err := store.Save(context.Background(), uuid.New(), 1, []byte("{not-json"))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadInvalid) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadInvalid, err)
	}
}

func TestSessionStoreLoadRejectsOwnershipMismatch(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	otherAccountID := uuid.New()
	client := newFakeSessionObjectClient()
	store := NewSessionStore(client, "artifacts")

	payload := []byte(`{"origins":[]}`)
	objectKey, err := store.Save(context.Background(), otherAccountID, 1, payload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	_, err = store.Load(context.Background(), accountID, objectKey)
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
		SessionPayloadObjectKey(accountID, 1),
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

	objectKey := SessionPayloadObjectKey(accountID, 2)
	client.objects[objectKey] = []byte("{broken")

	_, err := store.Load(context.Background(), accountID, objectKey)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadCorrupted, err)
	}
}

func TestSessionPayloadObjectKeyLayout(t *testing.T) {
	t.Parallel()

	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000111")
	got := SessionPayloadObjectKey(accountID, 42)
	want := "accounts/00000000-0000-0000-0000-000000000111/sessions/42.json"

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
			object:   "accounts/a/sessions/1.json",
			payload:  []byte(`{"ok":true}`),
			expected: "application/json",
		},
		{
			name:     "png by extension",
			object:   "accounts/a/tasks/t/attempts/1/screenshots/follow.png",
			payload:  []byte("not-png-but-extension"),
			expected: "image/png",
		},
		{
			name:     "png by signature",
			object:   "accounts/a/tasks/t/attempts/1/screenshots/follow.bin",
			payload:  []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
			expected: "image/png",
		},
		{
			name:     "octet default",
			object:   "accounts/a/tasks/t/attempts/1/raw.dat",
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
	objects map[string][]byte
	getErr  error
}

func newFakeSessionObjectClient() *fakeSessionObjectClient {
	return &fakeSessionObjectClient{
		objects: map[string][]byte{},
	}
}

func (c *fakeSessionObjectClient) Put(ctx context.Context, bucket string, objectKey string, payload []byte) error {
	copied := make([]byte, len(payload))
	copy(copied, payload)
	c.objects[objectKey] = copied
	return nil
}

func (c *fakeSessionObjectClient) Get(ctx context.Context, bucket string, objectKey string) ([]byte, error) {
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
	if _, ok := c.objects[objectKey]; !ok {
		return errors.New("missing object")
	}
	delete(c.objects, objectKey)
	return nil
}
