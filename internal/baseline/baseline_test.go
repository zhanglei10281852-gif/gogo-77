package baseline

import (
	"math"
	"strings"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
)

func series(values []float64) normalize.DailySeries {
	out := normalize.DailySeries{SiteID: "SITE-1", TargetGene: "gene", Unit: normalize.UnitCopiesPerLitre}
	base := timeutil.MustParse("2026-03-01T00:00:00Z")
	for index, value := range values {
		at := base.AddDays(index)
		out.Days = append(out.Days, normalize.DailyBin{
			Day: at.Day(), At: at, Value: value, Detected: value > 0, Quantified: value > 0,
		})
	}
	return out
}

func TestComputeAtUsesMedianAndRobustDispersion(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinDispersion = 0
	cfg.Baseline.MinPoints = 4
	// Values 96, 98, 100, 102, 104 have a median of 100 and deviations 4,2,0,2,4
	// whose median is 2, so the scaled dispersion is 2 * 1.4826.
	data := series([]float64{96, 98, 100, 102, 104, 999})
	asOf := data.Days[5].At
	result := ComputeAt(cfg, data, asOf)
	if !result.Sufficient {
		t.Fatalf("baseline refused: %s", result.Reason)
	}
	if result.Points != 5 {
		t.Fatalf("points = %d, want the five days before the reporting day", result.Points)
	}
	if result.Median != 100 {
		t.Fatalf("median = %v", result.Median)
	}
	if math.Abs(result.Dispersion-2*numeric.MADScale) > 1e-6 {
		t.Fatalf("dispersion = %v, want %v", result.Dispersion, 2*numeric.MADScale)
	}
	if result.Minimum != 96 || result.Maximum != 104 {
		t.Fatalf("range = %v..%v", result.Minimum, result.Maximum)
	}
	if math.Abs(result.P90-103.2) > 1e-6 {
		t.Fatalf("p90 = %v", result.P90)
	}
	if result.Unit != normalize.UnitCopiesPerLitre {
		t.Fatalf("unit = %s", result.Unit)
	}
}

func TestComputeAtCanIncludeTheCurrentDay(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinDispersion = 0
	cfg.Baseline.MinPoints = 3
	cfg.Baseline.ExcludeCurrentDay = false
	data := series([]float64{100, 100, 100, 400})
	result := ComputeAt(cfg, data, data.Days[3].At)
	if result.Points != 4 {
		t.Fatalf("points = %d, want the day under test included", result.Points)
	}
	if result.Maximum != 400 {
		t.Fatalf("maximum = %v", result.Maximum)
	}
}

func TestBaselineWindowLength(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.WindowDays = 5
	cfg.Baseline.MinPoints = 2
	cfg.Baseline.MinDispersion = 0
	// Ten days of data with a five day window keeps only the five days before the
	// reporting day.
	values := make([]float64, 10)
	for index := range values {
		values[index] = float64(index + 1)
	}
	data := series(values)
	result := ComputeAt(cfg, data, data.Days[9].At)
	if result.Points != 5 {
		t.Fatalf("points = %d, want 5", result.Points)
	}
	if result.Minimum != 5 || result.Maximum != 9 {
		t.Fatalf("window covered %v..%v, want 5..9", result.Minimum, result.Maximum)
	}
	window := WindowFor(cfg, data.Days[9].At)
	if window.Days() != 5 {
		t.Fatalf("window length = %d", window.Days())
	}
}

func TestInsufficientPointsAreExplained(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 5
	data := series([]float64{100, 110, 120})
	result := ComputeAt(cfg, data, data.Days[2].At)
	if result.Sufficient {
		t.Fatal("three days satisfied a five point minimum")
	}
	if !strings.Contains(result.Reason, "below the 5 point minimum") {
		t.Fatalf("reason = %q", result.Reason)
	}
	if result.RobustZ(9999) != 0 {
		t.Fatal("an unusable baseline produced a robust z")
	}
	if result.Ratio(9999) != 0 {
		t.Fatal("an unusable baseline produced a ratio")
	}
	if !strings.Contains(result.Describe(), "insufficient") {
		t.Fatalf("describe = %q", result.Describe())
	}
}

func TestMinimumDispersionFloorsAnUnvaryingHistory(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 3
	cfg.Baseline.MinDispersion = 0.1
	data := series([]float64{1000, 1000, 1000, 1000})
	result := ComputeAt(cfg, data, data.Days[3].At)
	if !result.Sufficient {
		t.Fatalf("baseline refused: %s", result.Reason)
	}
	if math.Abs(result.Dispersion-100) > 1e-6 {
		t.Fatalf("dispersion = %v, want the floor of ten percent of 1000", result.Dispersion)
	}
	if got := result.RobustZ(1500); math.Abs(got-5) > 1e-6 {
		t.Fatalf("robust z = %v, want 5", got)
	}
	if got := result.Ratio(1500); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("ratio = %v", got)
	}
}

func TestComputeUsesTheNewestDay(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 3
	cfg.Baseline.MinDispersion = 0
	data := series([]float64{10, 20, 30, 40})
	result := Compute(cfg, data)
	if result.AsOf.Day() != "2026-03-04" {
		t.Fatalf("as of = %s", result.AsOf.Day())
	}
	if result.Points != 3 {
		t.Fatalf("points = %d", result.Points)
	}
	empty := Compute(cfg, normalize.DailySeries{SiteID: "SITE-1"})
	if empty.Sufficient || !strings.Contains(empty.Reason, "no quality-passing days") {
		t.Fatalf("empty baseline = %+v", empty)
	}
}

func TestComputeAllSortsAndIndexes(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.MinPoints = 2
	cfg.Baseline.MinDispersion = 0
	first := series([]float64{10, 20, 30})
	first.SiteID = "Z-SITE"
	second := series([]float64{40, 50, 60})
	second.SiteID = "A-SITE"
	baselines := ComputeAll(cfg, []normalize.DailySeries{first, second}, timeutil.MustParse("2026-03-03T00:00:00Z"))
	if len(baselines) != 2 || baselines[0].SiteID != "A-SITE" {
		t.Fatalf("order = %+v", baselines)
	}
	index := Index(baselines)
	if index["Z-SITE"].SiteID != "Z-SITE" {
		t.Fatal("index lookup failed")
	}
	// Without an explicit instant each site is measured as of its own newest day.
	unanchored := ComputeAll(cfg, []normalize.DailySeries{first, second}, timeutil.Stamp{})
	if len(unanchored) != 2 {
		t.Fatalf("unanchored = %d", len(unanchored))
	}
	if !strings.Contains(baselines[0].Describe(), "A-SITE") {
		t.Fatalf("describe = %q", baselines[0].Describe())
	}
}

func TestBaselineIgnoresDaysOutsideTheWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Baseline.WindowDays = 3
	cfg.Baseline.MinPoints = 2
	cfg.Baseline.MinDispersion = 0
	data := series([]float64{9999, 100, 100, 100, 200})
	result := ComputeAt(cfg, data, data.Days[4].At)
	if result.Maximum != 100 {
		t.Fatalf("a day outside the window leaked in: maximum %v", result.Maximum)
	}
	if result.Points != 3 {
		t.Fatalf("points = %d", result.Points)
	}
}
