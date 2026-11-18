package dataimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var (
	ErrTooLarge    = errors.New("this file is larger than 10 MB")
	ErrTooManyRows = fmt.Errorf("this file has more than %d rows; split it into smaller files", MaxRows)
)

// decodeText returns UTF-8 text from UTF-8 (with or without BOM) or UTF-16 (BOM, or '<' / ',' sniffed without one).
func decodeText(data []byte) ([]byte, error) {
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	var fallback = unicode.UTF8.NewDecoder()
	if len(data) >= 2 && data[0] != 0xFF && data[0] != 0xFE && data[0] != 0xEF {
		switch {
		case data[1] == 0 && data[0] != 0:
			fallback = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
		case data[0] == 0 && data[1] != 0:
			fallback = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
		}
	}
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), unicode.BOMOverride(fallback)))
	if err != nil {
		return nil, fmt.Errorf("could not read the file's text encoding: %w", err)
	}
	return out, nil
}

// Table is a CSV file split into a header row and data rows.
type Table struct {
	HeaderRow int        // 1-based record number of the header row
	Headers   []string   // trimmed header cells
	Rows      [][]string // data rows, blank rows dropped
	RowNums   []int      // 1-based record number in the file for each of Rows
	Delimiter rune
}

// Column describes one file column for the mapping step.
type Column struct {
	Index   int      `json:"index"`
	Header  string   `json:"header"`
	Samples []string `json:"samples"`
	Field   string   `json:"field,omitempty"` // field key it is mapped to
}

func sniffDelimiter(text string) rune {
	first := text
	for _, l := range strings.Split(text, "\n") {
		if strings.Count(l, ",")+strings.Count(l, ";")+strings.Count(l, "\t")+strings.Count(l, "|") >= 2 {
			first = l
			break
		}
	}
	best, n := ',', strings.Count(first, ",")
	for _, d := range []rune{';', '\t', '|'} {
		if c := strings.Count(first, string(d)); c > n {
			best, n = d, c
		}
	}
	return best
}

// ReadCSV parses a CSV (UTF-8 or UTF-16, BOM tolerated, delimiter sniffed). The header row is the first of the first 40
// rows that matches at least two known headers for kind, else the best single match, else the first row.
func ReadCSV(data []byte, kind Kind) (*Table, error) {
	b, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = sniffDelimiter(text)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("this does not look like a readable CSV: %v", err)
	}
	hdr, best := 0, 0
	for i := 0; i < len(recs) && i < 40; i++ {
		s := len(AutoMap(kind, recs[i]))
		if s >= 2 {
			hdr, best = i, s
			break
		}
		if s > best {
			hdr, best = i, s
		}
	}
	if len(recs) == 0 {
		return nil, errors.New("the file is empty")
	}
	t := &Table{HeaderRow: hdr + 1, Delimiter: r.Comma}
	for _, h := range recs[hdr] {
		t.Headers = append(t.Headers, strings.TrimSpace(h))
	}
	for i := hdr + 1; i < len(recs); i++ {
		row := make([]string, len(recs[i]))
		blank := true
		for j, c := range recs[i] {
			row[j] = strings.TrimSpace(c)
			blank = blank && row[j] == ""
		}
		if blank {
			continue
		}
		if len(t.Rows) >= MaxRows {
			return nil, ErrTooManyRows
		}
		t.Rows = append(t.Rows, row)
		t.RowNums = append(t.RowNums, i+1)
	}
	return t, nil
}

// Columns lists each header with up to three distinct sample values and the field the mapping assigns to it.
func (t *Table) Columns(m Mapping) []Column {
	cols := make([]Column, len(t.Headers))
	for i, h := range t.Headers {
		cols[i] = Column{Index: i, Header: h, Samples: []string{}}
	}
	for f, i := range m {
		if i >= 0 && i < len(cols) {
			cols[i].Field = f
		}
	}
	for _, row := range t.Rows {
		for i := range cols {
			if i < len(row) && row[i] != "" && len(cols[i].Samples) < 3 && !contains(cols[i].Samples, row[i]) {
				cols[i].Samples = append(cols[i].Samples, row[i])
			}
		}
	}
	return cols
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
