// Package store is the local, offline persistence layer.
//
// A store is a plain directory. Observations live in append-only JSONL ledgers,
// one record per line, because an observation is a historical fact and rewriting
// it would destroy the record. Computed material lives in whole-document JSON
// files that are replaced atomically, because a snapshot is only ever the latest
// answer and a half-written one is useless.
//
// Every mutation is recorded in a SHA-256 hash-chained audit log. The chain does
// not prevent anyone from editing a file, it makes the edit detectable: verify
// recomputes every link and names the first one that no longer fits.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"FluWatershed/internal/model"
	"FluWatershed/internal/strictjson"
	"FluWatershed/internal/timeutil"
)

// The file names inside a store directory.
const (
	MetaFile     = "meta.json"
	NetworkFile  = "network.json"
	SamplesFile  = "samples.jsonl"
	ResultsFile  = "results.jsonl"
	EventsFile   = "events.jsonl"
	SnapshotFile = "signals.json"
	AlertsFile   = "alerts.json"
	AuditFile    = "audit.log"
)

// MetaSchemaVersion tags the store metadata document.
const MetaSchemaVersion = "fluwatershed-store/v1"

// Meta is the store's own bookkeeping.
type Meta struct {
	SchemaVersion     string         `json:"schema_version"`
	StoreID           string         `json:"store_id"`
	CreatedAt         timeutil.Stamp `json:"created_at"`
	UpdatedAt         timeutil.Stamp `json:"updated_at"`
	ConfigFingerprint string         `json:"config_fingerprint"`
	CatchmentID       string         `json:"catchment_id"`
	Sites             int            `json:"sites"`
	Samples           int            `json:"samples"`
	Results           int            `json:"results"`
	Events            int            `json:"events"`
	Ingests           int            `json:"ingests"`
}

// Store is a handle on one store directory.
type Store struct {
	root string
}

// Open prepares the directory at root, creating it when absent, and clears any
// temporary file left behind by an interrupted write.
func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("store path is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve store path %s: %w", root, err)
	}
	if err := os.MkdirAll(absolute, dirPerm); err != nil {
		return nil, fmt.Errorf("create store directory %s: %w", absolute, err)
	}
	store := &Store{root: absolute}
	if _, err := SweepTemporaries(absolute); err != nil {
		return nil, err
	}
	return store, nil
}

// Root is the absolute path of the store directory.
func (s *Store) Root() string { return s.root }

// Path joins a file name onto the store root.
func (s *Store) Path(name string) string { return filepath.Join(s.root, name) }

// Meta loads the store metadata, returning a zero value for a fresh store.
func (s *Store) Meta() (Meta, error) {
	path := s.Path(MetaFile)
	if !exists(path) {
		return Meta{SchemaVersion: MetaSchemaVersion}, nil
	}
	var meta Meta
	if err := strictjson.File(path, &meta); err != nil {
		return Meta{}, err
	}
	if meta.SchemaVersion != MetaSchemaVersion {
		return Meta{}, fmt.Errorf("%s: schema_version must be %q, got %q", path, MetaSchemaVersion, meta.SchemaVersion)
	}
	return meta, nil
}

// WriteMeta replaces the store metadata atomically.
func (s *Store) WriteMeta(meta Meta) error {
	meta.SchemaVersion = MetaSchemaVersion
	payload, err := strictjson.Indented(meta)
	if err != nil {
		return err
	}
	return WriteAtomic(s.Path(MetaFile), payload)
}

