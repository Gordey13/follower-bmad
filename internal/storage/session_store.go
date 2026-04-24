package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"follower/internal/domain"
	"follower/internal/stackerr"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

type sessionObjectClient interface {
	Put(ctx context.Context, bucket string, objectKey string, payload []byte) error
	Get(ctx context.Context, bucket string, objectKey string) ([]byte, error)
	Delete(ctx context.Context, bucket string, objectKey string) error
}

type MinioSessionObjectClient struct {
	client *minio.Client
}

func NewMinioSessionObjectClient(client *minio.Client) *MinioSessionObjectClient {
	return &MinioSessionObjectClient{client: client}
}

func (c *MinioSessionObjectClient) Put(ctx context.Context, bucket string, objectKey string, payload []byte) error {
	_, err := c.client.PutObject(
		ctx,
		bucket,
		objectKey,
		bytes.NewReader(payload),
		int64(len(payload)),
		minio.PutObjectOptions{ContentType: contentTypeForObject(objectKey, payload)},
	)
	return stackerr.WithStack(err)
}

func (c *MinioSessionObjectClient) Get(ctx context.Context, bucket string, objectKey string) ([]byte, error) {
	object, err := c.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, stackerr.WithStack(err)
	}
	defer object.Close()

	payload, err := io.ReadAll(object)
	if err != nil {
		if isMissingObjectError(err) {
			return nil, domain.NewDomainError(
				domain.ErrorCodeSessionPayloadMissing,
				fmt.Sprintf("session payload object %q is missing", objectKey),
			)
		}

		return nil, stackerr.WithStack(err)
	}

	return payload, nil
}

func (c *MinioSessionObjectClient) Delete(ctx context.Context, bucket string, objectKey string) error {
	return c.client.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{})
}

type SessionStore struct {
	client sessionObjectClient
	bucket string
}

func NewSessionStore(client sessionObjectClient, bucket string) *SessionStore {
	return &SessionStore{
		client: client,
		bucket: bucket,
	}
}

func (s *SessionStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	revision int64,
	payload []byte,
) (string, error) {
	if accountID == uuid.Nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"session payload save requires non-empty account id",
		)
	}
	if revision <= 0 {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidSessionRevision,
			fmt.Sprintf("session revision must be positive, got %d", revision),
		)
	}
	if !json.Valid(payload) {
		return "", domain.NewDomainError(
			domain.ErrorCodeSessionPayloadInvalid,
			"session payload must be valid JSON",
		)
	}

	ownerKey := ResolveObjectOwnerKey(accountLogin, accountID)
	objectKey := SessionPayloadObjectKey(ownerKey)
	if existingPayload, err := s.client.Get(ctx, s.bucket, objectKey); err == nil {
		historyObjectKey := SessionHistoryObjectKey(ownerKey, NextUniqueKeyTimestamp(ownerKey, "history", time.Now().UTC()))
		if err := s.client.Put(ctx, s.bucket, historyObjectKey, existingPayload); err != nil {
<<<<<<< HEAD
			return "", err
		}
	} else if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		return "", err
=======
			return "", stackerr.WithStack(err)
		}
	} else if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
		return "", stackerr.WithStack(err)
>>>>>>> 356ea5c (stackerr)
	}

	if err := s.client.Put(ctx, s.bucket, objectKey, payload); err != nil {
		return "", stackerr.WithStack(err)
	}

	return objectKey, nil
}

func (s *SessionStore) Load(ctx context.Context, accountID uuid.UUID, objectKey string) ([]byte, error) {
	if !IsSessionObjectOwnedByAccount(accountID, objectKey) {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionOwnershipMismatch,
			fmt.Sprintf("session object %q does not belong to account %s", objectKey, accountID.String()),
		)
	}

	payload, err := s.client.Get(ctx, s.bucket, objectKey)
	if err != nil {
		if domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing) {
			return nil, stackerr.WithStack(err)
		}
		return nil, stackerr.WithStack(err)
	}

	if !json.Valid(payload) {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionPayloadCorrupted,
			fmt.Sprintf("session object %q contains invalid JSON", objectKey),
		)
	}

	return payload, nil
}

func (s *SessionStore) Delete(ctx context.Context, objectKey string) error {
	return s.client.Delete(ctx, s.bucket, objectKey)
}

func isMissingObjectError(err error) bool {
	var minioErr minio.ErrorResponse
	if errors.As(err, &minioErr) {
		return minioErr.StatusCode == 404 ||
			minioErr.Code == "NoSuchKey" ||
			minioErr.Code == "NoSuchObject"
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such key") ||
		strings.Contains(message, "specified key does not exist") ||
		strings.Contains(message, "not found")
}

func contentTypeForObject(objectKey string, payload []byte) string {
	normalizedKey := strings.ToLower(strings.TrimSpace(objectKey))
	if strings.HasSuffix(normalizedKey, ".png") || bytes.HasPrefix(payload, []byte{0x89, 0x50, 0x4e, 0x47}) {
		return "image/png"
	}
	if strings.HasSuffix(normalizedKey, ".json") {
		return "application/json"
	}
	return "application/octet-stream"
}
