package storage

import (
	"context"
	"fmt"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type ScreenshotStore struct {
	client sessionObjectClient
	bucket string
}

func NewScreenshotStore(client sessionObjectClient, bucket string) *ScreenshotStore {
	return &ScreenshotStore{
		client: client,
		bucket: bucket,
	}
}

func (s *ScreenshotStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	payload []byte,
) (string, error) {
	if accountID == uuid.Nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"screenshot save requires non-empty account id",
		)
	}
	if len(payload) == 0 {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			"screenshot payload must not be empty",
		)
	}

	ownerKey := ResolveObjectOwnerKey(accountLogin, accountID)
	objectKey := ScreenshotObjectKey(
		ownerKey,
		NextUniqueKeyTimestamp(ownerKey, "screenshot", time.Now()),
	)
	if err := s.client.Put(ctx, s.bucket, objectKey, payload); err != nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("save screenshot object %q: %v", objectKey, err),
		)
	}

	return objectKey, nil
}

func (s *ScreenshotStore) Load(
	ctx context.Context,
	accountID uuid.UUID,
	objectKey string,
) ([]byte, error) {
	if !IsTaskArtifactOwnedByAccount(accountID, objectKey) {
		return nil, domain.NewDomainError(
			domain.ErrorCodeSessionOwnershipMismatch,
			fmt.Sprintf("screenshot object %q does not belong to account %s", objectKey, accountID.String()),
		)
	}

	payload, err := s.client.Get(ctx, s.bucket, objectKey)
	if err != nil {
		return nil, domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("load screenshot object %q: %v", objectKey, err),
		)
	}

	return payload, nil
}

func (s *ScreenshotStore) Delete(ctx context.Context, objectKey string) error {
	return s.client.Delete(ctx, s.bucket, objectKey)
}
