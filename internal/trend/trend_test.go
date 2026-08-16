package trend

import (
	"math"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/timeutil"
)

func TestTheilSenOnAPerfectLine(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 4}
	ys := []float64{1, 3, 5, 7, 9}
	slope, intercept, pairs, ok := TheilSen(xs, ys)
	if !ok {
		t.Fatal("TheilSen refused a clean line")
	}
	if math.Abs(slope-2) > 1e-12 {
		t.Fatalf("slope = %v, want 2", slope)
	}
	if math.Abs(intercept-1) > 1e-12 {
		t.Fatalf("intercept = %v, want 1", intercept)
	}
	if pairs != 10 {
		t.Fatalf("pairs = %d, want 10 for five points", pairs)
	}
}

func TestTheilSenIgnoresASingleOutlier(t *testing.T) {
	// The clean relation is y = 2x. Replacing the third point with 40 leaves the
	// median pairwise slope untouched, which is the whole reason for using it.
	xs := []float64{0, 1, 2, 3}
	clean := []float64{0, 2, 4, 6}
	dirty := []float64{0, 2, 40, 6}
	cleanSlope, cleanIntercept, _, ok := TheilSen(xs, clean)
	if !ok {
		t.Fatal("TheilSen refused the clean series")
	}
	dirtySlope, dirtyIntercept, _, ok := TheilSen(xs, dirty)
	if !ok {
		t.Fatal("TheilSen refused the contaminated series")
	}
	if math.Abs(cleanSlope-2) > 1e-12 || math.Abs(dirtySlope-2) > 1e-12 {
		t.Fatalf("slopes = %v and %v, both should be 2", cleanSlope, dirtySlope)
	}
	if math.Abs(cleanIntercept-dirtyIntercept) > 1e-12 {
		t.Fatalf("intercepts = %v and %v", cleanIntercept, dirtyIntercept)
	}
}

func TestTheilSenIsIndependentOfPointOrder(t *testing.T) {
	xs := []float64{0, 3, 1, 4, 2}
	ys := []float64{1.0, 2.8, 1.4, 3.9, 2.1}
	forwardSlope, forwardIntercept, _, ok := TheilSen(xs, ys)
	if !ok {
		t.Fatal("TheilSen refused the series")
	}
	reversedX := []float64{2, 4, 1, 3, 0}
	reversedY := []float64{2.1, 3.9, 1.4, 2.8, 1.0}
	reverseSlope, reverseIntercept, _, ok := TheilSen(reversedX, reversedY)
	if !ok {
		t.Fatal("TheilSen refused the reordered series")
	}
	if forwardSlope != reverseSlope || forwardIntercept != reverseIntercept {
		t.Fatalf("order changed the fit: %v/%v against %v/%v",
			forwardSlope, forwardIntercept, reverseSlope, reverseIntercept)
	}
}

func TestTheilSenRefusesDegenerateInput(t *testing.T) {
	if _, _, _, ok := TheilSen([]float64{1}, []float64{1}); ok {
		t.Error("a single point produced a slope")
	}
	if _, _, _, ok := TheilSen([]float64{1, 2}, []float64{1}); ok {
		t.Error("mismatched lengths produced a slope")
	}
	if _, _, _, ok := TheilSen([]float64{3, 3, 3}, []float64{1, 2, 3}); ok {
		t.Error("a vertical series produced a slope")
	}
}

func TestFitLog10ReadsDecadesPerDay(t *testing.T) {
	// Ten, one hundred, one thousand, ten thousand on consecutive days is exactly
	// one decade per day.
	fit := FitLog10([]int{100, 101, 102, 103}, []float64{10, 100, 1000, 10000}, 1, 3, 0.02, -0.02)
	if !fit.Sufficient {
		t.Fatalf("fit refused: %s", fit.Reason)
	}
	if math.Abs(fit.Slope-1) > 1e-9 {
		t.Fatalf("slope = %v, want 1", fit.Slope)
	}
	if fit.Direction != DirectionRising {
		t.Fatalf("direction = %s", fit.Direction)
	}
	if fit.SpanDays != 3 {
		t.Fatalf("span = %d", fit.SpanDays)
	}
	// A decade per day doubles in log10(2) days.
	if math.Abs(fit.DoublingDays-0.3) > 0.01 {
		t.Fatalf("doubling days = %v", fit.DoublingDays)
	}
	if got := fit.FoldChangeOverSpan(2); math.Abs(got-100) > 1e-6 {
		t.Fatalf("two day fold change = %v, want 100", got)
	}
}

func TestFitLog10Directions(t *testing.T) {
	falling := FitLog10([]int{0, 1, 2, 3}, []float64{10000, 1000, 100, 10}, 1, 3, 0.02, -0.02)
	if falling.Direction != DirectionFalling {
		t.Fatalf("direction = %s", falling.Direction)
	}
	if falling.DoublingDays != 0 {
		t.Fatalf("a falling series should report no doubling time, got %v", falling.DoublingDays)
	}
	flat := FitLog10([]int{0, 1, 2, 3}, []float64{1000, 1010, 995, 1005}, 1, 3, 0.02, -0.02)
	if flat.Direction != DirectionFlat {
		t.Fatalf("direction = %s", flat.Direction)
	}
	short := FitLog10([]int{0, 1}, []float64{10, 100}, 1, 3, 0.02, -0.02)
	if short.Sufficient {
		t.Fatal("two points satisfied a three point minimum")
	}
	if short.Direction != DirectionInsufficient {
		t.Fatalf("direction = %s", short.Direction)
	}
}

