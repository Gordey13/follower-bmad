package render

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

type OutputMode string

const (
	OutputModeTable OutputMode = "table"
	OutputModeJSON  OutputMode = "json"
)

func ParseOutputMode(raw string) (OutputMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		normalized = string(OutputModeTable)
	}

	switch OutputMode(normalized) {
	case OutputModeTable:
		return OutputModeTable, nil
	case OutputModeJSON:
		return OutputModeJSON, nil
	default:
		return "", fmt.Errorf("unsupported output mode %q (allowed: table,json)", raw)
	}
}

type Table struct {
	Headers []string
	Rows    [][]string
}

func WriteTable(w io.Writer, table Table) error {
	if len(table.Headers) == 0 {
		return errors.New("table headers are required")
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.Join(table.Headers, "\t")); err != nil {
		return err
	}

	for i, row := range table.Rows {
		if len(row) != len(table.Headers) {
			return fmt.Errorf(
				"row %d has %d columns, expected %d",
				i,
				len(row),
				len(table.Headers),
			)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}

	return tw.Flush()
}
