package storage

import (
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var loginStripPattern = regexp.MustCompile(`[@.\-+]`)

var keyTimestampSequencer = struct {
	mu   sync.Mutex
	last map[string]time.Time
}{
	last: map[string]time.Time{},
}

func NormalizeOSKELLYLoginKey(login string) string {
	normalized := strings.ToLower(strings.TrimSpace(login))
	if normalized == "" {
		return "unknown"
	}

	parts := strings.SplitN(normalized, "@", 2)
	local := normalizeLoginPart(parts[0])
	if len(parts) == 1 {
		if local == "" {
			return "unknown"
		}
		return local
	}

	domain := normalizeLoginPart(parts[1])
	switch {
	case local == "" && domain == "":
		return "unknown"
	case local == "":
		return domain
	case domain == "":
		return local
	default:
		return local + "_" + domain
	}
}

func SessionPayloadPrefix(ownerKey string) string {
	return NormalizeOSKELLYLoginKey(ownerKey) + "/"
}

func SessionPayloadObjectKey(ownerKey string) string {
	return path.Join(NormalizeOSKELLYLoginKey(ownerKey), "latest.json")
}

func SessionHistoryObjectKey(ownerKey string, at time.Time) string {
	return path.Join(NormalizeOSKELLYLoginKey(ownerKey), "history", formatKeyTimestamp(at)+".json")
}

func ScreenshotObjectKey(ownerKey string, at time.Time) string {
	return path.Join(NormalizeOSKELLYLoginKey(ownerKey), "screenshot", formatKeyTimestamp(at)+".png")
}

func ExecutionArtifactObjectKey(ownerKey string, at time.Time) string {
	return path.Join(NormalizeOSKELLYLoginKey(ownerKey), "artifacts", formatKeyTimestamp(at)+".json")
}

func ResolveObjectOwnerKey(accountLogin string, accountID uuid.UUID) string {
	if strings.TrimSpace(accountLogin) != "" {
		return accountLogin
	}
	if accountID == uuid.Nil {
		return "unknown"
	}
	return accountID.String()
}

func NextUniqueKeyTimestamp(ownerKey string, objectKind string, now time.Time) time.Time {
	keyTimestampSequencer.mu.Lock()
	defer keyTimestampSequencer.mu.Unlock()

	base := now.UTC().Truncate(time.Second)
	sequenceKey := NormalizeOSKELLYLoginKey(ownerKey) + "|" + strings.TrimSpace(strings.ToLower(objectKind))
	if last, exists := keyTimestampSequencer.last[sequenceKey]; exists && !base.After(last) {
		base = last.Add(time.Second)
	}
	keyTimestampSequencer.last[sequenceKey] = base

	return base
}

func IsSessionObjectOwnedByAccount(accountID uuid.UUID, objectKey string) bool {
	normalized := path.Clean(strings.TrimSpace(objectKey))
	if normalized == "." || strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "../") {
		return false
	}

	accountPrefix := SessionPayloadPrefix(accountID.String())
	return strings.HasPrefix(normalized, accountPrefix) ||
		(strings.HasSuffix(normalized, "/latest.json") && strings.Count(normalized, "/") == 1)
}

func IsTaskArtifactOwnedByAccount(accountID uuid.UUID, objectKey string) bool {
	normalized := path.Clean(strings.TrimSpace(objectKey))
	if normalized == "." || strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "../") {
		return false
	}

	accountPrefix := SessionPayloadPrefix(accountID.String())
	if strings.HasPrefix(normalized, accountPrefix) {
		return true
	}

	if strings.Count(normalized, "/") < 2 {
		return false
	}

	return strings.Contains(normalized, "/screenshot/") || strings.Contains(normalized, "/artifacts/") || strings.Contains(normalized, "/history/")
}

func normalizeLoginPart(value string) string {
	return loginStripPattern.ReplaceAllString(strings.TrimSpace(value), "")
}

func formatKeyTimestamp(at time.Time) string {
	return at.UTC().Format("2006-01-02-150405")
}