// WriteDocument replaces one whole-document JSON file atomically and returns the
// bytes written so that the caller can record them in the audit chain.
func (s *Store) WriteDocument(name string, value any) ([]byte, error) {
	payload, err := strictjson.Indented(value)
	if err != nil {
		return nil, err
	}
	if err := WriteAtomic(s.Path(name), payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// ReadDocument strictly decodes one whole-document JSON file. It reports whether
// the file was present, so a missing snapshot is a normal state rather than an
// error.
func (s *Store) ReadDocument(name string, target any) (bool, error) {
	path := s.Path(name)
	if !exists(path) {
		return false, nil
	}
	if err := strictjson.File(path, target); err != nil {
		return false, err
	}
	return true, nil
}

// WriteNetwork stores the catchment definition.
func (s *Store) WriteNetwork(network model.Network) ([]byte, error) {
	return s.WriteDocument(NetworkFile, network)
}

// LoadNetwork reads the stored catchment definition.
func (s *Store) LoadNetwork() (model.Network, bool, error) {
	var network model.Network
	found, err := s.ReadDocument(NetworkFile, &network)
	if err != nil {
		return model.Network{}, false, err
	}
	return network, found, nil
}

// AppendSamples adds samples the ledger does not already hold. Existing
// identifiers are skipped rather than duplicated, and the count of both is
// returned so the caller can report what an ingest actually did.
func (s *Store) AppendSamples(samples []model.Sample) (added int, skipped []string, payload []byte, err error) {
	existing, err := s.LoadSamples()
	if err != nil {
		return 0, nil, nil, err
	}
	present := make(map[string]bool, len(existing))
	for _, sample := range existing {
		present[sample.SampleID] = true
	}
	var lines [][]byte
	for _, sample := range samples {
		if present[sample.SampleID] {
			skipped = append(skipped, sample.SampleID)
			continue
		}
		present[sample.SampleID] = true
		line, encodeErr := strictjson.Line(sample)
		if encodeErr != nil {
			return 0, nil, nil, encodeErr
		}
		lines = append(lines, line)
		payload = append(payload, line...)
	}
	if err := AppendLines(s.Path(SamplesFile), lines); err != nil {
		return 0, nil, nil, err
	}
	sort.Strings(skipped)
	return len(lines), skipped, payload, nil
}

// LoadSamples reads the whole sample ledger in file order.
func (s *Store) LoadSamples() ([]model.Sample, error) {
	var out []model.Sample
	err := s.readLedger(SamplesFile, func(raw []byte) error {
		var sample model.Sample
		if err := strictjson.Document(raw, &sample); err != nil {
			return err
		}
		out = append(out, sample)
		return nil
	})
	return out, err
}

// AppendResults adds assay results the ledger does not already hold.
func (s *Store) AppendResults(results []model.Result) (added int, skipped []string, payload []byte, err error) {
	existing, err := s.LoadResults()
	if err != nil {
		return 0, nil, nil, err
	}
	present := make(map[string]bool, len(existing))
	for _, result := range existing {
		present[result.ResultID] = true
	}
	var lines [][]byte
	for _, result := range results {
		if present[result.ResultID] {
			skipped = append(skipped, result.ResultID)
			continue
		}
		present[result.ResultID] = true
		line, encodeErr := strictjson.Line(result)
		if encodeErr != nil {
			return 0, nil, nil, encodeErr
		}
		lines = append(lines, line)
		payload = append(payload, line...)
	}
	if err := AppendLines(s.Path(ResultsFile), lines); err != nil {
		return 0, nil, nil, err
	}
	sort.Strings(skipped)
	return len(lines), skipped, payload, nil
}

// LoadResults reads the whole result ledger in file order.
func (s *Store) LoadResults() ([]model.Result, error) {
	var out []model.Result
	err := s.readLedger(ResultsFile, func(raw []byte) error {
		var result model.Result
		if err := strictjson.Document(raw, &result); err != nil {
			return err
		}
		out = append(out, result)
		return nil
	})
	return out, err
}

// AppendEvents adds carcass events the ledger does not already hold.
func (s *Store) AppendEvents(events []model.CarcassEvent) (added int, skipped []string, payload []byte, err error) {
	existing, err := s.LoadEvents()
	if err != nil {
		return 0, nil, nil, err
	}
	present := make(map[string]bool, len(existing))
	for _, event := range existing {
		present[event.EventID] = true
	}
	var lines [][]byte
	for _, event := range events {
		if present[event.EventID] {
			skipped = append(skipped, event.EventID)
			continue
		}
		present[event.EventID] = true
		line, encodeErr := strictjson.Line(event)
		if encodeErr != nil {
			return 0, nil, nil, encodeErr
		}
		lines = append(lines, line)
		payload = append(payload, line...)
	}
	if err := AppendLines(s.Path(EventsFile), lines); err != nil {
		return 0, nil, nil, err
	}
	sort.Strings(skipped)
	return len(lines), skipped, payload, nil
}

// LoadEvents reads the whole carcass event ledger in file order.
func (s *Store) LoadEvents() ([]model.CarcassEvent, error) {
	var out []model.CarcassEvent
	err := s.readLedger(EventsFile, func(raw []byte) error {
		var event model.CarcassEvent
		if err := strictjson.Document(raw, &event); err != nil {
			return err
		}
		out = append(out, event)
		return nil
	})
	return out, err
}

func (s *Store) readLedger(name string, decode func(raw []byte) error) error {
	path := s.Path(name)
	if !exists(path) {
		return nil
	}
	return strictjson.RecordsFile(path, func(line int, raw []byte) error { return decode(raw) })
}

// LoadBundle assembles the stored network and ledgers into one input set in
// canonical order.
func (s *Store) LoadBundle() (model.Bundle, error) {
	network, found, err := s.LoadNetwork()
	if err != nil {
		return model.Bundle{}, err
	}
	if !found {
		return model.Bundle{}, fmt.Errorf("store %s holds no catchment definition; run ingest first", s.root)
	}
	samples, err := s.LoadSamples()
	if err != nil {
		return model.Bundle{}, err
	}
	results, err := s.LoadResults()
	if err != nil {
		return model.Bundle{}, err
	}
	events, err := s.LoadEvents()
	if err != nil {
		return model.Bundle{}, err
	}
	bundle := model.Bundle{Network: network, Samples: samples, Results: results, Events: events}
	bundle.SortAll()
	return bundle, nil
}

// Inventory is a listing of the store's files and their sizes.
type Inventory struct {
	Root  string          `json:"root"`
	Files []InventoryFile `json:"files"`
}

// InventoryFile is one file in the store.
type InventoryFile struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Present bool   `json:"present"`
}

// List reports which of the store's files exist and how large they are, in a
// fixed name order.
func (s *Store) List() (Inventory, error) {
	names := []string{MetaFile, NetworkFile, SamplesFile, ResultsFile, EventsFile, SnapshotFile, AlertsFile, AuditFile}
	inventory := Inventory{Root: s.root}
	for _, name := range names {
		file := InventoryFile{Name: name}
		info, err := os.Stat(s.Path(name))
		switch {
		case err == nil:
			file.Present = true
			file.Bytes = info.Size()
		case errors.Is(err, fs.ErrNotExist):
		default:
			return Inventory{}, fmt.Errorf("stat %s: %w", name, err)
		}
		inventory.Files = append(inventory.Files, file)
	}
	return inventory, nil
}
