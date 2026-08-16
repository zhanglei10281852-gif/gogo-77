package corroborate

import (
	"math"
	"strings"
	"testing"

	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func testNetwork() model.Network {
	return model.Network{
		SchemaVersion: model.SchemaVersion,
		CatchmentID:   "test-basin",
		Label:         "test",
		Sites: []model.Site{
			{SiteID: "UPPER", Label: "upper", Matrix: model.MatrixWastewaterInfluent,
				Latitude: 45.21, Longitude: -95.44, ServedPopulation: 10000, MeanDailyFlowM3: 4000},
			{SiteID: "BROOK", Label: "brook", Matrix: model.MatrixWaterwayGrab,
				Latitude: 45.185, Longitude: -95.39},
			{SiteID: "POOL", Label: "pool", Matrix: model.MatrixSediment,
				Latitude: 45.09, Longitude: -95.16},
		},
		Links: []model.Link{
			{UpstreamSiteID: "UPPER", DownstreamSiteID: "BROOK", TravelHours: 6},
			{UpstreamSiteID: "BROOK", DownstreamSiteID: "POOL", TravelHours: 12},
		},
	}
}

func testGraph(t *testing.T) *catchment.Graph {
	t.Helper()
	graph, err := catchment.Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return graph
}

func siteTrend(id string, sustained, flagged bool, slope float64, detectionDays int) trend.SiteTrend {
	direction := trend.DirectionFlat
	if slope >= 0.02 {
		direction = trend.DirectionRising
	}
	return trend.SiteTrend{
		SiteID:             id,
		LatestExceeded:     flagged,
		SustainedRise:      sustained,
		ConsecutiveFlagged: 3,
		LatestDay:          "2026-03-20",
		DetectionDays:      detectionDays,
		ObservedDays:       10,
		Slope:              trend.Slope{Slope: slope, Sufficient: true, Direction: direction},
	}
}

func TestGradeRanksAndParsing(t *testing.T) {
	if Rank(GradeHigh) <= Rank(GradeModerate) || Rank(GradeModerate) <= Rank(GradeLow) ||
		Rank(GradeLow) <= Rank(GradeNone) {
		t.Fatal("grade ranks are not ordered")
	}
	for _, grade := range Grades() {
		parsed, err := ParseGrade(string(grade))
		if err != nil || parsed != grade {
			t.Errorf("ParseGrade(%s) = %s, %v", grade, parsed, err)
		}
	}
	if _, err := ParseGrade("extreme"); err == nil {
		t.Error("an unknown grade was accepted")
	}
	if len(Streams()) != 4 {
		t.Fatalf("streams = %v", Streams())
	}
}

func TestEvaluateCombinesWeightedStreams(t *testing.T) {
	cfg := config.Default()
	graph := testGraph(t)
	site, _ := graph.Site("POOL")
	trends := map[string]trend.SiteTrend{
		"UPPER": siteTrend("UPPER", true, true, 0.06, 6), // strength 1.00, weight 0.45
		"BROOK": siteTrend("BROOK", false, true, 0.0, 3), // strength 0.75, weight 0.25
		"POOL":  siteTrend("POOL", false, false, 0.0, 0), // strength 0.00, weight 0.10
	}
	events := []model.CarcassEvent{
		{EventID: "EVT-1", ObservedAt: stamp("2026-03-18T09:00:00Z"),
			Latitude: 45.10, Longitude: -95.17, Species: "invented tern", CarcassCount: 30, H5Confirmed: true},
	}
	signal := Evaluate(cfg, graph, site, trends, events, stamp("2026-03-20T12:00:00Z"))
	if signal.ApplicableStreams != 4 {
		t.Fatalf("applicable streams = %d, want all four", signal.ApplicableStreams)
	}
	if signal.PresentStreams != 3 {
		t.Fatalf("present streams = %d, want three", signal.PresentStreams)
	}
	// (0.45*1.00 + 0.25*0.75 + 0.10*0.00 + 0.20*1.00) / 1.00 = 0.8375
	if math.Abs(signal.Score-0.8375) > 1e-4 {
		t.Fatalf("score = %v, want 0.8375", signal.Score)
	}
	if signal.Grade != GradeHigh {
		t.Fatalf("grade = %s", signal.Grade)
	}
	if signal.Capped {
		t.Fatal("the grade was capped despite three contributing streams")
	}
	if !strings.Contains(signal.Rationale, "score 0.8375") {
		t.Fatalf("rationale = %q", signal.Rationale)
	}
}

func TestMinimumStreamsCapsTheGrade(t *testing.T) {
	cfg := config.Default()
	cfg.Corroboration.MinEvidenceStreams = 3
	graph := testGraph(t)
	site, _ := graph.Site("POOL")
	trends := map[string]trend.SiteTrend{
		"UPPER": siteTrend("UPPER", true, true, 0.06, 6),
		"BROOK": siteTrend("BROOK", false, false, 0.0, 0),
		"POOL":  siteTrend("POOL", false, false, 0.0, 0),
	}
	signal := Evaluate(cfg, graph, site, trends, nil, stamp("2026-03-20T12:00:00Z"))
	if signal.PresentStreams != 1 {
		t.Fatalf("present streams = %d", signal.PresentStreams)
	}
	if signal.RawGrade == GradeLow {
		t.Fatalf("the raw grade should have exceeded low, got %s at score %v", signal.RawGrade, signal.Score)
	}
	if signal.Grade != GradeLow {
		t.Fatalf("grade = %s, want the cap at low", signal.Grade)
	}
	if !signal.Capped {
		t.Fatal("the capping was not recorded")
	}
	if signal.MinStreamsMet {
		t.Fatal("the minimum was reported as met")
	}
	if !strings.Contains(signal.Rationale, "held at low") {
		t.Fatalf("rationale = %q", signal.Rationale)
	}
}

func TestCarcassEvidenceRespectsRadiusAndWindow(t *testing.T) {
	cfg := config.Default()
	graph := testGraph(t)
	site, _ := graph.Site("UPPER")
	trends := map[string]trend.SiteTrend{"UPPER": siteTrend("UPPER", false, false, 0, 0)}
	asOf := stamp("2026-03-20T12:00:00Z")

	// Far outside the twenty five kilometre radius.
	far := []model.CarcassEvent{{EventID: "EVT-FAR", ObservedAt: stamp("2026-03-19T09:00:00Z"),
		Latitude: 47.5, Longitude: -95.44, Species: "invented gull", CarcassCount: 80, H5Confirmed: true}}
	signal := Evaluate(cfg, graph, site, trends, far, asOf)
	carcass := findStream(t, signal, StreamCarcass)
	if carcass.Applicable {
		t.Fatalf("a distant event was treated as coverage: %s", carcass.Detail)
	}

	// Inside the radius but outside the fourteen day window.
	stale := []model.CarcassEvent{{EventID: "EVT-OLD", ObservedAt: stamp("2026-01-05T09:00:00Z"),
		Latitude: 45.22, Longitude: -95.45, Species: "invented gull", CarcassCount: 80, H5Confirmed: true}}
	signal = Evaluate(cfg, graph, site, trends, stale, asOf)
	carcass = findStream(t, signal, StreamCarcass)
	if !carcass.Applicable {
		t.Fatal("a nearby site should have carcass coverage")
	}
	if carcass.Strength != 0 {
		t.Fatalf("a stale event contributed strength %v", carcass.Strength)
	}
	if !strings.Contains(carcass.Detail, "day window") {
		t.Fatalf("detail = %q", carcass.Detail)
	}

	// Inside both: a small cluster with a confirmation reaches the floor.
	small := []model.CarcassEvent{{EventID: "EVT-SMALL", ObservedAt: stamp("2026-03-19T09:00:00Z"),
		Latitude: 45.22, Longitude: -95.45, Species: "invented gull", CarcassCount: 3, H5Confirmed: true}}
	signal = Evaluate(cfg, graph, site, trends, small, asOf)
	carcass = findStream(t, signal, StreamCarcass)
	if math.Abs(carcass.Strength-strengthConfirmed) > 1e-6 {
		t.Fatalf("strength = %v, want the confirmed floor %v", carcass.Strength, strengthConfirmed)
	}
	if !strings.Contains(carcass.Detail, "confirmed H5") {
		t.Fatalf("detail = %q", carcass.Detail)
	}

	// A large cluster saturates at one.
	large := []model.CarcassEvent{{EventID: "EVT-BIG", ObservedAt: stamp("2026-03-19T09:00:00Z"),
		Latitude: 45.22, Longitude: -95.45, Species: "invented gull", CarcassCount: 500}}
	signal = Evaluate(cfg, graph, site, trends, large, asOf)
	carcass = findStream(t, signal, StreamCarcass)
	if carcass.Strength != 1 {
		t.Fatalf("strength = %v, want 1", carcass.Strength)
	}
}

func TestCarcassEvidenceCanBeSwitchedOff(t *testing.T) {
	cfg := config.Default()
	cfg.Corroboration.CarcassWeight = 0
	graph := testGraph(t)
	site, _ := graph.Site("UPPER")
	events := []model.CarcassEvent{{EventID: "EVT-1", ObservedAt: stamp("2026-03-19T09:00:00Z"),
		Latitude: 45.22, Longitude: -95.45, Species: "invented gull", CarcassCount: 80, H5Confirmed: true}}
	signal := Evaluate(cfg, graph, site, map[string]trend.SiteTrend{}, events, stamp("2026-03-20T12:00:00Z"))
	carcass := findStream(t, signal, StreamCarcass)
	if carcass.Applicable {
		t.Fatal("a zero weighted stream was still applicable")
	}
	if !strings.Contains(carcass.Detail, "no weight") {
		t.Fatalf("detail = %q", carcass.Detail)
	}
}

func TestUpstreamCoverageOnlyCountsInScope(t *testing.T) {
	cfg := config.Default()
	graph := testGraph(t)
	upper, _ := graph.Site("UPPER")
	trends := map[string]trend.SiteTrend{
		"UPPER": siteTrend("UPPER", false, false, 0, 0),
		"BROOK": siteTrend("BROOK", true, true, 0.09, 8),
	}
	// A headwater has no ancestors, so the strong downstream grab must not raise it.
	signal := Evaluate(cfg, graph, upper, trends, nil, stamp("2026-03-20T12:00:00Z"))
	grab := findStream(t, signal, StreamWaterway)
	if grab.Applicable {
		t.Fatalf("a downstream grab was pulled into an upstream scope: %s", grab.Detail)
	}
	// The sediment outlet sees everything above it.
	pool, _ := graph.Site("POOL")
	signal = Evaluate(cfg, graph, pool, trends, nil, stamp("2026-03-20T12:00:00Z"))
	grab = findStream(t, signal, StreamWaterway)
	if !grab.Applicable || grab.Strength != strengthSustained {
		t.Fatalf("outlet grab evidence = %+v", grab)
	}
	if strings.Join(grab.SourceSites, ",") != "BROOK" {
		t.Fatalf("source sites = %v", grab.SourceSites)
	}
}

func TestTrendStrengthLadder(t *testing.T) {
	cases := []struct {
		item trend.SiteTrend
		want float64
	}{
		{siteTrend("A", true, true, 0.09, 5), strengthSustained},
		{siteTrend("A", false, true, 0.0, 5), strengthFlagged},
		{siteTrend("A", false, false, 0.09, 5), strengthRising},
		{siteTrend("A", false, false, 0.0, 5), strengthDetected},
		{siteTrend("A", false, false, 0.0, 0), 0},
	}
	for index, item := range cases {
		got, detail := trendStrength(item.item)
		if got != item.want {
			t.Errorf("case %d strength = %v, want %v", index, got, item.want)
		}
		if detail == "" {
			t.Errorf("case %d carries no explanation", index)
		}
	}
}

func TestEvaluateAllCoversEverySiteAndSorts(t *testing.T) {
	cfg := config.Default()
	graph := testGraph(t)
	trends := []trend.SiteTrend{
		siteTrend("UPPER", true, true, 0.06, 6),
		siteTrend("BROOK", false, false, 0, 0),
	}
	signals := EvaluateAll(cfg, graph, trends, nil, stamp("2026-03-20T12:00:00Z"))
	if len(signals) != 3 {
		t.Fatalf("signals = %d, want one per site", len(signals))
	}
	if signals[0].SiteID != "BROOK" || signals[2].SiteID != "UPPER" {
		t.Fatalf("order = %s, %s, %s", signals[0].SiteID, signals[1].SiteID, signals[2].SiteID)
	}
	if Index(signals)["POOL"].SiteID != "POOL" {
		t.Fatal("index lookup failed")
	}
	if Rank(Strongest(signals)) == 0 {
		t.Fatalf("strongest grade = %s", Strongest(signals))
	}
}

func TestGradeCutPoints(t *testing.T) {
	cfg := config.Default()
	cases := []struct {
		score float64
		want  Grade
	}{
		{0, GradeNone},
		{0.19, GradeNone},
		{0.2, GradeLow},
		{0.44, GradeLow},
		{0.45, GradeModerate},
		{0.69, GradeModerate},
		{0.7, GradeHigh},
		{1, GradeHigh},
	}
	for _, item := range cases {
		if got := gradeFor(cfg, item.score); got != item.want {
			t.Errorf("score %v graded %s, want %s", item.score, got, item.want)
		}
	}
}

func findStream(t *testing.T, signal Signal, stream Stream) Evidence {
	t.Helper()
	for _, evidence := range signal.Evidence {
		if evidence.Stream == stream {
			return evidence
		}
	}
	t.Fatalf("signal for %s carries no %s stream", signal.SiteID, stream)
	return Evidence{}
}
