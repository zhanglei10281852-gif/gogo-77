package report

import (
	"math"
	"strings"
	"testing"

	"FluWatershed/internal/alert"
	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/pipeline"
	"FluWatershed/internal/store"
	"FluWatershed/internal/timeutil"
)

func TestTableRendersFixedWidthColumns(t *testing.T) {
	table := NewTable("SITE", "VALUE").RightAlign(1)
	table.Add("SHORT", "1")
	table.Add("A-MUCH-LONGER-SITE", "1000")
	lines := table.Render()
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want a header, a rule and two rows", len(lines))
	}
	if !strings.HasPrefix(lines[1], "----") {
		t.Fatalf("second line should be a rule: %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "   1") {
		t.Fatalf("the value column is not right aligned: %q", lines[2])
	}
	if strings.HasSuffix(lines[3], " ") {
		t.Fatalf("trailing spaces were left on %q", lines[3])
	}
	if table.Rows() != 2 {
		t.Fatalf("rows = %d", table.Rows())
	}
}

func TestTableToleratesRaggedRows(t *testing.T) {
	table := NewTable("A", "B", "C")
	table.Add("one")
	table.Add("one", "two", "three", "four")
	lines := table.Render()
	if len(lines) != 4 {
		t.Fatalf("lines = %d", len(lines))
	}
	if strings.Contains(lines[3], "four") {
		t.Fatalf("an extra cell leaked into the output: %q", lines[3])
	}
}

func TestTableIsDeterministic(t *testing.T) {
	build := func() []string {
		table := NewTable("A", "B").RightAlign(1)
		table.Add("x", "1")
		table.Add("yy", "22")
		return table.Render()
	}
	if strings.Join(build(), "\n") != strings.Join(build(), "\n") {
		t.Fatal("two renders of the same table differ")
	}
}

func TestSciFormatsAcrossMagnitudes(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{1.5, "1.5"},
		{1234, "1234"},
		{1.234e11, "1.234e+11"},
		{0.0000012, "1.200e-06"},
	}
	for _, item := range cases {
		if got := Sci(item.value); got != item.want {
			t.Errorf("Sci(%v) = %q, want %q", item.value, got, item.want)
		}
	}
	if Sci(math.NaN()) != "n/a" {
		t.Error("a non-number should render as n/a")
	}
	if Sci(math.Inf(1)) != "n/a" {
		t.Error("infinity should render as n/a")
	}
}

func TestFormattingHelpers(t *testing.T) {
	if Fixed(1.005, 2) != "1.00" && Fixed(1.005, 2) != "1.01" {
		t.Errorf("Fixed produced %q", Fixed(1.005, 2))
	}
	if Fixed(math.NaN(), 2) != "n/a" {
		t.Error("Fixed should guard non-numbers")
	}
	if YesNo(true) != "yes" || YesNo(false) != "no" {
		t.Error("YesNo is wrong")
	}
	if Dash("") != "-" || Dash("  ") != "-" || Dash("x") != "x" {
		t.Error("Dash is wrong")
	}
	if JoinOrDash(nil) != "-" {
		t.Error("JoinOrDash on an empty list should be a dash")
	}
	if JoinOrDash([]string{"a", "b"}) != "a,b" {
		t.Error("JoinOrDash is wrong")
	}
}

func TestSectionAndKeyValues(t *testing.T) {
	lines := Section("Title", []string{"body"})
	if lines[0] != "Title" || lines[1] != "=====" {
		t.Fatalf("section head = %v", lines[:2])
	}
	if lines[2] != "body" || lines[3] != "" {
		t.Fatalf("section body = %v", lines[2:])
	}
	empty := Section("Title", nil)
	if empty[2] != "(nothing to report)" {
		t.Fatalf("empty section body = %q", empty[2])
	}
	pairs := KeyValues([][2]string{{"short", "1"}, {"a longer key", "2"}})
	if !strings.HasPrefix(pairs[0], "short        ") {
		t.Fatalf("keys are not aligned: %q", pairs[0])
	}
	if Join(nil) != "" {
		t.Error("Join of nothing should be empty")
	}
	if Join([]string{"a", "b"}) != "a\nb\n" {
		t.Errorf("Join = %q", Join([]string{"a", "b"}))
	}
}

