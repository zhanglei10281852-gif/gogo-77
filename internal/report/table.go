// Package report renders analysis output as deterministic plain text.
//
// The JSON output mode is produced by the strictjson encoder, so this package
// only concerns itself with the human-readable form. Every table computes its
// column widths from the cells it was given, pads with spaces, and never reads
// terminal width or locale, so the same analysis produces byte-identical text on
// every machine. Numbers are formatted here and nowhere else.
package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Align says how a column's cells sit inside their width.
type Align int

// The two alignments.
const (
	AlignLeft Align = iota
	AlignRight
)

// Table is a fixed-width text table.
type Table struct {
	headers []string
	aligns  []Align
	rows    [][]string
}

// NewTable starts a table with the given headers, all left aligned.
func NewTable(headers ...string) *Table {
	aligns := make([]Align, len(headers))
	return &Table{headers: headers, aligns: aligns}
}

// RightAlign marks the given zero-based columns as right aligned.
func (t *Table) RightAlign(columns ...int) *Table {
	for _, column := range columns {
		if column >= 0 && column < len(t.aligns) {
			t.aligns[column] = AlignRight
		}
	}
	return t
}

// Add appends one row. Rows shorter than the header are padded with empty cells
// and longer rows are truncated, so a caller mistake shows up as a blank cell
// rather than a panic in the middle of a report.
func (t *Table) Add(cells ...string) *Table {
	row := make([]string, len(t.headers))
	for index := range row {
		if index < len(cells) {
			row[index] = cells[index]
		}
	}
	t.rows = append(t.rows, row)
	return t
}

// Rows is the number of data rows added so far.
func (t *Table) Rows() int { return len(t.rows) }

// Render returns the table as lines: a header, a rule, then the rows.
func (t *Table) Render() []string {
	widths := make([]int, len(t.headers))
	for index, header := range t.headers {
		widths[index] = len(header)
	}
	for _, row := range t.rows {
		for index, cell := range row {
			if len(cell) > widths[index] {
				widths[index] = len(cell)
			}
		}
	}
	lines := make([]string, 0, len(t.rows)+2)
	lines = append(lines, t.renderRow(t.headers, widths))
	rule := make([]string, len(widths))
	for index, width := range widths {
		rule[index] = strings.Repeat("-", width)
	}
	lines = append(lines, t.renderRow(rule, widths))
	for _, row := range t.rows {
		lines = append(lines, t.renderRow(row, widths))
	}
	return lines
}

func (t *Table) renderRow(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for index, cell := range cells {
		width := widths[index]
		if t.aligns[index] == AlignRight {
			parts[index] = strings.Repeat(" ", maxInt(0, width-len(cell))) + cell
		} else {
			parts[index] = cell + strings.Repeat(" ", maxInt(0, width-len(cell)))
		}
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Sci renders a copy number in a compact form: plain digits for small magnitudes
// and scientific notation with three significant figures above a million, which
// is where surveillance loads live.
func Sci(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	if value == 0 {
		return "0"
	}
	magnitude := math.Abs(value)
	switch {
	case magnitude >= 1e6 || magnitude < 1e-3:
		return strconv.FormatFloat(value, 'e', 3, 64)
	case magnitude >= 1000:
		return strconv.FormatFloat(value, 'f', 0, 64)
	default:
		return trimZeros(strconv.FormatFloat(value, 'f', 3, 64))
	}
}

func trimZeros(text string) string {
	if !strings.Contains(text, ".") {
		return text
	}
	text = strings.TrimRight(text, "0")
	return strings.TrimSuffix(text, ".")
}

// Fixed renders a value with a fixed number of decimals, or "n/a" when it is not
// a real number.
func Fixed(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

// YesNo renders a flag as a short word.
func YesNo(flag bool) string {
	if flag {
		return "yes"
	}
	return "no"
}

// Dash returns text, or "-" when text is empty, so a table never shows a gap
// that could be mistaken for a missing column.
func Dash(text string) string {
	if strings.TrimSpace(text) == "" {
		return "-"
	}
	return text
}

// JoinOrDash joins parts with a comma, or renders "-" for an empty list.
func JoinOrDash(parts []string) string {
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// Section renders a titled block: a heading, an underline and the body lines.
func Section(title string, body []string) []string {
	lines := make([]string, 0, len(body)+3)
	lines = append(lines, title)
	lines = append(lines, strings.Repeat("=", len(title)))
	if len(body) == 0 {
		lines = append(lines, "(nothing to report)")
	} else {
		lines = append(lines, body...)
	}
	lines = append(lines, "")
	return lines
}

// KeyValues renders aligned key and value pairs in the order supplied.
func KeyValues(pairs [][2]string) []string {
	width := 0
	for _, pair := range pairs {
		if len(pair[0]) > width {
			width = len(pair[0])
		}
	}
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, pair[0], pair[1]))
	}
	return lines
}

// Join renders a slice of lines as one newline-terminated block.
func Join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
