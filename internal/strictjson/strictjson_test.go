package strictjson

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type payload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDocumentRejectsUnknownMember(t *testing.T) {
	var target payload
	err := Document([]byte(`{"name":"a","count":1,"extra":true}`), &target)
	if err == nil {
		t.Fatal("an unknown member was accepted")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("error should name the offending member: %v", err)
	}
}

func TestDocumentRejectsTrailingValue(t *testing.T) {
	var target payload
	err := Document([]byte(`{"name":"a","count":1}{"name":"b","count":2}`), &target)
	if err == nil {
		t.Fatal("a second document was accepted")
	}
	if !strings.Contains(err.Error(), "second JSON value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDocumentRejectsEmptyAndBadUTF8(t *testing.T) {
	var target payload
	if err := Document([]byte("   \n"), &target); err == nil {
		t.Fatal("an empty payload was accepted")
	}
	if err := Document([]byte{'{', 0xff, '}'}, &target); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}

func TestDocumentAcceptsByteOrderMark(t *testing.T) {
	var target payload
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"name":"a","count":3}`)...)
	if err := Document(raw, &target); err != nil {
		t.Fatalf("a byte order mark broke decoding: %v", err)
	}
	if target.Count != 3 {
		t.Fatalf("count = %d", target.Count)
	}
}

func TestDocumentReportsTypeMismatchWithField(t *testing.T) {
	var target payload
	err := Document([]byte(`{"name":"a","count":"seven"}`), &target)
	if err == nil {
		t.Fatal("a string in an int member was accepted")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Fatalf("error should name the member: %v", err)
	}
}

func TestDocumentReportsTruncation(t *testing.T) {
	var target payload
	err := Document([]byte(`{"name":"a","count":`), &target)
	if err == nil {
		t.Fatal("a truncated document was accepted")
	}
}

func TestRecordsWalksNonBlankLines(t *testing.T) {
	stream := "\n{\"name\":\"a\",\"count\":1}\n\n{\"name\":\"b\",\"count\":2}\n"
	var seen []payload
	var lines []int
	err := Records(strings.NewReader(stream), func(line int, raw []byte) error {
		var item payload
		if err := Document(raw, &item); err != nil {
			return err
		}
		seen = append(seen, item)
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(seen) != 2 || seen[0].Name != "a" || seen[1].Name != "b" {
		t.Fatalf("records = %+v", seen)
	}
	if lines[0] != 2 || lines[1] != 4 {
		t.Fatalf("physical line numbers = %v, want 2 and 4", lines)
	}
}

func TestRecordsRejectsEmptyStream(t *testing.T) {
	err := Records(strings.NewReader("\n\n  \n"), func(int, []byte) error { return nil })
	if err == nil {
		t.Fatal("a stream with no records was accepted")
	}
}

func TestRecordsWrapsHandlerErrorWithLine(t *testing.T) {
	err := Records(strings.NewReader("{}\n{\"bad\":1}\n"), func(line int, raw []byte) error {
		var item payload
		return Document(raw, &item)
	})
	if err == nil {
		t.Fatal("a bad record was accepted")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error should name the line: %v", err)
	}
}

func TestRecordsRejectsOversizedLine(t *testing.T) {
	huge := "{\"name\":\"" + strings.Repeat("x", RecordLimit+16) + "\"}\n"
	err := Records(strings.NewReader(huge), func(int, []byte) error { return nil })
	if err == nil {
		t.Fatal("an oversized record was accepted")
	}
	if !strings.Contains(err.Error(), "record limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFileAndRecordsFile(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.json")
	if err := os.WriteFile(docPath, []byte(`{"name":"a","count":9}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var target payload
	if err := File(docPath, &target); err != nil {
		t.Fatalf("File: %v", err)
	}
	if target.Count != 9 {
		t.Fatalf("count = %d", target.Count)
	}
	if err := File(filepath.Join(dir, "missing.json"), &target); err == nil {
		t.Fatal("a missing file was accepted")
	}
	linePath := filepath.Join(dir, "ledger.jsonl")
	if err := os.WriteFile(linePath, []byte("{\"name\":\"a\",\"count\":1}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	count := 0
	if err := RecordsFile(linePath, func(int, []byte) error { count++; return nil }); err != nil {
		t.Fatalf("RecordsFile: %v", err)
	}
	if count != 1 {
		t.Fatalf("records = %d", count)
	}
}

func TestEncodersAreDeterministicAndNewlineTerminated(t *testing.T) {
	value := payload{Name: "a<b>", Count: 2}
	indented, err := Indented(value)
	if err != nil {
		t.Fatalf("Indented: %v", err)
	}
	if !bytes.HasSuffix(indented, []byte("\n")) {
		t.Fatal("indented output is not newline terminated")
	}
	if bytes.Contains(indented, []byte("\\u003c")) {
		t.Fatalf("HTML escaping leaked into output: %s", indented)
	}
	if !bytes.Contains(indented, []byte("\n  \"name\"")) {
		t.Fatalf("indentation missing: %s", indented)
	}
	line, err := Line(value)
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("compact output should hold exactly one newline: %q", line)
	}
	again, err := Line(value)
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if !bytes.Equal(line, again) {
		t.Fatal("two encodings of the same value differ")
	}
}

func TestReaderDecodes(t *testing.T) {
	var target payload
	if err := Reader(strings.NewReader(`{"name":"z","count":4}`), &target); err != nil {
		t.Fatalf("Reader: %v", err)
	}
	if target.Name != "z" {
		t.Fatalf("name = %q", target.Name)
	}
}
