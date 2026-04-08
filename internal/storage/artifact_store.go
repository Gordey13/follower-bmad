package storage

import (
	"context"
	"fmt"
	"path"
	"strings"

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
	taskID uuid.UUID,
	attempt int,
	artifactName string,
	payload []byte,
) (string, error) {
	if accountID == uuid.Nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidAccountIdentifier,
			"artifact save requires non-empty account id",
		)
	}
	if taskID == uuid.Nil {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidTaskIdentifier,
			"artifact save requires non-empty task id",
		)
	}
	if attempt <= 0 {
		return "", domain.NewDomainError(
			domain.ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("artifact save attempt must be > 0, got %d", attempt),
		)
	}
	if len(payload) == 0 {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			"artifact payload must not be empty",
		)
	}

	safeName, err := sanitizeArtifactName(artifactName)
	if err != nil {
		return "", err
	}

	objectKey := ExecutionArtifactObjectKey(accountID, taskID, attempt, safeName)
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

func sanitizeArtifactName(name string) (string, error) {
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			"artifact name must not be empty",
		)
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "..") {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("artifact name %q is unsafe", name),
		)
	}
	if strings.Contains(cleaned, "/") {
		return "", domain.NewDomainError(
			domain.ErrorCodeArtifactPersistFailed,
			fmt.Sprintf("artifact name %q must not contain path separators", name),
		)
	}

	return cleaned, nil
}
