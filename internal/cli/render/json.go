package render

import (
	"encoding/json"
	"io"
	"strings"
)

func WriteJSON(w io.Writer, payload any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}

type ErrorPayload struct {
	Code          string
	Message       string
	CorrelationID string
}

func WriteErrorJSON(w io.Writer, payload ErrorPayload) error {
	code := strings.TrimSpace(payload.Code)
	if code == "" {
		code = "CLI_ERROR"
	}

	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = "command failed"
	}

	result := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}

	correlationID := strings.TrimSpace(payload.CorrelationID)
	if correlationID != "" {
		result["meta"] = map[string]string{
			"correlation_id": correlationID,
		}
	}

	return WriteJSON(w, result)
}
