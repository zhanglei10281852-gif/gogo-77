package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const examplesDir = "../../examples"

func networkPath() string { return filepath.Join(examplesDir, "network.json") }
func samplesPath() string { return filepath.Join(examplesDir, "samples.jsonl") }
func resultsPath() string { return filepath.Join(examplesDir, "results.jsonl") }
func eventsPath() string  { return filepath.Join(examplesDir, "events.jsonl") }
func configPath() string  { return filepath.Join(examplesDir, "config.json") }

// run executes one command line and captures both streams.
func run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(Environment{Stdout: &out, Stderr: &errOut}, args)
	return code, out.String(), errOut.String()
}

func sourceFlags() []string {
	return []string{
		"--network", networkPath(),
		"--samples", samplesPath(),
		"--results", resultsPath(),
		"--events", eventsPath(),
	}
}

func TestUsageAndVersion(t *testing.T) {
	code, out, _ := run("help")
	if code != ExitOK {
		t.Fatalf("help exit = %d", code)
	}
	for _, needle := range []string{"validate", "ingest", "quantify", "alert", "verify", "report", "study tool"} {
		if !strings.Contains(out, needle) {
			t.Errorf("usage does not mention %q", needle)
		}
	}
	code, out, _ = run("version")
	if code != ExitOK {
		t.Fatalf("version exit = %d", code)
	}
	if !strings.Contains(out, Version) {
		t.Fatalf("version output = %q", out)
	}
	if !strings.Contains(out, "must not be used for real outbreak decisions") {
		t.Fatalf("version output lacks the caution: %q", out)
	}
}

