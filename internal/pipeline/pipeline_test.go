package pipeline

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/corroborate"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/timeutil"
)

const examples = "../../examples"

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func exampleSources() Sources {
	return Sources{
		NetworkPath: filepath.Join(examples, "network.json"),
		SamplesPath: filepath.Join(examples, "samples.jsonl"),
		ResultsPath: filepath.Join(examples, "results.jsonl"),
		EventsPath:  filepath.Join(examples, "events.jsonl"),
	}
}

func TestLoadRequiresANetworkPath(t *testing.T) {
	if _, err := Load(Sources{}); err == nil {
		t.Fatal("an empty source set was accepted")
	}
	if _, err := Load(Sources{NetworkPath: filepath.Join(examples, "absent.json")}); err == nil {
		t.Fatal("a missing catchment file was accepted")
	}
}

func TestLoadAndValidateTheBundledExample(t *testing.T) {
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bundle.Network.Sites) == 0 || len(bundle.Samples) == 0 || len(bundle.Results) == 0 {
		t.Fatalf("the example looks empty: %d sites, %d samples, %d results",
			len(bundle.Network.Sites), len(bundle.Samples), len(bundle.Results))
	}
	report := Validate(bundle)
	if !report.Valid {
		t.Fatalf("the bundled example does not validate: %v", report.Problems)
	}
	if err := report.Err(); err != nil {
		t.Fatalf("Err on a valid report = %v", err)
	}
	if len(report.Topology) != len(bundle.Network.Sites) {
		t.Fatalf("topology covers %d of %d sites", len(report.Topology), len(bundle.Network.Sites))
	}
	if len(report.Headwaters) == 0 || len(report.Outlets) == 0 {
		t.Fatalf("headwaters %v outlets %v", report.Headwaters, report.Outlets)
	}
	if !strings.Contains(report.TopologyNote, "acyclic") {
		t.Fatalf("topology note = %q", report.TopologyNote)
	}
}

func TestValidateReportsACycle(t *testing.T) {
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	outlet := bundle.Network.Links[len(bundle.Network.Links)-1].DownstreamSiteID
	headwater := bundle.Network.Links[0].UpstreamSiteID
	bundle.Network.Links = append(bundle.Network.Links, model.Link{
		UpstreamSiteID: outlet, DownstreamSiteID: headwater, TravelHours: 1,
	})
	report := Validate(bundle)
	if report.Valid {
		t.Fatal("a cyclic catchment validated")
	}
	if !strings.Contains(report.TopologyNote, "cyclic") {
		t.Fatalf("topology note = %q", report.TopologyNote)
	}
	if err := report.Err(); err == nil {
		t.Fatal("Err returned nil for an invalid report")
	}
}

func TestValidateReportsStructuralProblems(t *testing.T) {
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bundle.Samples[0].VolumeML = 0
	report := Validate(bundle)
	if report.Valid {
		t.Fatal("a sample with no volume validated")
	}
	if len(report.Problems) == 0 {
		t.Fatal("no problems were listed")
	}
	for index := 1; index < len(report.Problems); index++ {
		left, right := report.Problems[index-1], report.Problems[index]
		if left.Scope > right.Scope {
			t.Fatalf("problems are not sorted: %v", report.Problems)
		}
	}
}

func TestResolveAsOfPrefersTheOverrideThenTheNewestInstant(t *testing.T) {
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	override := stamp("2026-01-01T00:00:00Z")
	got, err := ResolveAsOf(bundle, override)
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	if !got.Equal(override) {
		t.Fatalf("override ignored, got %s", got)
	}
	derived, err := ResolveAsOf(bundle, timeutil.Stamp{})
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	for _, result := range bundle.Results {
		if result.AnalysedAt.After(derived) {
			t.Fatalf("result %s is newer than the derived instant %s", result.ResultID, derived)
		}
	}
	if _, err := ResolveAsOf(model.Bundle{}, timeutil.Stamp{}); err == nil {
		t.Fatal("an input with no timestamps produced a reporting instant")
	}
}

