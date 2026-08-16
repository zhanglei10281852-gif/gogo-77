package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"FluWatershed/internal/strictjson"
	"FluWatershed/internal/timeutil"
)

// GenesisHash is the previous-hash value of the first audit entry. It is a
// literal rather than an empty string so that a truncated first line cannot be
// mistaken for a valid start of chain.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditEntry is one link in the store's hash chain.
//
// PayloadHash covers the bytes that the recorded action wrote. Hash covers the
// entry's own fields together with the previous entry's Hash, which is what
// binds the chain: editing any earlier entry, or any recorded payload, changes
// every hash after it.
type AuditEntry struct {
	Sequence     int            `json:"sequence"`
	At           timeutil.Stamp `json:"at"`
	Action       string         `json:"action"`
	Detail       string         `json:"detail"`
	Records      int            `json:"records"`
	PayloadHash  string         `json:"payload_sha256"`
	PreviousHash string         `json:"previous_hash"`
	Hash         string         `json:"hash"`
}

// HashPayload is the hex SHA-256 of a payload, or the genesis value when there
// is no payload to cover.
func HashPayload(payload []byte) string {
	if len(payload) == 0 {
		return GenesisHash
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// canonical renders the fields an entry's hash covers. The layout is fixed and
// field-separated by a character that cannot appear in any of the fields, so two
// different entries can never produce the same pre-image.
func (e AuditEntry) canonical() string {
	return strings.Join([]string{
		fmt.Sprintf("%d", e.Sequence),
		e.At.String(),
		e.Action,
		e.Detail,
		fmt.Sprintf("%d", e.Records),
		e.PayloadHash,
		e.PreviousHash,
	}, "\x1f")
}

// ComputeHash returns the hash the entry should carry.
func (e AuditEntry) ComputeHash() string {
	sum := sha256.Sum256([]byte(e.canonical()))
	return hex.EncodeToString(sum[:])
}

// AuditReport is the outcome of verifying a chain.
//
// Verified covers integrity alone: sequence numbering, previous-hash linkage and
// the recomputed hash of every entry. Chronological is reported separately
// because a chain can be perfectly intact and still hold entries whose reporting
// instants move backwards, which happens legitimately when an operator replays
// history at earlier instants. That is worth saying out loud but it is not
// tampering, so it does not fail verification.
type AuditReport struct {
	Entries       int      `json:"entries"`
	Verified      bool     `json:"verified"`
	Chronological bool     `json:"chronological"`
	HeadHash      string   `json:"head_hash"`
	FirstBadAt    int      `json:"first_bad_sequence"`
	Problems      []string `json:"problems"`
	Notes         []string `json:"notes"`
}

// readAudit loads the chain from disk in file order.
func (s *Store) readAudit() ([]AuditEntry, error) {
	path := s.Path(AuditFile)
	if !exists(path) {
		return nil, nil
	}
	var entries []AuditEntry
	err := strictjson.RecordsFile(path, func(line int, raw []byte) error {
		var entry AuditEntry
		if err := strictjson.Document(raw, &entry); err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Head returns the hash of the newest audit entry, or the genesis value when the
// chain is empty.
func (s *Store) Head() (string, int, error) {
	entries, err := s.readAudit()
	if err != nil {
		return "", 0, err
	}
	if len(entries) == 0 {
		return GenesisHash, 0, nil
	}
	last := entries[len(entries)-1]
	return last.Hash, last.Sequence, nil
}

// Record appends one audit entry covering payload. The instant comes from the
// caller because nothing in this program reads the system clock.
func (s *Store) Record(at timeutil.Stamp, action, detail string, records int, payload []byte) (AuditEntry, error) {
	previous, sequence, err := s.Head()
	if err != nil {
		return AuditEntry{}, err
	}
	entry := AuditEntry{
		Sequence:     sequence + 1,
		At:           at,
		Action:       action,
		Detail:       detail,
		Records:      records,
		PayloadHash:  HashPayload(payload),
		PreviousHash: previous,
	}
	entry.Hash = entry.ComputeHash()
	line, err := strictjson.Line(entry)
	if err != nil {
		return AuditEntry{}, err
	}
	if err := AppendLines(s.Path(AuditFile), [][]byte{line}); err != nil {
		return AuditEntry{}, err
	}
	return entry, nil
}

// VerifyAudit recomputes the chain and reports the first place it breaks.
//
// Four things are checked for every entry: the sequence increments by one, the
// previous-hash matches the hash of the entry before it, the stored hash matches
// what the entry's own fields produce, and the timestamps never move backwards.
// The first three catch tampering; the last catches a chain assembled from runs
// whose reporting instants were supplied out of order.
func (s *Store) VerifyAudit() (AuditReport, error) {
	entries, err := s.readAudit()
	if err != nil {
		return AuditReport{}, err
	}
	report := AuditReport{
		Entries: len(entries), Verified: true, Chronological: true,
		HeadHash: GenesisHash, FirstBadAt: 0,
	}
	previousHash := GenesisHash
	previousAt := timeutil.Stamp{}
	for index, entry := range entries {
		expectedSequence := index + 1
		if entry.Sequence != expectedSequence {
			report.fail(entry.Sequence, fmt.Sprintf("entry %d carries sequence %d", expectedSequence, entry.Sequence))
		}
		if entry.PreviousHash != previousHash {
			report.fail(entry.Sequence, fmt.Sprintf(
				"entry %d previous_hash %s does not match the preceding hash %s",
				entry.Sequence, shorten(entry.PreviousHash), shorten(previousHash)))
		}
		recomputed := entry.ComputeHash()
		if entry.Hash != recomputed {
			report.fail(entry.Sequence, fmt.Sprintf(
				"entry %d hash %s does not match the recomputed %s",
				entry.Sequence, shorten(entry.Hash), shorten(recomputed)))
		}
		if previousAt.IsSet() && entry.At.Before(previousAt) {
			report.Chronological = false
			report.Notes = append(report.Notes, fmt.Sprintf(
				"entry %d is timestamped %s, before the preceding %s; the chain is intact but was written out of order",
				entry.Sequence, entry.At, previousAt))
		}
		// The next link is compared against the hash this entry's own fields
		// produce, not the hash it claims to carry. Chaining on the recomputed
		// value is what makes a single edited entry invalidate every entry after
		// it rather than only itself.
		previousHash = recomputed
		previousAt = entry.At
		report.HeadHash = entry.Hash
	}
	return report, nil
}

func (r *AuditReport) fail(sequence int, message string) {
	r.Verified = false
	if r.FirstBadAt == 0 {
		r.FirstBadAt = sequence
	}
	r.Problems = append(r.Problems, message)
}

func shorten(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "..."
}

// AuditEntries exposes the chain for reporting.
func (s *Store) AuditEntries() ([]AuditEntry, error) { return s.readAudit() }

// AuditPath is the absolute path of the audit chain, for messages.
func (s *Store) AuditPath() string { return s.Path(AuditFile) }

// removeAudit deletes the chain. It exists only so that tests can build a
// deliberately broken chain from scratch; nothing in the CLI calls it.
func (s *Store) removeAudit() error {
	path := s.Path(AuditFile)
	if !exists(path) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