func testAnalysis(t *testing.T) (config.Config, pipeline.Analysis, *catchment.Graph) {
	t.Helper()
	cfg := config.Default()
	bundle, err := pipeline.Load(pipeline.Sources{
		NetworkPath: "../../examples/network.json",
		SamplesPath: "../../examples/samples.jsonl",
		ResultsPath: "../../examples/results.jsonl",
		EventsPath:  "../../examples/events.jsonl",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asOf, err := pipeline.ResolveAsOf(bundle, timeutil.Stamp{})
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	analysis, err := pipeline.Run(cfg, bundle, asOf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	graph, err := catchment.Build(bundle.Network)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cfg, analysis, graph
}

func TestEverySectionRendersSomething(t *testing.T) {
	cfg, analysis, graph := testAnalysis(t)
	sections := map[string][]string{
		"header":        Header(analysis, cfg),
		"quantify":      Quantification(analysis),
		"quality":       Quality(analysis),
		"normalisation": Normalisation(analysis),
		"series":        Series(analysis),
		"baselines":     Baselines(analysis),
		"detection":     Detection(analysis),
		"exceedances":   Exceedances(analysis),
		"states":        States(analysis),
		"rollups":       RollUps(analysis),
		"corroboration": Corroboration(analysis),
		"topology":      Topology(graph),
		"grades":        Grades(analysis.Signals),
	}
	for name, lines := range sections {
		if len(lines) < 3 {
			t.Errorf("section %s rendered only %d line(s)", name, len(lines))
		}
		if strings.TrimSpace(lines[0]) == "" {
			t.Errorf("section %s has no title", name)
		}
	}
	if len(Quality(analysis)) < 6 {
		t.Error("the quality section should list assessments")
	}
	if !strings.Contains(strings.Join(Detection(analysis), "\n"), "SUSTAINED") {
		t.Error("the detection table lost its sustained column")
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	cfg, analysis, graph := testAnalysis(t)
	ledger := alert.NewLedger(cfg)
	first := Join(Combined(cfg, analysis, ledger, graph, nil, store.AuditReport{}, ""))
	second := Join(Combined(cfg, analysis, ledger, graph, nil, store.AuditReport{}, ""))
	if first != second {
		t.Fatal("two renders of the same analysis differ")
	}
	if !strings.Contains(first, "study tool") {
		t.Fatal("the standing caution is missing from the combined report")
	}
	if !strings.HasSuffix(first, "\n") {
		t.Fatal("the report is not newline terminated")
	}
}

func TestValidationRenderingListsProblems(t *testing.T) {
	report := pipeline.ValidationReport{
		CatchmentID: "test", Sites: 1, Valid: false,
		TopologyNote: "acyclic",
		Problems: []model.Problem{
			{Scope: "site", Ref: "WW-ONE", Message: "label is required"},
			{Scope: "network", Message: "schema_version is wrong"},
		},
	}
	lines := Validation(report)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "one or more checks failed") {
		t.Fatalf("verdict missing: %s", joined)
	}
	if !strings.Contains(joined, "label is required") {
		t.Fatalf("problem missing: %s", joined)
	}
	clean := pipeline.ValidationReport{CatchmentID: "test", Valid: true, TopologyNote: "acyclic"}
	joined = strings.Join(Validation(clean), "\n")
	if !strings.Contains(joined, "every check passed") {
		t.Fatalf("clean verdict missing: %s", joined)
	}
	if strings.Contains(joined, "Problems") {
		t.Fatalf("a clean report listed a problems section: %s", joined)
	}
}

func TestAuditRenderingShowsChainState(t *testing.T) {
	entries := []store.AuditEntry{{
		Sequence: 1, At: timeutil.MustParse("2026-03-14T08:00:00Z"), Action: "write-network",
		Detail: "six sites", Records: 6,
		PayloadHash: strings.Repeat("a", 64), PreviousHash: store.GenesisHash, Hash: strings.Repeat("b", 64),
	}}
	verified := store.AuditReport{Entries: 1, Verified: true, Chronological: true, HeadHash: strings.Repeat("b", 64)}
	joined := strings.Join(Audit(entries, verified, "/tmp/audit.log"), "\n")
	if !strings.Contains(joined, "verified") || !strings.Contains(joined, "write-network") {
		t.Fatalf("audit rendering = %s", joined)
	}
	broken := store.AuditReport{
		Entries: 1, Verified: false, Chronological: false, FirstBadAt: 1,
		Problems: []string{"entry 1 hash mismatch"},
		Notes:    []string{"entry 1 is out of order"},
	}
	joined = strings.Join(Audit(entries, broken, "/tmp/audit.log"), "\n")
	if !strings.Contains(joined, "Chain problems") || !strings.Contains(joined, "Chain notes") {
		t.Fatalf("broken audit rendering = %s", joined)
	}
}

func TestAlertRenderingCoversHistory(t *testing.T) {
	cfg := config.Default()
	ledger := alert.NewLedger(cfg)
	ledger.Alerts = []alert.Alert{{
		AlertID: "ALR-abc", SiteID: "WW-ONE", State: alert.StateSustained,
		Grade: "high", PeakGrade: "high", Score: 0.8, PeakScore: 0.9,
		OpenedAt:  timeutil.MustParse("2026-03-14T08:00:00Z"),
		UpdatedAt: timeutil.MustParse("2026-03-20T08:00:00Z"),
		Updates:   3, AtOrAboveRun: 3,
		History: []alert.Transition{{
			Kind: alert.KindStateChange, To: alert.StateOpen, Grade: "moderate", Score: 0.5,
			At: timeutil.MustParse("2026-03-14T08:00:00Z"), Reason: "threshold reached",
		}},
	}}
	joined := strings.Join(Alerts(ledger), "\n")
	for _, needle := range []string{"ALR-abc", "sustained", "Transition history", "threshold reached"} {
		if !strings.Contains(joined, needle) {
			t.Errorf("alert rendering does not mention %q", needle)
		}
	}
}

func TestInventoryRendering(t *testing.T) {
	inventory := store.Inventory{Root: "/tmp/store", Files: []store.InventoryFile{
		{Name: "meta.json", Present: true, Bytes: 120},
		{Name: "signals.json", Present: false},
	}}
	joined := strings.Join(Inventory(inventory), "\n")
	if !strings.Contains(joined, "/tmp/store") || !strings.Contains(joined, "meta.json") {
		t.Fatalf("inventory rendering = %s", joined)
	}
}

func TestQualityDetailAndTrendDetail(t *testing.T) {
	_, analysis, _ := testAnalysis(t)
	if len(analysis.Assessments) == 0 || len(analysis.Trends) == 0 {
		t.Fatal("the example produced nothing to render")
	}
	joined := strings.Join(QualityDetail(analysis.Assessments[0]), "\n")
	if !strings.Contains(joined, "CHECK") || !strings.Contains(joined, "SEVERITY") {
		t.Fatalf("quality detail = %s", joined)
	}
	joined = strings.Join(TrendDetail(analysis.Trends[0]), "\n")
	if !strings.Contains(joined, "EXPLANATION") {
		t.Fatalf("trend detail = %s", joined)
	}
}
