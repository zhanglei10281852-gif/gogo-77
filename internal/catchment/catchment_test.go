package catchment

import (
	"math"
	"strings"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

// testNetwork is a small branching catchment:
//
//	UPPER -> BROOK -> WEIR -> POOL
//	MID   ---------> WEIR
//	LOWER ------------------> POOL
func testNetwork() model.Network {
	return model.Network{
		SchemaVersion: model.SchemaVersion,
		CatchmentID:   "test-basin",
		Label:         "test",
		Sites: []model.Site{
			{SiteID: "UPPER", Label: "upper works", Matrix: model.MatrixWastewaterInfluent,
				Latitude: 45.21, Longitude: -95.44, ServedPopulation: 10000, MeanDailyFlowM3: 4000},
			{SiteID: "MID", Label: "mid works", Matrix: model.MatrixWastewaterInfluent,
				Latitude: 45.16, Longitude: -95.32, ServedPopulation: 20000, MeanDailyFlowM3: 8000},
			{SiteID: "LOWER", Label: "lower works", Matrix: model.MatrixWastewaterInfluent,
				Latitude: 45.11, Longitude: -95.21, ServedPopulation: 40000, MeanDailyFlowM3: 12000},
			{SiteID: "BROOK", Label: "brook grab", Matrix: model.MatrixWaterwayGrab,
				Latitude: 45.185, Longitude: -95.39},
			{SiteID: "WEIR", Label: "weir grab", Matrix: model.MatrixWaterwayGrab,
				Latitude: 45.14, Longitude: -95.26},
			{SiteID: "POOL", Label: "pool sediment", Matrix: model.MatrixSediment,
				Latitude: 45.09, Longitude: -95.16},
		},
		Links: []model.Link{
			{UpstreamSiteID: "UPPER", DownstreamSiteID: "BROOK", TravelHours: 6},
			{UpstreamSiteID: "BROOK", DownstreamSiteID: "WEIR", TravelHours: 10},
			{UpstreamSiteID: "MID", DownstreamSiteID: "WEIR", TravelHours: 4},
			{UpstreamSiteID: "WEIR", DownstreamSiteID: "POOL", TravelHours: 14},
			{UpstreamSiteID: "LOWER", DownstreamSiteID: "POOL", TravelHours: 8},
		},
	}
}

func TestBuildOrdersUpstreamFirst(t *testing.T) {
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	order := graph.Order()
	position := make(map[string]int, len(order))
	for index, id := range order {
		position[id] = index
	}
	pairs := [][2]string{
		{"UPPER", "BROOK"}, {"BROOK", "WEIR"}, {"MID", "WEIR"},
		{"WEIR", "POOL"}, {"LOWER", "POOL"},
	}
	for _, pair := range pairs {
		if position[pair[0]] >= position[pair[1]] {
			t.Errorf("%s should precede %s in %v", pair[0], pair[1], order)
		}
	}
	// The order is unique: ties break on identifier, so a rebuild matches exactly.
	again, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if strings.Join(order, ",") != strings.Join(again.Order(), ",") {
		t.Fatalf("two builds disagreed: %v against %v", order, again.Order())
	}
}

func TestBuildRejectsCycles(t *testing.T) {
	network := testNetwork()
	network.Links = append(network.Links, model.Link{
		UpstreamSiteID: "POOL", DownstreamSiteID: "UPPER", TravelHours: 1,
	})
	_, err := Build(network)
	if err == nil {
		t.Fatal("a cyclic topology was accepted")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "->") {
		t.Fatalf("the error should trace the loop: %v", err)
	}
}

func TestBuildRejectsBadLinks(t *testing.T) {
	network := testNetwork()
	network.Links = append(network.Links, model.Link{UpstreamSiteID: "UPPER", DownstreamSiteID: "UPPER"})
	if _, err := Build(network); err == nil {
		t.Error("a self link was accepted")
	}
	network = testNetwork()
	network.Links = append(network.Links, model.Link{UpstreamSiteID: "GHOST", DownstreamSiteID: "POOL"})
	if _, err := Build(network); err == nil {
		t.Error("a link from an unknown site was accepted")
	}
	network = testNetwork()
	network.Links = append(network.Links, network.Links[0])
	if _, err := Build(network); err == nil {
		t.Error("a duplicate link was accepted")
	}
	network = testNetwork()
	network.Sites = append(network.Sites, network.Sites[0])
	if _, err := Build(network); err == nil {
		t.Error("a duplicate site was accepted")
	}
}

func TestAncestorsDescendantsHeadwatersOutlets(t *testing.T) {
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := strings.Join(graph.Ancestors("POOL"), ","); got != "BROOK,LOWER,MID,UPPER,WEIR" {
		t.Fatalf("ancestors of POOL = %s", got)
	}
	if got := strings.Join(graph.Ancestors("UPPER"), ","); got != "" {
		t.Fatalf("ancestors of a headwater = %s", got)
	}
	if got := strings.Join(graph.Descendants("UPPER"), ","); got != "BROOK,POOL,WEIR" {
		t.Fatalf("descendants of UPPER = %s", got)
	}
	if got := strings.Join(graph.Headwaters(), ","); got != "LOWER,MID,UPPER" {
		t.Fatalf("headwaters = %s", got)
	}
	if got := strings.Join(graph.Outlets(), ","); got != "POOL" {
		t.Fatalf("outlets = %s", got)
	}
	if got := strings.Join(graph.DirectUpstream("WEIR"), ","); got != "BROOK,MID" {
		t.Fatalf("direct upstream of WEIR = %s", got)
	}
	if got := strings.Join(graph.DirectDownstream("UPPER"), ","); got != "BROOK" {
		t.Fatalf("direct downstream of UPPER = %s", got)
	}
	if _, ok := graph.Site("GHOST"); ok {
		t.Error("an unknown site was found")
	}
}

func TestTravelHoursTakesTheShortestPath(t *testing.T) {
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if hours, ok := graph.TravelHours("UPPER", "POOL"); !ok || hours != 30 {
		t.Fatalf("UPPER to POOL = %v ok=%v, want 30", hours, ok)
	}
	if hours, ok := graph.TravelHours("MID", "POOL"); !ok || hours != 18 {
		t.Fatalf("MID to POOL = %v ok=%v, want 18", hours, ok)
	}
	if hours, ok := graph.TravelHours("UPPER", "UPPER"); !ok || hours != 0 {
		t.Fatalf("travel to self = %v ok=%v", hours, ok)
	}
	if _, ok := graph.TravelHours("POOL", "UPPER"); ok {
		t.Fatal("an upstream journey was reported as possible")
	}
	if _, ok := graph.TravelHours("UPPER", "LOWER"); ok {
		t.Fatal("two headwaters were reported as connected")
	}
}

func buildPoint(siteID, day string, load, flow, concentration float64, detected bool) normalize.Point {
	at := timeutil.MustParse(day + "T08:00:00Z")
	unit := normalize.UnitCopiesPerLitre
	if load > 0 {
		unit = normalize.UnitCopiesPerDay
	}
	return normalize.Point{
		ResultID:                "RES-" + siteID + "-" + day,
		SampleID:                "SMP-" + siteID + "-" + day,
		SiteID:                  siteID,
		Day:                     day,
		CollectedAt:             at,
		Detected:                detected,
		Usable:                  true,
		TargetGene:              "influenza_a_matrix_gene",
		CopiesPerLitre:          concentration,
		IndicatorFactor:         1,
		CorrectedCopiesPerLitre: concentration,
		FlowLitresPerDay:        flow,
		LoadCopiesPerDay:        load,
		SignalValue:             load,
		SignalUnit:              unit,
	}
}

func TestBuildStatesUsesTheNewestUsableDay(t *testing.T) {
	cfg := config.Default()
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	points := []normalize.Point{
		buildPoint("UPPER", "2026-03-10", 1e10, 4e6, 2500, true),
		buildPoint("UPPER", "2026-03-12", 4e10, 4e6, 10000, true),
	}
	trends := []trend.SiteTrend{{SiteID: "UPPER", LatestExceeded: true, SustainedRise: true}}
	states := BuildStates(cfg, graph, points, trends)
	byID := IndexStates(states)
	upper := byID["UPPER"]
	if upper.Day != "2026-03-12" {
		t.Fatalf("day = %s, want the newest", upper.Day)
	}
	if math.Abs(upper.LoadCopiesPerDay-4e10) > 1 {
		t.Fatalf("load = %v", upper.LoadCopiesPerDay)
	}
	if !upper.Flagged || !upper.SustainedRise {
		t.Fatal("trend flags were not carried into the state")
	}
	quiet := byID["MID"]
	if quiet.Observed {
		t.Fatal("a site with no points was marked observed")
	}
	if len(states) != 6 {
		t.Fatalf("states = %d, want one per site", len(states))
	}
}

func TestPropagateAddsUpstreamLoads(t *testing.T) {
	cfg := config.Default()
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	states := []SiteState{
		{SiteID: "UPPER", Day: "2026-03-12", At: timeutil.MustParse("2026-03-12T00:00:00Z"),
			LoadCopiesPerDay: 1e10, FlowLitresPerDay: 4e6, HasFlow: true, Observed: true, Detected: true, Flagged: true},
		{SiteID: "MID", Day: "2026-03-12", At: timeutil.MustParse("2026-03-12T00:00:00Z"),
			LoadCopiesPerDay: 2e10, FlowLitresPerDay: 8e6, HasFlow: true, Observed: true},
		{SiteID: "LOWER", Day: "2026-03-12", At: timeutil.MustParse("2026-03-12T00:00:00Z"),
			LoadCopiesPerDay: 3e10, FlowLitresPerDay: 12e6, HasFlow: true, Observed: true},
		{SiteID: "BROOK"}, {SiteID: "WEIR"},
		{SiteID: "POOL", Day: "2026-03-12", At: timeutil.MustParse("2026-03-12T00:00:00Z"), Observed: true, Flagged: true},
	}
	rollUps := IndexRollUps(Propagate(cfg, graph, states))
	weir := rollUps["WEIR"]
	if math.Abs(weir.UpstreamLoad-3e10) > 1 {
		t.Fatalf("WEIR upstream load = %v, want 3e10", weir.UpstreamLoad)
	}
	if math.Abs(weir.TotalLoad-3e10) > 1 {
		t.Fatalf("WEIR total load = %v", weir.TotalLoad)
	}
	pool := rollUps["POOL"]
	if math.Abs(pool.TotalLoad-6e10) > 1 {
		t.Fatalf("POOL total load = %v, want 6e10", pool.TotalLoad)
	}
	if math.Abs(pool.TotalFlow-24e6) > 1 {
		t.Fatalf("POOL total flow = %v, want 24e6", pool.TotalFlow)
	}
	// Six billion copies a day carried by twenty four million litres a day.
	if math.Abs(pool.ImpliedConcentration-2500) > 1 {
		t.Fatalf("implied concentration = %v, want 2500", pool.ImpliedConcentration)
	}
	shares := 0.0
	for _, contribution := range pool.Contributions {
		shares += contribution.LoadShare
		if contribution.SiteID == "UPPER" {
			if math.Abs(contribution.LoadShare-1.0/6.0) > 1e-3 {
				t.Errorf("UPPER load share = %v, want a sixth", contribution.LoadShare)
			}
			if math.Abs(contribution.FlowShare-4.0/24.0) > 1e-3 {
				t.Errorf("UPPER flow share = %v", contribution.FlowShare)
			}
			if contribution.TravelHours != 30 {
				t.Errorf("UPPER travel hours = %v", contribution.TravelHours)
			}
		}
		if contribution.SiteID == "POOL" && !contribution.Self {
			t.Error("the node itself should be marked as self")
		}
	}
	if math.Abs(shares-1) > 1e-3 {
		t.Fatalf("load shares sum to %v, want 1", shares)
	}
}

func TestAttributionFindsTheMostUpstreamFlag(t *testing.T) {
	cfg := config.Default()
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	upperAt := timeutil.MustParse("2026-03-11T00:00:00Z")
	downAt := timeutil.MustParse("2026-03-12T00:00:00Z")
	states := []SiteState{
		{SiteID: "UPPER", Day: "2026-03-11", At: upperAt, Observed: true, Flagged: true,
			LoadCopiesPerDay: 1e10, FlowLitresPerDay: 4e6, HasFlow: true},
		{SiteID: "BROOK", Day: "2026-03-12", At: downAt, Observed: true, Flagged: true},
		{SiteID: "MID", Day: "2026-03-12", At: downAt, Observed: true},
		{SiteID: "LOWER", Day: "2026-03-12", At: downAt, Observed: true},
		{SiteID: "WEIR", Day: "2026-03-12", At: downAt, Observed: true, Flagged: true},
		{SiteID: "POOL", Day: "2026-03-12", At: downAt, Observed: true, Flagged: true},
	}
	rollUps := IndexRollUps(Propagate(cfg, graph, states))
	for _, siteID := range []string{"BROOK", "WEIR", "POOL"} {
		got := rollUps[siteID].MostUpstreamFlagged
		if len(got) != 1 || got[0] != "UPPER" {
			t.Errorf("%s attributed to %v, want UPPER", siteID, got)
		}
	}
	if got := rollUps["UPPER"].MostUpstreamFlagged; len(got) != 1 || got[0] != "UPPER" {
		t.Errorf("a flagged headwater should attribute to itself, got %v", got)
	}
	if rollUps["MID"].Attribution == "" {
		t.Error("an unflagged site should still carry an explanation")
	}
	if got := rollUps["MID"].MostUpstreamFlagged; len(got) != 0 {
		t.Errorf("an unflagged site attributed to %v", got)
	}
}

func TestAttributionRejectsImplausibleTravelTime(t *testing.T) {
	cfg := config.Default()
	cfg.Detection.UpstreamTravelToleranceHr = 1
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// UPPER is flagged twelve days before POOL, far longer than the thirty hour
	// travel time plus the tolerance allows, so it cannot explain the POOL flag.
	states := []SiteState{
		{SiteID: "UPPER", Day: "2026-02-28", At: timeutil.MustParse("2026-02-28T00:00:00Z"),
			Observed: true, Flagged: true},
		{SiteID: "MID"}, {SiteID: "LOWER"}, {SiteID: "BROOK"}, {SiteID: "WEIR"},
		{SiteID: "POOL", Day: "2026-03-12", At: timeutil.MustParse("2026-03-12T00:00:00Z"),
			Observed: true, Flagged: true},
	}
	rollUps := IndexRollUps(Propagate(cfg, graph, states))
	got := rollUps["POOL"].MostUpstreamFlagged
	if len(got) != 1 || got[0] != "POOL" {
		t.Fatalf("attribution = %v, want POOL itself", got)
	}
	if !strings.Contains(rollUps["POOL"].Attribution, "travel time") {
		t.Fatalf("explanation = %q", rollUps["POOL"].Attribution)
	}
}

func TestDescribeCoversEverySite(t *testing.T) {
	graph, err := Build(testNetwork())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lines := graph.Describe()
	if len(lines) != 6 {
		t.Fatalf("describe lines = %d", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "POOL") {
		t.Fatalf("the outlet should come last: %q", lines[len(lines)-1])
	}
	if graph.FlowLitresPerDay("UPPER") != 4e6 {
		t.Fatalf("flow = %v", graph.FlowLitresPerDay("UPPER"))
	}
	if graph.FlowLitresPerDay("GHOST") != 0 {
		t.Fatal("an unknown site reported a flow")
	}
	if got := strings.Join(graph.SiteIDs(), ","); got != "BROOK,LOWER,MID,POOL,UPPER,WEIR" {
		t.Fatalf("site ids = %s", got)
	}
}
