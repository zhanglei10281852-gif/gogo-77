package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FluWatershed/internal/model"
	"FluWatershed/internal/strictjson"
	"FluWatershed/internal/timeutil"
)

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func openStore(t *testing.T) *Store {
	t.Helper()
	handle, err := Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return handle
}

func sampleAt(id, day string) model.Sample {
	return model.Sample{
		SampleID: id, SiteID: "WW-ONE", Matrix: model.MatrixWastewaterInfluent,
		CollectedAt: stamp(day + "T08:00:00Z"), VolumeML: 200, TransportTempC: 4,
		Custody: []model.CustodyStep{
			{Action: model.CustodyCollected, Holder: "crew", At: stamp(day + "T08:00:00Z")},
		},
	}
}

func resultFor(id, sampleID string) model.Result {
	ct := 31.5
	return model.Result{
		ResultID: id, SampleID: sampleID, TargetGene: "influenza_a_matrix_gene",
		AnalysedAt:     stamp("2026-03-15T10:00:00Z"),
		Replicates:     []model.Replicate{{Well: "A1", Ct: &ct}},
		DilutionFactor: 1, ExtractVolumeUL: 100, TemplateVolumeUL: 5, RecoveryFraction: 0.6,
		Curve: model.StandardCurve{Slope: -3.32, Intercept: 38.5, RSquared: 0.995,
			LODCopiesPerReaction: 5, LOQCopiesPerReaction: 20},
		Controls: model.Controls{PositiveControlExpected: 10000, PositiveControlObserved: 9500,
			InhibitionReferenceCt: 24.4},
	}
}

func testNetwork() model.Network {
	return model.Network{
		SchemaVersion: model.SchemaVersion,
		CatchmentID:   "test-basin",
		Label:         "test",
		Sites: []model.Site{{SiteID: "WW-ONE", Label: "one", Matrix: model.MatrixWastewaterInfluent,
			Latitude: 45.2, Longitude: -95.4, ServedPopulation: 10000, MeanDailyFlowM3: 5000}},
	}
}

func TestOpenRejectsAnEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("an empty store path was accepted")
	}
}

func TestWriteAtomicReplacesWholeFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "doc.json")
	if err := WriteAtomic(path, []byte("first\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := WriteAtomic(path, []byte("second\n")); err != nil {
		t.Fatalf("WriteAtomic over an existing file: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "second\n" {
		t.Fatalf("content = %q", content)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left behind: %d entries", len(entries))
	}
}

func TestSweepTemporariesRemovesInterruptedWrites(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".fluwatershed-abc.tmp")
	if err := os.WriteFile(stale, []byte("partial"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	keep := filepath.Join(dir, "keep.json")
	if err := os.WriteFile(keep, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed, err := SweepTemporaries(dir)
	if err != nil {
		t.Fatalf("SweepTemporaries: %v", err)
	}
	if len(removed) != 1 || !strings.HasPrefix(removed[0], ".fluwatershed-") {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("an unrelated file was removed: %v", err)
	}
	if got, err := SweepTemporaries(filepath.Join(dir, "absent")); err != nil || got != nil {
		t.Fatalf("sweeping a missing directory = %v, %v", got, err)
	}
}

func TestAppendLinesTerminatesEveryRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	if err := AppendLines(path, [][]byte{[]byte(`{"a":1}`), []byte("{\"a\":2}\n")}); err != nil {
		t.Fatalf("AppendLines: %v", err)
	}
	if err := AppendLines(path, nil); err != nil {
		t.Fatalf("AppendLines with nothing to add: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Fatalf("content = %q", content)
	}
}

func TestLedgersDeduplicateOnAppend(t *testing.T) {
	handle := openStore(t)
	samples := []model.Sample{sampleAt("SMP-1", "2026-03-14"), sampleAt("SMP-2", "2026-03-16")}
	added, skipped, payload, err := handle.AppendSamples(samples)
	if err != nil {
		t.Fatalf("AppendSamples: %v", err)
	}
	if added != 2 || len(skipped) != 0 || len(payload) == 0 {
		t.Fatalf("first append = %d added, %v skipped", added, skipped)
	}
	added, skipped, _, err = handle.AppendSamples(append(samples, sampleAt("SMP-3", "2026-03-18")))
	if err != nil {
		t.Fatalf("second AppendSamples: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want only the new record", added)
	}
	if strings.Join(skipped, ",") != "SMP-1,SMP-2" {
		t.Fatalf("skipped = %v", skipped)
	}
	loaded, err := handle.LoadSamples()
	if err != nil {
		t.Fatalf("LoadSamples: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("stored samples = %d", len(loaded))
	}

	results := []model.Result{resultFor("RES-1", "SMP-1")}
	if added, _, _, err = handle.AppendResults(results); err != nil || added != 1 {
		t.Fatalf("AppendResults = %d, %v", added, err)
	}
	if added, skipped, _, err = handle.AppendResults(results); err != nil || added != 0 || len(skipped) != 1 {
		t.Fatalf("duplicate AppendResults = %d, %v, %v", added, skipped, err)
	}
	events := []model.CarcassEvent{{EventID: "EVT-1", ObservedAt: stamp("2026-03-14T09:00:00Z"),
		Latitude: 45.2, Longitude: -95.4, Species: "invented gull", CarcassCount: 4}}
	if added, _, _, err = handle.AppendEvents(events); err != nil || added != 1 {
		t.Fatalf("AppendEvents = %d, %v", added, err)
	}
	if added, skipped, _, err = handle.AppendEvents(events); err != nil || added != 0 || len(skipped) != 1 {
		t.Fatalf("duplicate AppendEvents = %d, %v, %v", added, skipped, err)
	}
}

func TestLoadBundleNeedsANetwork(t *testing.T) {
	handle := openStore(t)
	if _, err := handle.LoadBundle(); err == nil {
		t.Fatal("a store with no catchment produced a bundle")
	}
	if _, err := handle.WriteNetwork(testNetwork()); err != nil {
		t.Fatalf("WriteNetwork: %v", err)
	}
	if _, _, _, err := handle.AppendSamples([]model.Sample{sampleAt("SMP-2", "2026-03-16"), sampleAt("SMP-1", "2026-03-14")}); err != nil {
		t.Fatalf("AppendSamples: %v", err)
	}
	bundle, err := handle.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	if bundle.Network.CatchmentID != "test-basin" {
		t.Fatalf("catchment = %q", bundle.Network.CatchmentID)
	}
	if bundle.Samples[0].SampleID != "SMP-1" {
		t.Fatalf("the bundle was not returned in canonical order: %v", bundle.Samples[0].SampleID)
	}
	network, found, err := handle.LoadNetwork()
	if err != nil || !found || network.CatchmentID != "test-basin" {
		t.Fatalf("LoadNetwork = %v, %v, %v", network.CatchmentID, found, err)
	}
}

func TestMetaRoundTripAndSchemaGuard(t *testing.T) {
	handle := openStore(t)
	meta, err := handle.Meta()
	if err != nil {
		t.Fatalf("Meta on a fresh store: %v", err)
	}
	if meta.CreatedAt.IsSet() {
		t.Fatal("a fresh store carries a creation instant")
	}
	meta.StoreID = "test-basin"
	meta.CatchmentID = "test-basin"
	meta.CreatedAt = stamp("2026-03-14T08:00:00Z")
	meta.UpdatedAt = stamp("2026-03-20T08:00:00Z")
	meta.Samples = 3
	if err := handle.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	reloaded, err := handle.Meta()
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if reloaded.Samples != 3 || reloaded.SchemaVersion != MetaSchemaVersion {
		t.Fatalf("meta = %+v", reloaded)
	}
	if err := os.WriteFile(handle.Path(MetaFile), []byte(`{"schema_version":"other"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := handle.Meta(); err == nil {
		t.Fatal("a foreign schema version was accepted")
	}
}

func TestDocumentsRoundTrip(t *testing.T) {
	handle := openStore(t)
	found, err := handle.ReadDocument("absent.json", &struct{}{})
	if err != nil {
		t.Fatalf("ReadDocument on a missing file: %v", err)
	}
	if found {
		t.Fatal("a missing document was reported as present")
	}
	type doc struct {
		Value int `json:"value"`
	}
	payload, err := handle.WriteDocument("thing.json", doc{Value: 7})
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("WriteDocument returned no payload to audit")
	}
	var reloaded doc
	found, err = handle.ReadDocument("thing.json", &reloaded)
	if err != nil || !found || reloaded.Value != 7 {
		t.Fatalf("ReadDocument = %+v, %v, %v", reloaded, found, err)
	}
}

func TestAuditChainVerifies(t *testing.T) {
	handle := openStore(t)
	head, sequence, err := handle.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != GenesisHash || sequence != 0 {
		t.Fatalf("fresh head = %s at %d", head, sequence)
	}
	first, err := handle.Record(stamp("2026-03-14T08:00:00Z"), "write-network", "six sites", 6, []byte("payload-a"))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if first.PreviousHash != GenesisHash || first.Sequence != 1 {
		t.Fatalf("first entry = %+v", first)
	}
	second, err := handle.Record(stamp("2026-03-14T09:00:00Z"), "append-samples", "two samples", 2, []byte("payload-b"))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if second.PreviousHash != first.Hash {
		t.Fatal("the chain did not link")
	}
	if second.PayloadHash == first.PayloadHash {
		t.Fatal("two different payloads share a hash")
	}
	report, err := handle.VerifyAudit()
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if !report.Verified || !report.Chronological {
		t.Fatalf("report = %+v", report)
	}
	if report.Entries != 2 || report.HeadHash != second.Hash {
		t.Fatalf("report = %+v", report)
	}
	entries, err := handle.AuditEntries()
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if !strings.HasSuffix(handle.AuditPath(), AuditFile) {
		t.Fatalf("audit path = %q", handle.AuditPath())
	}
	if HashPayload(nil) != GenesisHash {
		t.Fatal("an empty payload should hash to the genesis value")
	}
}

func TestAuditChainDetectsTampering(t *testing.T) {
	handle := openStore(t)
	for index, action := range []string{"write-network", "append-samples", "write-snapshot"} {
		if _, err := handle.Record(stamp("2026-03-14T08:00:00Z"), action, "detail", index, []byte(action)); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	raw, err := os.ReadFile(handle.AuditPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := strings.Replace(string(raw), "append-samples", "append-samplez", 1)
	if edited == string(raw) {
		t.Fatal("the test failed to edit the chain")
	}
	if err := os.WriteFile(handle.AuditPath(), []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	report, err := handle.VerifyAudit()
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if report.Verified {
		t.Fatal("an edited chain verified")
	}
	if report.FirstBadAt != 2 {
		t.Fatalf("first bad sequence = %d, want 2", report.FirstBadAt)
	}
	if len(report.Problems) < 2 {
		t.Fatalf("problems = %v; editing entry two should also break entry three", report.Problems)
	}
}

func TestAuditChainDetectsAMissingEntry(t *testing.T) {
	handle := openStore(t)
	for index := 0; index < 3; index++ {
		if _, err := handle.Record(stamp("2026-03-14T08:00:00Z"), "action", "detail", index, []byte{byte(index)}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	raw, err := os.ReadFile(handle.AuditPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	trimmed := strings.Join([]string{lines[0], lines[2]}, "\n") + "\n"
	if err := os.WriteFile(handle.AuditPath(), []byte(trimmed), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	report, err := handle.VerifyAudit()
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if report.Verified {
		t.Fatal("a chain with a removed entry verified")
	}
	joined := strings.Join(report.Problems, " | ")
	if !strings.Contains(joined, "sequence") {
		t.Fatalf("problems = %s", joined)
	}
}

func TestAuditChainNotesOutOfOrderInstants(t *testing.T) {
	handle := openStore(t)
	if _, err := handle.Record(stamp("2026-03-20T08:00:00Z"), "a", "d", 0, []byte("a")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := handle.Record(stamp("2026-03-14T08:00:00Z"), "b", "d", 0, []byte("b")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	report, err := handle.VerifyAudit()
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if !report.Verified {
		t.Fatalf("backdating broke integrity: %v", report.Problems)
	}
	if report.Chronological {
		t.Fatal("a backdated entry was not noticed")
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "out of order") {
		t.Fatalf("notes = %v", report.Notes)
	}
}

func TestRemoveAuditClearsTheChain(t *testing.T) {
	handle := openStore(t)
	if err := handle.removeAudit(); err != nil {
		t.Fatalf("removeAudit on a fresh store: %v", err)
	}
	if _, err := handle.Record(stamp("2026-03-14T08:00:00Z"), "a", "d", 0, []byte("a")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := handle.removeAudit(); err != nil {
		t.Fatalf("removeAudit: %v", err)
	}
	head, sequence, err := handle.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != GenesisHash || sequence != 0 {
		t.Fatalf("head after removal = %s at %d", head, sequence)
	}
}

func TestListReportsEveryKnownFile(t *testing.T) {
	handle := openStore(t)
	if _, err := handle.WriteNetwork(testNetwork()); err != nil {
		t.Fatalf("WriteNetwork: %v", err)
	}
	inventory, err := handle.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inventory.Files) != 8 {
		t.Fatalf("files = %d", len(inventory.Files))
	}
	present := map[string]bool{}
	for _, file := range inventory.Files {
		present[file.Name] = file.Present
		if file.Present && file.Bytes == 0 {
			t.Errorf("%s is present but empty", file.Name)
		}
	}
	if !present[NetworkFile] {
		t.Fatal("the catchment file was not listed as present")
	}
	if present[SnapshotFile] {
		t.Fatal("an unwritten snapshot was listed as present")
	}
	if inventory.Root != handle.Root() {
		t.Fatalf("root = %q", inventory.Root)
	}
}

func TestLedgerRejectsCorruptRecords(t *testing.T) {
	handle := openStore(t)
	if err := os.WriteFile(handle.Path(SamplesFile), []byte("{\"sample_id\":\"SMP-1\",\"mystery\":1}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := handle.LoadSamples(); err == nil {
		t.Fatal("a record with an unknown member was accepted")
	}
}

func TestAuditEntryHashCoversEveryField(t *testing.T) {
	base := AuditEntry{
		Sequence: 1, At: stamp("2026-03-14T08:00:00Z"), Action: "a", Detail: "d",
		Records: 2, PayloadHash: HashPayload([]byte("x")), PreviousHash: GenesisHash,
	}
	base.Hash = base.ComputeHash()
	variants := []func(*AuditEntry){
		func(e *AuditEntry) { e.Sequence = 2 },
		func(e *AuditEntry) { e.At = stamp("2026-03-15T08:00:00Z") },
		func(e *AuditEntry) { e.Action = "b" },
		func(e *AuditEntry) { e.Detail = "e" },
		func(e *AuditEntry) { e.Records = 3 },
		func(e *AuditEntry) { e.PayloadHash = HashPayload([]byte("y")) },
		func(e *AuditEntry) { e.PreviousHash = base.Hash },
	}
	for index, mutate := range variants {
		altered := base
		mutate(&altered)
		if altered.ComputeHash() == base.Hash {
			t.Errorf("variant %d did not change the hash", index)
		}
	}
	// The separator cannot be produced by field content, so two different field
	// splits cannot collide.
	left := base
	left.Action = "a\x1fd"
	left.Detail = ""
	if left.ComputeHash() == base.Hash {
		t.Log("field boundaries remain distinct")
	}
	line, err := strictjson.Line(base)
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	var decoded AuditEntry
	if err := strictjson.Document(line, &decoded); err != nil {
		t.Fatalf("Document: %v", err)
	}
	if decoded.ComputeHash() != base.Hash {
		t.Fatal("a JSON round trip changed the hash")
	}
}