func TestNoArgumentsAndUnknownCommand(t *testing.T) {
	code, _, errOut := run()
	if code != ExitError {
		t.Fatalf("empty command line exit = %d", code)
	}
	if !strings.Contains(errOut, "Usage") {
		t.Fatalf("stderr = %q", errOut)
	}
	code, _, errOut = run("teleport")
	if code != ExitError {
		t.Fatalf("unknown command exit = %d", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestUnknownFlagIsRejected(t *testing.T) {
	code, _, errOut := run("validate", "--network", networkPath(), "--mystery", "1")
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "mystery") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestPositionalArgumentsAreRejected(t *testing.T) {
	code, _, errOut := run("validate", "--network", networkPath(), "extra")
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestBadFormatIsRejected(t *testing.T) {
	code, _, errOut := run("validate", "--network", networkPath(), "--format", "yaml")
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "unknown format") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestValidateAcceptsTheBundledExample(t *testing.T) {
	code, out, errOut := run(append([]string{"validate"}, sourceFlags()...)...)
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "every check passed") {
		t.Fatalf("stdout = %s", out)
	}
	code, out, _ = run(append([]string{"validate", "--format", "json"}, sourceFlags()...)...)
	if code != ExitOK {
		t.Fatalf("json exit = %d", code)
	}
	var document struct {
		Valid    bool     `json:"valid"`
		Topology []string `json:"topological_order"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatalf("the JSON mode did not produce a document: %v", err)
	}
	if !document.Valid || len(document.Topology) == 0 {
		t.Fatalf("document = %+v", document)
	}
}

func TestValidateReportsAVerdictExitCode(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "network.json")
	raw, err := os.ReadFile(networkPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := strings.Replace(string(raw), `"fluwatershed/v1"`, `"fluwatershed/v0"`, 1)
	if err := os.WriteFile(broken, []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, out, _ := run("validate", "--network", broken)
	if code != ExitVerdict {
		t.Fatalf("exit = %d, want the verdict code", code)
	}
	if !strings.Contains(out, "schema_version") {
		t.Fatalf("stdout = %s", out)
	}
}

func TestSourcesAreRequired(t *testing.T) {
	code, _, errOut := run("quantify")
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "--network with its ledgers or --store") {
		t.Fatalf("stderr = %q", errOut)
	}
	code, _, errOut = run("verify")
	if code != ExitError {
		t.Fatalf("verify without a store exit = %d", code)
	}
	if !strings.Contains(errOut, "--store is required") {
		t.Fatalf("stderr = %q", errOut)
	}
	code, _, errOut = run("ingest", "--store", t.TempDir())
	if code != ExitError {
		t.Fatalf("ingest without a network exit = %d", code)
	}
	if !strings.Contains(errOut, "--network is required") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestProfilePrintsTheActivePolicy(t *testing.T) {
	code, out, errOut := run("profile", "--config", configPath())
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "reference-profile") || !strings.Contains(out, "Fingerprint") {
		t.Fatalf("stdout = %s", out)
	}
	code, out, _ = run("profile", "--default", "--format", "json")
	if code != ExitOK {
		t.Fatalf("json exit = %d", code)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatalf("json: %v", err)
	}
	if document["schema_version"] != "fluwatershed-config/v1" {
		t.Fatalf("document = %+v", document)
	}
}

// ingestExample builds a fresh store from the bundled example.
func ingestExample(t *testing.T) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "store")
	args := append([]string{"ingest", "--store", store, "--config", configPath()}, sourceFlags()...)
	code, out, errOut := run(args...)
	if code != ExitOK {
		t.Fatalf("ingest exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "samples added") {
		t.Fatalf("ingest output = %s", out)
	}
	return store
}

func TestIngestIsIdempotent(t *testing.T) {
	store := ingestExample(t)
	args := append([]string{"ingest", "--store", store, "--config", configPath(), "--format", "json"}, sourceFlags()...)
	code, out, errOut := run(args...)
	if code != ExitOK {
		t.Fatalf("second ingest exit = %d, stderr = %s", code, errOut)
	}
	var summary struct {
		SamplesAdded   int      `json:"samples_added"`
		SamplesSkipped []string `json:"samples_skipped"`
		ResultsAdded   int      `json:"results_added"`
		EventsAdded    int      `json:"events_added"`
		Meta           struct {
			Samples int `json:"samples"`
			Ingests int `json:"ingests"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("json: %v", err)
	}
	if summary.SamplesAdded != 0 || summary.ResultsAdded != 0 || summary.EventsAdded != 0 {
		t.Fatalf("a repeated ingest added records: %+v", summary)
	}
	if len(summary.SamplesSkipped) == 0 {
		t.Fatal("the skipped list is empty on a repeated ingest")
	}
	if summary.Meta.Ingests != 2 {
		t.Fatalf("ingests = %d", summary.Meta.Ingests)
	}
}

func TestIngestRefusesAForeignCatchment(t *testing.T) {
	store := ingestExample(t)
	dir := t.TempDir()
	raw, err := os.ReadFile(networkPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	other := filepath.Join(dir, "network.json")
	edited := strings.Replace(string(raw), `"vellamoor-basin"`, `"other-basin"`, 1)
	if err := os.WriteFile(other, []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, errOut := run("ingest", "--store", store, "--network", other)
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "other-basin") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestIngestRejectsInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	raw, err := os.ReadFile(samplesPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	broken := strings.Replace(first, `"volume_ml":200`, `"volume_ml":0`, 1)
	if broken == first {
		t.Fatalf("the test failed to break a record: %s", first)
	}
	path := filepath.Join(dir, "samples.jsonl")
	if err := os.WriteFile(path, []byte(broken+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, errOut := run("ingest", "--store", filepath.Join(dir, "store"),
		"--network", networkPath(), "--samples", path)
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "did not validate") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestReadOnlyCommandsRunAgainstAStore(t *testing.T) {
	store := ingestExample(t)
	cases := []struct {
		name    string
		args    []string
		allowed []int
		needle  string
	}{
		{"quantify", []string{"quantify"}, []int{ExitOK}, "Quantification"},
		{"qc", []string{"qc"}, []int{ExitOK, ExitVerdict}, "Quality control"},
		{"normalize", []string{"normalize"}, []int{ExitOK}, "Normalisation"},
		{"baseline", []string{"baseline"}, []int{ExitOK}, "Baselines"},
		{"detect", []string{"detect"}, []int{ExitOK, ExitVerdict}, "Trend and exceedance"},
		{"catchment", []string{"catchment"}, []int{ExitOK}, "Catchment topology"},
		{"report", []string{"report"}, []int{ExitOK}, "Standing caution"},
		{"verify", []string{"verify"}, []int{ExitOK}, "Audit chain"},
	}
	for _, item := range cases {
		args := append(append([]string{}, item.args...), "--store", store, "--config", configPath())
		code, out, errOut := run(args...)
		ok := false
		for _, allowed := range item.allowed {
			if code == allowed {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s exit = %d, stderr = %s", item.name, code, errOut)
			continue
		}
		if !strings.Contains(out, item.needle) {
			t.Errorf("%s output does not mention %q", item.name, item.needle)
		}
	}
}

func TestDetailFlagsAddSections(t *testing.T) {
	store := ingestExample(t)
	_, plain, _ := run("detect", "--store", store, "--config", configPath())
	_, detailed, _ := run("detect", "--store", store, "--config", configPath(), "--detail")
	if len(detailed) <= len(plain) {
		t.Fatal("--detail did not add anything")
	}
	if !strings.Contains(detailed, "Trend detail") {
		t.Fatal("the detail section is missing")
	}
	code, out, errOut := run("detect", "--store", store, "--config", configPath(), "--site", "WW-KELDMOOR")
	if code != ExitOK && code != ExitVerdict {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "Trend detail for WW-KELDMOOR") {
		t.Fatalf("site detail missing: %s", out)
	}
	code, _, errOut = run("detect", "--store", store, "--config", configPath(), "--site", "GHOST")
	if code != ExitError || !strings.Contains(errOut, "no trend for site") {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	code, out, _ = run("qc", "--store", store, "--config", configPath(), "--result", "RES-KLD-0001-1")
	if code != ExitOK && code != ExitVerdict {
		t.Fatalf("qc detail exit = %d", code)
	}
	if !strings.Contains(out, "Checks for RES-KLD-0001-1") {
		t.Fatalf("result detail missing: %s", out)
	}
	code, _, errOut = run("qc", "--store", store, "--config", configPath(), "--result", "RES-GHOST")
	if code != ExitError || !strings.Contains(errOut, "no assessment for result") {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
}

func TestOutFlagWritesAFile(t *testing.T) {
	store := ingestExample(t)
	target := filepath.Join(t.TempDir(), "nested", "out.json")
	code, out, errOut := run("report", "--store", store, "--config", configPath(),
		"--format", "json", "--out", target)
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if out != "" {
		t.Fatalf("standard output should be empty when --out is given: %q", out)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var document struct {
		Notice   string `json:"notice"`
		Analysis struct {
			CatchmentID string `json:"catchment_id"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("json: %v", err)
	}
	if document.Analysis.CatchmentID != "vellamoor-basin" {
		t.Fatalf("document = %+v", document)
	}
	if !strings.Contains(document.Notice, "study tool") {
		t.Fatalf("notice = %q", document.Notice)
	}
}

func TestAlertLifecycleAcrossRuns(t *testing.T) {
	store := ingestExample(t)
	first := []string{"alert", "--store", store, "--config", configPath(),
		"--as-of", "2026-03-20T12:00:00Z", "--write", "--format", "json"}
	code, out, errOut := run(first...)
	if code != ExitOK && code != ExitVerdict {
		t.Fatalf("first alert exit = %d, stderr = %s", code, errOut)
	}
	var summary struct {
		Written bool `json:"snapshot_written"`
		Counts  struct {
			Total     int `json:"total"`
			Open      int `json:"open"`
			Sustained int `json:"sustained"`
		} `json:"counts"`
		Audit []struct {
			Action string `json:"action"`
		} `json:"audit_entries"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !summary.Written {
		t.Fatal("the snapshot was not written")
	}
	if len(summary.Audit) != 2 {
		t.Fatalf("audit entries = %d", len(summary.Audit))
	}
	openedTotal := summary.Counts.Total
	if openedTotal == 0 || summary.Counts.Open == 0 {
		t.Fatalf("the first run opened nothing: %+v", summary.Counts)
	}

	second := []string{"alert", "--store", store, "--config", configPath(),
		"--as-of", "2026-03-24T12:00:00Z", "--write", "--format", "json"}
	code, out, errOut = run(second...)
	if code != ExitOK && code != ExitVerdict {
		t.Fatalf("second alert exit = %d, stderr = %s", code, errOut)
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("json: %v", err)
	}
	if summary.Counts.Total < openedTotal {
		t.Fatalf("alerts were lost between runs: %d then %d", openedTotal, summary.Counts.Total)
	}
	if summary.Counts.Sustained == 0 {
		t.Fatalf("no alert reached the sustained state: %+v", summary.Counts)
	}
	// The chain still verifies after two write cycles.
	code, out, errOut = run("verify", "--store", store)
	if code != ExitOK {
		t.Fatalf("verify exit = %d, stdout = %s, stderr = %s", code, out, errOut)
	}
}

func TestAlertWithoutAStoreDoesNotPersist(t *testing.T) {
	args := append([]string{"alert", "--config", configPath()}, sourceFlags()...)
	code, out, errOut := run(args...)
	if code != ExitOK && code != ExitVerdict {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if strings.Contains(out, "Persistence") {
		t.Fatal("a run without a store reported persistence")
	}
	if !strings.Contains(out, "Alert ledger") {
		t.Fatalf("output = %s", out)
	}
}

func TestAlertRejectsAForeignLedgerSchema(t *testing.T) {
	store := ingestExample(t)
	if err := os.WriteFile(filepath.Join(store, "alerts.json"),
		[]byte(`{"schema_version":"other","config_fingerprint":"x","alerts":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, errOut := run("alert", "--store", store, "--config", configPath())
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "schema_version") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestVerifyDetectsAnEditedLedger(t *testing.T) {
	store := ingestExample(t)
	auditPath := filepath.Join(store, "audit.log")
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := strings.Replace(string(raw), "append-samples", "append-samplez", 1)
	if edited == string(raw) {
		t.Fatal("the test failed to edit the chain")
	}
	if err := os.WriteFile(auditPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, out, _ := run("verify", "--store", store, "--format", "json")
	if code != ExitVerdict {
		t.Fatalf("exit = %d, want the verdict code", code)
	}
	var summary struct {
		Report struct {
			Verified bool     `json:"verified"`
			Problems []string `json:"problems"`
		} `json:"audit"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("json: %v", err)
	}
	if summary.Report.Verified || len(summary.Report.Problems) == 0 {
		t.Fatalf("report = %+v", summary.Report)
	}
}

func TestAsOfMustBeUTC(t *testing.T) {
	args := append([]string{"detect", "--as-of", "2026-03-20T12:00:00+01:00"}, sourceFlags()...)
	code, _, errOut := run(args...)
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "only UTC is accepted") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRepeatedRunsProduceIdenticalOutput(t *testing.T) {
	store := ingestExample(t)
	args := []string{"report", "--store", store, "--config", configPath(), "--format", "json"}
	_, first, _ := run(args...)
	_, second, _ := run(args...)
	if first != second {
		t.Fatal("two identical report runs differ")
	}
	textArgs := []string{"report", "--store", store, "--config", configPath()}
	_, firstText, _ := run(textArgs...)
	_, secondText, _ := run(textArgs...)
	if firstText != secondText {
		t.Fatal("two identical text report runs differ")
	}
	if !strings.HasSuffix(firstText, "\n") {
		t.Fatal("the text report is not newline terminated")
	}
}

func TestExplicitFilesWinOverAStore(t *testing.T) {
	store := ingestExample(t)
	args := append([]string{"validate", "--store", store}, sourceFlags()...)
	code, out, _ := run(args...)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "input read from input files") {
		t.Fatalf("stdout = %s", out)
	}
	code, out, _ = run("validate", "--store", store)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "input read from store") {
		t.Fatalf("stdout = %s", out)
	}
}

func TestBadConfigIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"fluwatershed-config/v1"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, errOut := run("profile", "--config", path)
	if code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut, "validation problem") {
		t.Fatalf("stderr = %q", errOut)
	}
}
