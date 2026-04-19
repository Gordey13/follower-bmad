package httptransport

import (
	"strings"
	"testing"
)

func TestAdminCSVValidatorRejectsInvalidSchema(t *testing.T) {
	t.Parallel()

	validator := newAdminCSVValidator()
	_, err := validator.Validate(strings.NewReader("account,target\n1,https://oskelly.ru/profile/1\n"))
	if err == nil {
		t.Fatal("expected schema validation error")
	}

	var schemaErr *adminCSVSchemaError
	if !isAdminCSVSchemaError(err, &schemaErr) {
		t.Fatalf("expected adminCSVSchemaError, got %T", err)
	}
}

func TestAdminCSVValidatorReturnsRowErrorsWithDeterministicRowNumbers(t *testing.T) {
	t.Parallel()

	validator := newAdminCSVValidator()
	result, err := validator.Validate(strings.NewReader(strings.Join([]string{
		"account_id,target_profile",
		"00000000-0000-0000-0000-000000000001,https://oskelly.ru/profile/1001",
		"not-a-uuid,https://oskelly.ru/profile/1002",
		"00000000-0000-0000-0000-000000000003,https://oskelly.ru/profiles/bad",
	}, "\n")))
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if result.RowsTotal != 3 || result.RowsValid != 1 || result.RowsInvalid != 2 {
		t.Fatalf("unexpected counters: %+v", result)
	}
	if len(result.InvalidRows) != 2 {
		t.Fatalf("expected 2 invalid rows, got %d", len(result.InvalidRows))
	}

	if result.InvalidRows[0].Row != 3 {
		t.Fatalf("expected first invalid row index 3, got %d", result.InvalidRows[0].Row)
	}
	if result.InvalidRows[0].Field != "account_id" {
		t.Fatalf("expected account_id error, got %s", result.InvalidRows[0].Field)
	}
	if result.InvalidRows[0].Code != string(AdminErrorCodeCSVRowInvalid) {
		t.Fatalf("expected row code %s, got %s", AdminErrorCodeCSVRowInvalid, result.InvalidRows[0].Code)
	}

	if result.InvalidRows[1].Row != 4 {
		t.Fatalf("expected second invalid row index 4, got %d", result.InvalidRows[1].Row)
	}
	if result.InvalidRows[1].Field != "target_profile" {
		t.Fatalf("expected target_profile error, got %s", result.InvalidRows[1].Field)
	}
}

func TestAdminCSVValidatorInvalidOnlyPayloadHasNoValidRows(t *testing.T) {
	t.Parallel()

	validator := newAdminCSVValidator()
	result, err := validator.Validate(strings.NewReader(strings.Join([]string{
		"account_id,target_profile",
		"bad-uuid,https://oskelly.ru/profiles/invalid",
		"also-bad,   ",
	}, "\n")))
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if result.RowsTotal != 2 || result.RowsValid != 0 || result.RowsInvalid != 2 {
		t.Fatalf("unexpected counters: %+v", result)
	}
	if len(result.ValidRows) != 0 {
		t.Fatalf("expected no valid rows, got %d", len(result.ValidRows))
	}
}
