// Package strictjson centralises every JSON read and write performed by
// FluWatershed.
//
// Reads are deliberately unforgiving. Unknown object members are an error, a
// second document following the first is an error, an empty payload is an
// error, and invalid UTF-8 is an error. Surveillance inputs are hand-edited
// often enough that silently ignoring a misspelled key is worse than refusing
// to run.
//
// Writes are deterministic: no HTML escaping, two-space indentation for
// documents, one compact line per record for ledgers, and exactly one
// terminating newline in both forms.
package strictjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// RecordLimit is the largest single JSONL record accepted, in bytes. Anything
// larger is rejected instead of being cut short.
const RecordLimit = 1 << 20

var byteOrderMark = []byte{0xEF, 0xBB, 0xBF}

// Document strictly decodes one JSON document from raw into target.
func Document(raw []byte, target any) error {
	raw = bytes.TrimPrefix(raw, byteOrderMark)
	if !utf8.Valid(raw) {
		return errors.New("payload is not valid UTF-8")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("payload contains no JSON document")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return describe(err)
	}
	return requireEnd(dec)
}

// Reader strictly decodes one JSON document read from r into target.
func Reader(r io.Reader, target any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read payload: %w", err)
	}
	return Document(raw, target)
}

// File strictly decodes the JSON document stored at path into target.
func File(path string, target any) error {
	handle, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = handle.Close() }()
	if err := Reader(handle, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func requireEnd(dec *json.Decoder) error {
	var leftover json.RawMessage
	err := dec.Decode(&leftover)
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return fmt.Errorf("content follows the first JSON document: %w", err)
	}
	tail := strings.TrimSpace(string(leftover))
	if len(tail) > 40 {
		tail = tail[:40] + "..."
	}
	return fmt.Errorf("a second JSON value follows the first document: %s", tail)
}

func describe(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		where := typeErr.Field
		if where == "" {
			where = "(document root)"
		}
		return fmt.Errorf("member %s: JSON %s is not assignable to %s at byte %d",
			where, typeErr.Value, typeErr.Type.String(), typeErr.Offset)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("malformed JSON at byte %d: %w", syntaxErr.Offset, err)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return errors.New("JSON document ends before it is complete")
	}
	return err
}

// RecordFunc is invoked once per non-blank JSONL record. line is 1-based and
// counts physical lines so that error messages match what an editor shows.
type RecordFunc func(line int, raw []byte) error

// Records walks a JSONL stream and hands every non-blank line to visit. Blank
// lines are skipped; a stream with no records at all is an error because an
// empty ledger is almost always a wiring mistake.
func Records(r io.Reader, visit RecordFunc) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), RecordLimit)
	line, kept := 0, 0
	for sc.Scan() {
		line++
		payload := bytes.TrimSpace(sc.Bytes())
		if line == 1 {
			payload = bytes.TrimSpace(bytes.TrimPrefix(payload, byteOrderMark))
		}
		if len(payload) == 0 {
			continue
		}
		owned := make([]byte, len(payload))
		copy(owned, payload)
		kept++
		if err := visit(line, owned); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("line %d is larger than the %d byte record limit", line+1, RecordLimit)
		}
		return fmt.Errorf("scan stream: %w", err)
	}
	if kept == 0 {
		return errors.New("JSONL stream holds no records")
	}
	return nil
}

// RecordsFile is Records applied to a file on disk.
func RecordsFile(path string, visit RecordFunc) error {
	handle, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = handle.Close() }()
	if err := Records(handle, visit); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Indented renders value as pretty-printed JSON with a trailing newline.
func Indented(value any) ([]byte, error) {
	return encode(value, "  ")
}

// Line renders value as a single compact JSON line with a trailing newline.
// This is the on-disk shape of every ledger and audit record.
func Line(value any) ([]byte, error) {
	return encode(value, "")
}

func encode(value any, indent string) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	return out.Bytes(), nil
}