func TestFitLog10AppliesTheFloorToZeroValues(t *testing.T) {
	// A non-detecting day is substituted with zero, which has no logarithm. The
	// floor keeps it in the regression instead of dropping the day.
	fit := FitLog10([]int{0, 1, 2, 3}, []float64{0, 0, 100, 1000}, 10, 3, 0.02, -0.02)
	if !fit.Sufficient {
		t.Fatalf("fit refused: %s", fit.Reason)
	}
	if fit.Direction != DirectionRising {
		t.Fatalf("direction = %s", fit.Direction)
	}
	if fit.Points != 4 {
		t.Fatalf("points = %d, want all four days", fit.Points)
	}
}

func buildSeries(values []float64, startDay int) normalize.DailySeries {
	series := normalize.DailySeries{SiteID: "SITE-1", TargetGene: "gene", Unit: normalize.UnitCopiesPerLitre}
	base := timeutil.MustParse("2026-03-01T00:00:00Z")
	for index, value := range values {
		at := base.AddDays(startDay + index)
		series.Days = append(series.Days, normalize.DailyBin{
			Day:         at.Day(),
			At:          at,
			Value:       value,
			Detected:    value > 0,
			Quantified:  value > 0,
			ResultIDs:   []string{"RES"},
			Contributed: 1,
		})
	}
	return series
}

func testSite() model.Site {
	return model.Site{
		SiteID:   "SITE-1",
		Label:    "test site",
		Matrix:   model.MatrixWaterwayGrab,
		Latitude: 45,
	}
}

func TestEvaluateSiteFlagsOnBaselineMultiple(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 4
	cfg.Detection.DefaultAbsoluteConcentration = 1e12 // effectively disable the absolute test
	cfg.Baseline.MinDispersion = 0
	cfg.Detection.RobustZThreshold = 1e9 // and the robust test, isolating the multiple
	values := []float64{100, 110, 90, 105, 95, 5000}
	series := buildSeries(values, 0)
	asOf := series.Days[len(series.Days)-1].At
	result := EvaluateSite(cfg, testSite(), series, asOf)
	if len(result.Exceedances) != 6 {
		t.Fatalf("exceedances = %d, want one per day", len(result.Exceedances))
	}
	last := result.Exceedances[5]
	if !last.Exceeded {
		t.Fatalf("the final day was not flagged: %s", last.Explanation)
	}
	if len(last.Triggers) != 1 || last.Triggers[0] != TriggerBaselineMultiple {
		t.Fatalf("triggers = %v", last.Triggers)
	}
	if result.FlaggedDays != 1 {
		t.Fatalf("flagged days = %d", result.FlaggedDays)
	}
	if result.ConsecutiveFlagged != 1 {
		t.Fatalf("run = %d", result.ConsecutiveFlagged)
	}
	if result.SustainedRise {
		t.Fatal("a single flagged day met the sustained rule")
	}
	if !result.LatestExceeded {
		t.Fatal("the latest day should be reported as flagged")
	}
}

func TestEvaluateSiteFlagsOnAbsoluteThreshold(t *testing.T) {
	cfg := config.Default()
	cfg.Detection.DefaultAbsoluteConcentration = 1000
	cfg.Detection.RobustZThreshold = 1e9
	cfg.Detection.BaselineMultiple = 1e9
	series := buildSeries([]float64{900, 950, 920, 940, 1500}, 0)
	asOf := series.Days[len(series.Days)-1].At
	result := EvaluateSite(cfg, testSite(), series, asOf)
	last := result.Exceedances[len(result.Exceedances)-1]
	if len(last.Triggers) != 1 || last.Triggers[0] != TriggerAbsolute {
		t.Fatalf("triggers = %v", last.Triggers)
	}
}

func TestSiteThresholdOverridesTheDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Detection.DefaultAbsoluteConcentration = 1e12
	cfg.Detection.RobustZThreshold = 1e9
	cfg.Detection.BaselineMultiple = 1e9
	site := testSite()
	site.AbsoluteThreshold = 1000
	series := buildSeries([]float64{900, 950, 920, 940, 1500}, 0)
	asOf := series.Days[len(series.Days)-1].At
	result := EvaluateSite(cfg, site, series, asOf)
	last := result.Exceedances[len(result.Exceedances)-1]
	if !last.Exceeded {
		t.Fatalf("the site specific threshold was ignored: %s", last.Explanation)
	}
	if last.AbsoluteThreshold != 1000 {
		t.Fatalf("threshold = %v", last.AbsoluteThreshold)
	}
}