func TestRunProducesACompleteAnalysis(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asOf, err := ResolveAsOf(bundle, timeutil.Stamp{})
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	analysis, err := Run(cfg, bundle, asOf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if analysis.SchemaVersion != SnapshotSchemaVersion {
		t.Fatalf("schema version = %q", analysis.SchemaVersion)
	}
	if analysis.ConfigFingerprint != cfg.Fingerprint() {
		t.Fatal("the analysis does not carry the policy fingerprint")
	}
	if analysis.Counts.Measurements == 0 || analysis.Counts.UsablePoints == 0 {
		t.Fatalf("counts = %+v", analysis.Counts)
	}
	if analysis.Counts.Sites != len(bundle.Network.Sites) {
		t.Fatalf("sites = %d", analysis.Counts.Sites)
	}
	if len(analysis.Assessments) != len(analysis.Measurements) {
		t.Fatalf("%d measurements produced %d assessments",
			len(analysis.Measurements), len(analysis.Assessments))
	}
	if len(analysis.Points) != len(analysis.Measurements) {
		t.Fatalf("%d measurements produced %d points", len(analysis.Measurements), len(analysis.Points))
	}
	if len(analysis.Signals) != len(bundle.Network.Sites) {
		t.Fatalf("signals = %d", len(analysis.Signals))
	}
	if len(analysis.RollUps) != len(bundle.Network.Sites) {
		t.Fatalf("roll-ups = %d", len(analysis.RollUps))
	}
	if len(analysis.Topology) != len(bundle.Network.Sites) {
		t.Fatalf("topology = %v", analysis.Topology)
	}
	if analysis.QualityTally.Total != len(analysis.Assessments) {
		t.Fatalf("tally = %+v", analysis.QualityTally)
	}
	if analysis.QualityTally.Failed == 0 {
		t.Fatal("the example is meant to include deliberately failing results")
	}
	if len(analysis.FailedResults()) != analysis.QualityTally.Failed {
		t.Fatalf("failed list = %v", analysis.FailedResults())
	}
	if analysis.SignalIndex()[analysis.Signals[0].SiteID].SiteID != analysis.Signals[0].SiteID {
		t.Fatal("signal index lookup failed")
	}
	if len(analysis.TrendIndex()) != len(analysis.Trends) {
		t.Fatal("trend index lookup failed")
	}
}

func TestFailingResultsNeverReachATrend(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asOf, err := ResolveAsOf(bundle, timeutil.Stamp{})
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	analysis, err := Run(cfg, bundle, asOf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	barred := map[string]bool{}
	for _, id := range analysis.FailedResults() {
		barred[id] = true
	}
	if len(barred) == 0 {
		t.Skip("the example carries no failing result to check")
	}
	for _, point := range analysis.Points {
		if barred[point.ResultID] && point.Usable {
			t.Fatalf("result %s failed quality control but is marked usable", point.ResultID)
		}
	}
	for _, series := range analysis.Series {
		for _, bin := range series.Days {
			for _, id := range bin.ResultIDs {
				if barred[id] {
					t.Fatalf("result %s failed quality control but appears in the %s series",
						id, series.SiteID)
				}
			}
		}
	}
}

func TestRunIsDeterministic(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	asOf, err := ResolveAsOf(bundle, timeutil.Stamp{})
	if err != nil {
		t.Fatalf("ResolveAsOf: %v", err)
	}
	first, err := Run(cfg, bundle, asOf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	firstBytes, err := first.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Shuffle the input order: a deterministic pipeline must not notice.
	shuffled := model.Bundle{Network: bundle.Network}
	for index := len(bundle.Samples) - 1; index >= 0; index-- {
		shuffled.Samples = append(shuffled.Samples, bundle.Samples[index])
	}
	for index := len(bundle.Results) - 1; index >= 0; index-- {
		shuffled.Results = append(shuffled.Results, bundle.Results[index])
	}
	for index := len(bundle.Events) - 1; index >= 0; index-- {
		shuffled.Events = append(shuffled.Events, bundle.Events[index])
	}
	for index := len(shuffled.Network.Sites) - 1; index >= 0; index-- {
		shuffled.Network.Sites = append(shuffled.Network.Sites, bundle.Network.Sites[index])
	}
	shuffled.Network.Sites = shuffled.Network.Sites[len(bundle.Network.Sites):]
	second, err := Run(cfg, shuffled, asOf)
	if err != nil {
		t.Fatalf("Run on reordered input: %v", err)
	}
	secondBytes, err := second.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("reordering the input changed the analysis document")
	}
}

func TestRunRejectsACyclicCatchment(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	outlet := bundle.Network.Links[len(bundle.Network.Links)-1].DownstreamSiteID
	headwater := bundle.Network.Links[0].UpstreamSiteID
	bundle.Network.Links = append(bundle.Network.Links, model.Link{
		UpstreamSiteID: outlet, DownstreamSiteID: headwater, TravelHours: 1,
	})
	if _, err := Run(cfg, bundle, stamp("2026-03-28T10:00:00Z")); err == nil {
		t.Fatal("a cyclic catchment was analysed")
	}
}

func TestAnalysisSignalsUseTheConfiguredGrades(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	analysis, err := Run(cfg, bundle, stamp("2026-03-28T10:00:00Z"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	known := map[corroborate.Grade]bool{}
	for _, grade := range corroborate.Grades() {
		known[grade] = true
	}
	for _, signal := range analysis.Signals {
		if !known[signal.Grade] {
			t.Fatalf("site %s carries an unknown grade %q", signal.SiteID, signal.Grade)
		}
		if signal.Score < 0 || signal.Score > 1 {
			t.Fatalf("site %s score %v is outside the unit interval", signal.SiteID, signal.Score)
		}
	}
	if corroborate.Rank(corroborate.Strongest(analysis.Signals)) == 0 {
		t.Fatal("the example should raise at least one graded signal")
	}
}

func TestFlowNormalisedSitesCarryLoadsAndConcentrationsElsewhere(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	analysis, err := Run(cfg, bundle, stamp("2026-03-28T10:00:00Z"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sites := map[string]model.Site{}
	for _, site := range bundle.Network.Sites {
		sites[site.SiteID] = site
	}
	for _, point := range analysis.Points {
		site := sites[point.SiteID]
		if site.Matrix.FlowNormalisable() {
			if point.SignalUnit != normalize.UnitCopiesPerDay {
				t.Fatalf("influent point %s is on %s", point.ResultID, point.SignalUnit)
			}
			if point.Status == quant.StatusQuantified && point.LoadCopiesPerDay <= 0 {
				t.Fatalf("quantified influent point %s carries no load", point.ResultID)
			}
			if point.LoadCopiesPerDay > 0 && point.LoadPerPersonDay <= 0 {
				t.Fatalf("influent point %s has a load but no per capita figure", point.ResultID)
			}
			continue
		}
		if point.SignalUnit != normalize.UnitCopiesPerLitre {
			t.Fatalf("point %s at a %s site is on %s", point.ResultID, site.Matrix, point.SignalUnit)
		}
	}
}

func TestQualityCountsMatchTheAssessmentVerdicts(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	analysis, err := Run(cfg, bundle, stamp("2026-03-28T10:00:00Z"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	counted := qc.Count(analysis.Assessments)
	if counted != analysis.QualityTally {
		t.Fatalf("tally %+v does not match a recount %+v", analysis.QualityTally, counted)
	}
	failures := qc.FailureReasons(analysis.Assessments)
	if len(failures) == 0 {
		t.Fatal("the example should exercise at least one failing rule")
	}
	total := 0
	for _, failure := range failures {
		total += int(failure.Observed)
	}
	if total < counted.Failed {
		t.Fatalf("%d failing results explained by %d rule failures", counted.Failed, total)
	}
}

func TestLoadRejectsAnUnknownMemberInALedger(t *testing.T) {
	dir := t.TempDir()
	network, err := os.ReadFile(filepath.Join(examples, "network.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	networkPath := filepath.Join(dir, "network.json")
	if err := os.WriteFile(networkPath, network, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	samplesPath := filepath.Join(dir, "samples.jsonl")
	if err := os.WriteFile(samplesPath, []byte("{\"sample_id\":\"SMP-1\",\"mystery\":1}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = Load(Sources{NetworkPath: networkPath, SamplesPath: samplesPath})
	if err == nil {
		t.Fatal("an unknown member in a ledger was accepted")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("error should name the member: %v", err)
	}
}

func TestBaselineAndTrendCoverEveryQuantitativeSite(t *testing.T) {
	cfg := config.Default()
	bundle, err := Load(exampleSources())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	analysis, err := Run(cfg, bundle, stamp("2026-03-28T10:00:00Z"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	quantitative := 0
	for _, site := range bundle.Network.Sites {
		if site.Matrix.Quantitative() {
			quantitative++
		}
	}
	if len(analysis.Trends) != quantitative {
		t.Fatalf("trends = %d, want %d", len(analysis.Trends), quantitative)
	}
	for _, item := range analysis.Trends {
		if item.Summary == "" {
			t.Errorf("site %s has no summary", item.SiteID)
		}
		if math.IsNaN(item.Slope.Slope) {
			t.Errorf("site %s slope is not a number", item.SiteID)
		}
	}
	if len(analysis.Baselines) == 0 {
		t.Fatal("no baselines were produced")
	}
}
