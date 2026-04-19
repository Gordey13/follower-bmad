package httptransport

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type adminCSVRowError struct {
	Row     int    `json:"row"`
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type adminCSVValidatedRow struct {
	Row           int
	AccountID     uuid.UUID
	TargetProfile string
}

type adminCSVValidationResult struct {
	RowsTotal   int                `json:"rows_total"`
	RowsValid   int                `json:"rows_valid"`
	RowsInvalid int                `json:"rows_invalid"`
	InvalidRows []adminCSVRowError `json:"invalid_rows"`
	ValidRows   []adminCSVValidatedRow
}

type adminCSVSchemaError struct {
	message string
	details map[string]any
}

func (e *adminCSVSchemaError) Error() string {
	return e.message
}

func (e *adminCSVSchemaError) Details() map[string]any {
	if e == nil || len(e.details) == 0 {
		return map[string]any{}
	}
	copied := make(map[string]any, len(e.details))
	for key, value := range e.details {
		copied[key] = value
	}
	return copied
}

func isAdminCSVSchemaError(err error, target **adminCSVSchemaError) bool {
	return errors.As(err, target)
}

type adminCSVValidator struct{}

func newAdminCSVValidator() adminCSVValidator {
	return adminCSVValidator{}
}

func (adminCSVValidator) Validate(reader io.Reader) (adminCSVValidationResult, error) {
	result := adminCSVValidationResult{
		InvalidRows: make([]adminCSVRowError, 0),
		ValidRows:   make([]adminCSVValidatedRow, 0),
	}

	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1

	header, err := csvReader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return result, newAdminCSVSchemaError(
				"CSV header is required",
				map[string]any{
					"expected_columns": []string{"account_id", "target_profile"},
				},
			)
		}
		return result, newAdminCSVSchemaError(
			"CSV schema could not be parsed",
			map[string]any{
				"reason": err.Error(),
			},
		)
	}

	if !isExpectedCSVHeader(header) {
		return result, newAdminCSVSchemaError(
			"CSV header must be exactly account_id,target_profile",
			map[string]any{
				"expected_columns": []string{"account_id", "target_profile"},
				"actual_columns":   normalizeCSVHeader(header),
			},
		)
	}

	for rowNumber := 2; ; rowNumber++ {
		record, readErr := csvReader.Read()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}

			return result, newAdminCSVSchemaError(
				"CSV row parsing failed",
				map[string]any{
					"row":    rowNumber,
					"reason": readErr.Error(),
				},
			)
		}

		result.RowsTotal++

		if len(record) != 2 {
			result.InvalidRows = append(result.InvalidRows, adminCSVRowError{
				Row:     rowNumber,
				Code:    string(AdminErrorCodeCSVRowInvalid),
				Field:   "row",
				Message: fmt.Sprintf("row must contain exactly 2 columns, got %d", len(record)),
			})
			continue
		}

		accountIDRaw := strings.TrimSpace(record[0])
		if _, parseErr := uuid.Parse(accountIDRaw); parseErr != nil {
			result.InvalidRows = append(result.InvalidRows, adminCSVRowError{
				Row:     rowNumber,
				Code:    string(AdminErrorCodeCSVRowInvalid),
				Field:   "account_id",
				Message: "account_id must be a valid UUID",
			})
			continue
		}

		targetProfileRaw := strings.TrimSpace(record[1])
		targetProfile, normalizeErr := domain.NormalizeOskellyTargetProfileURL(
			domain.TargetProfileDescriptor(targetProfileRaw),
		)
		if normalizeErr != nil {
			result.InvalidRows = append(result.InvalidRows, adminCSVRowError{
				Row:     rowNumber,
				Code:    string(AdminErrorCodeCSVRowInvalid),
				Field:   "target_profile",
				Message: "target_profile must match https://oskelly.ru/profile/<NUM>",
			})
			continue
		}

		accountID, _ := uuid.Parse(accountIDRaw)
		result.ValidRows = append(result.ValidRows, adminCSVValidatedRow{
			Row:           rowNumber,
			AccountID:     accountID,
			TargetProfile: targetProfile,
		})
	}

	result.RowsValid = len(result.ValidRows)
	result.RowsInvalid = len(result.InvalidRows)
	return result, nil
}

func newAdminCSVSchemaError(message string, details map[string]any) *adminCSVSchemaError {
	return &adminCSVSchemaError{
		message: message,
		details: details,
	}
}

func isExpectedCSVHeader(header []string) bool {
	normalized := normalizeCSVHeader(header)
	if len(normalized) != 2 {
		return false
	}
	return normalized[0] == "account_id" && normalized[1] == "target_profile"
}

func normalizeCSVHeader(header []string) []string {
	normalized := make([]string, len(header))
	for i := range header {
		normalized[i] = strings.TrimSpace(strings.ToLower(header[i]))
	}
	return normalized
}

func (result adminCSVValidationResult) report() adminCSVValidationReport {
	return adminCSVValidationReport{
		RowsTotal:   result.RowsTotal,
		RowsValid:   result.RowsValid,
		RowsInvalid: result.RowsInvalid,
		InvalidRows: result.InvalidRows,
	}
}

type adminCSVValidationReport struct {
	RowsTotal   int                `json:"rows_total"`
	RowsValid   int                `json:"rows_valid"`
	RowsInvalid int                `json:"rows_invalid"`
	InvalidRows []adminCSVRowError `json:"invalid_rows"`
}