func TestSustainedRiseNeedsAnUnbrokenRunToTheEnd(t *testing.T) {
	cfg := config.Default()
	cfg.Detection.SustainedIntervals = 3
	cfg.Detection.DefaultAbsoluteConcentration = 1000
	cfg.Detection.RobustZThreshold = 1e9
	cfg.Detection.BaselineMultiple = 1e9
	// Three flagged days that end two days before the series does.
	broken := buildSeries([]float64{100, 100, 100, 100, 2000, 2000, 2000, 100, 100}, 0)
	asOf := broken.Days[len(broken.Days)-1].At
	result := EvaluateSite(cfg, testSite(), broken, asOf)
	if result.FlaggedDays != 3 {
		t.Fatalf("flagged days = %d", result.FlaggedDays)
	}
	if result.ConsecutiveFlagged != 0 {
		t.Fatalf("trailing run = %d, want 0 because the run has ended", result.ConsecutiveFlagged)
	}
	if result.SustainedRise {
		t.Fatal("a run that already ended counted as a current sustained rise")
	}
	current := buildSeries([]float64{100, 100, 100, 100, 2000, 2000, 2000}, 0)
	asOf = current.Days[len(current.Days)-1].At
	result = EvaluateSite(cfg, testSite(), current, asOf)
	if result.ConsecutiveFlagged != 3 {
		t.Fatalf("trailing run = %d, want 3", result.ConsecutiveFlagged)
	}
	if !result.SustainedRise {
		t.Fatal("three consecutive flagged days did not meet a three interval rule")
	}
}

func TestBaselineExcludesTheDayUnderTest(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 3
	cfg.Baseline.MinDispersion = 0
	cfg.Detection.DefaultAbsoluteConcentration = 1e12
	cfg.Detection.RobustZThreshold = 1e9
	series := buildSeries([]float64{100, 100, 100, 100000}, 0)
	asOf := series.Days[3].At
	result := EvaluateSite(cfg, testSite(), series, asOf)
	last := result.Exceedances[3]
	if last.BaselineMedian != 100 {
		t.Fatalf("baseline median = %v; the day under test leaked into its own baseline", last.BaselineMedian)
	}
	if last.Ratio != 1000 {
		t.Fatalf("ratio = %v, want 1000", last.Ratio)
	}
}

func TestEvaluateSiteWithNoObservations(t *testing.T) {
	cfg := config.Default()
	series := normalize.DailySeries{SiteID: "SITE-1", Unit: normalize.UnitCopiesPerLitre}
	result := EvaluateSite(cfg, testSite(), series, timeutil.MustParse("2026-03-10T00:00:00Z"))
	if result.ObservedDays != 0 || len(result.Exceedances) != 0 {
		t.Fatalf("unexpected content: %+v", result)
	}
	if result.Baseline.Sufficient {
		t.Fatal("a baseline was reported without any observations")
	}
	if result.Summary == "" {
		t.Fatal("an empty site should still be summarised")
	}
}

func TestEvaluateAllSkipsNonQuantitativeSitesAndSorts(t *testing.T) {
	cfg := config.Default()
	network := model.Network{
		SchemaVersion: model.SchemaVersion,
		CatchmentID:   "test",
		Sites: []model.Site{
			{SiteID: "B-SITE", Label: "b", Matrix: model.MatrixWaterwayGrab},
			{SiteID: "A-SITE", Label: "a", Matrix: model.MatrixSediment},
			{SiteID: "C-EVENT", Label: "c", Matrix: model.MatrixCarcassEvent},
		},
	}
	trends := EvaluateAll(cfg, network, nil, timeutil.MustParse("2026-03-10T00:00:00Z"))
	if len(trends) != 2 {
		t.Fatalf("trends = %d, want the two quantitative sites", len(trends))
	}
	if trends[0].SiteID != "A-SITE" || trends[1].SiteID != "B-SITE" {
		t.Fatalf("order = %s, %s", trends[0].SiteID, trends[1].SiteID)
	}
	if got := Flagged(trends); len(got) != 0 {
		t.Fatalf("flagged = %v", got)
	}
	if index := Index(trends); index["A-SITE"].SiteID != "A-SITE" {
		t.Fatal("index lookup failed")
	}
}

func TestExceedanceUsesRobustZ(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 4
	cfg.Baseline.MinDispersion = 0
	cfg.Detection.DefaultAbsoluteConcentration = 1e12
	cfg.Detection.BaselineMultiple = 1e9
	cfg.Detection.RobustZThreshold = 3
	// Deviations from the median of 100 are 4,2,0,2,4 so the scaled dispersion is
	// 2*1.4826 = 2.9652. A value of 120 sits about 6.7 dispersions above.
	series := buildSeries([]float64{96, 98, 100, 102, 104, 120}, 0)
	asOf := series.Days[len(series.Days)-1].At
	result := EvaluateSite(cfg, testSite(), series, asOf)
	last := result.Exceedances[len(result.Exceedances)-1]
	if len(last.Triggers) != 1 || last.Triggers[0] != TriggerRobustZ {
		t.Fatalf("triggers = %v (z = %v)", last.Triggers, last.RobustZ)
	}
	if last.RobustZ < 6 || last.RobustZ > 8 {
		t.Fatalf("robust z = %v, want about 6.7", last.RobustZ)
	}
}
