package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteTableRendersHeadersAndRows(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := WriteTable(&buf, Table{
		Headers: []string{"ID", "STATUS"},
		Rows: [][]string{
			{"task-1", "queued"},
			{"task-2", "fail"},
		},
	})
	if err != nil {
		t.Fatalf("WriteTable() error = %v", err)
	}

	out := buf.String()
	for _, want := range []string{"ID", "STATUS", "task-1", "queued", "task-2", "fail"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestWriteTableRejectsInvalidRowWidth(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := WriteTable(&buf, Table{
		Headers: []string{"ID", "STATUS"},
		Rows: [][]string{
			{"task-1"},
		},
	})
	if err == nil {
		t.Fatal("expected row width validation error, got nil")
	}
}
