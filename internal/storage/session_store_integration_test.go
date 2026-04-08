package storage

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func TestSessionStoreWithMinIO(t *testing.T) {
	client, bucket := mustOpenMinioForIntegration(t)
	store := NewSessionStore(NewMinioSessionObjectClient(client), bucket)

	accountID := uuid.New()
	payload := []byte(`{"cookies":[{"name":"sid","value":"integration"}]}`)

	objectKey, err := store.Save(context.Background(), accountID, 1, payload)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	gotPayload, err := store.Load(context.Background(), accountID, objectKey)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(gotPayload) != string(payload) {
		t.Fatalf("expected payload %q, got %q", payload, gotPayload)
	}
}

func TestSessionStoreWithMinIOMissingObject(t *testing.T) {
	client, bucket := mustOpenMinioForIntegration(t)
	store := NewSessionStore(NewMinioSessionObjectClient(client), bucket)

	accountID := uuid.New()
	missingObjectKey := SessionPayloadObjectKey(accountID, 99999)

	_, err := store.Load(context.Background(), accountID, missingObjectKey)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeSessionPayloadMissing, err)
	}
}

func mustOpenMinioForIntegration(t *testing.T) (*minio.Client, string) {
	t.Helper()

	endpoint := os.Getenv("FOLLOWER_TEST_MINIO_ENDPOINT")
	accessKey := os.Getenv("FOLLOWER_TEST_MINIO_ACCESS_KEY")
	secretKey := os.Getenv("FOLLOWER_TEST_MINIO_SECRET_KEY")
	bucket := os.Getenv("FOLLOWER_TEST_MINIO_BUCKET")

	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		t.Skip(
			"skipping integration test, FOLLOWER_TEST_MINIO_ENDPOINT/FOLLOWER_TEST_MINIO_ACCESS_KEY/" +
				"FOLLOWER_TEST_MINIO_SECRET_KEY/FOLLOWER_TEST_MINIO_BUCKET must be set",
		)
	}

	useSSL, err := strconv.ParseBool(os.Getenv("FOLLOWER_TEST_MINIO_USE_SSL"))
	if err != nil {
		useSSL = false
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		t.Skipf("skipping integration test, cannot create minio client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		t.Skipf("skipping integration test, minio bucket check failed: %v", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			t.Skipf("skipping integration test, cannot create minio bucket: %v", err)
		}
	}

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer healthCancel()
	_, err = client.ListBuckets(healthCtx)
	if err != nil {
		t.Skipf("skipping integration test, minio unavailable: %v", err)
	}

	return client, bucket
}
