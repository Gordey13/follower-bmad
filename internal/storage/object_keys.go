package storage

import (
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
)

func SessionPayloadPrefix(accountID uuid.UUID) string {
	return path.Join("accounts", accountID.String(), "sessions") + "/"
}

func SessionPayloadObjectKey(accountID uuid.UUID, revision int64) string {
	return path.Join("accounts", accountID.String(), "sessions", fmt.Sprintf("%d.json", revision))
}

func TaskArtifactsPrefix(accountID uuid.UUID, taskID uuid.UUID, attempt int) string {
	return path.Join(
		"accounts",
		accountID.String(),
		"tasks",
		taskID.String(),
		"attempts",
		fmt.Sprintf("%d", attempt),
	) + "/"
}

func ScreenshotObjectKey(accountID uuid.UUID, taskID uuid.UUID, attempt int) string {
	return path.Join(
		"accounts",
		accountID.String(),
		"tasks",
		taskID.String(),
		"attempts",
		fmt.Sprintf("%d", attempt),
		"screenshots",
		"follow.png",
	)
}

func ExecutionArtifactObjectKey(
	accountID uuid.UUID,
	taskID uuid.UUID,
	attempt int,
	artifactName string,
) string {
	return path.Join(
		"accounts",
		accountID.String(),
		"tasks",
		taskID.String(),
		"attempts",
		fmt.Sprintf("%d", attempt),
		"artifacts",
		artifactName,
	)
}

func IsSessionObjectOwnedByAccount(accountID uuid.UUID, objectKey string) bool {
	normalized := path.Clean(strings.TrimSpace(objectKey))
	if normalized == "." || strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "../") {
		return false
	}

	return strings.HasPrefix(normalized, SessionPayloadPrefix(accountID))
}

func IsTaskArtifactOwnedByAccount(accountID uuid.UUID, objectKey string) bool {
	normalized := path.Clean(strings.TrimSpace(objectKey))
	if normalized == "." || strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "../") {
		return false
	}

	accountPrefix := path.Join("accounts", accountID.String()) + "/"
	return strings.HasPrefix(normalized, accountPrefix)
}
