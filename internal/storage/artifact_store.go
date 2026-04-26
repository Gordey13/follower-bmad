package storage

import (
	"context"
	"fmt"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type ArtifactStore struct {
	client sessionObjectClient
	bucket string
}

func NewArtifactStore(client sessionObjectClient, bucket string) *ArtifactStore {
	return &ArtifactStore{
		client: client,
		bucket: bucket,
	}
}

func (s *ArtifactStore) Save(
	ctx context.Context,
	accountID uuid.UUID,
	accountLogin string,
	payload []byte,
) (string, error) {
	if accountID == uuid.Nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"artifact save requires non-empty account id",
		)
	}
	if len(payload) == 0 {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			"artifact payload must not be empty",
		)
	}

	ownerKey := ResolveObjectOwnerKey(accountLogin, accountID)
	objectKey := ExecutionArtifactObjectKey(
		ownerKey,
		NextUniqueKeyTimestamp(ownerKey, "artifact", time.Now()),
	)
	if err := s.client.Put(ctx, s.bucket, objectKey, payload); err != nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("save artifact object %q: %v", objectKey, err),
		)
	}

	return objectKey, nil
}

func (s *ArtifactStore) Delete(ctx context.Context, objectKey string) error {
	return s.client.Delete(ctx, s.bucket, objectKey)
}
