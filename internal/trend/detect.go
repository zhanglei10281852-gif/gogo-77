package trend

import (
	"fmt"
	"sort"

	"FluWatershed/internal/baseline"
	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
)

// Trigger names one reason a day was flagged.
type Trigger string

// The three independent exceedance triggers.
const (
	TriggerBaselineMultiple Trigger = "baseline_multiple"
	TriggerAbsolute         Trigger = "absolute_threshold"
	TriggerRobustZ          Trigger = "robust_z"
)

// Exceedance is the verdict for one day of one site.
type Exceedance struct {
	SiteID             string         `json:"site_id"`
	Day                string         `json:"day"`
	At                 timeutil.Stamp `json:"at"`
	Value              float64        `json:"value"`
	Unit               normalize.Unit `json:"signal_unit"`
	Detected           bool           `json:"detected"`
	BaselineMedian     float64        `json:"baseline_median"`
	BaselineDispersion float64        `json:"baseline_dispersion"`
	BaselinePoints     int            `json:"baseline_points"`
	BaselineUsable     bool           `json:"baseline_usable"`
	Ratio              float64        `json:"ratio_to_baseline"`
	RobustZ            float64        `json:"robust_z"`
	AbsoluteThreshold  float64        `json:"absolute_threshold"`
	Triggers           []Trigger      `json:"triggers"`
	Exceeded           bool           `json:"exceeded"`
	Explanation        string         `json:"explanation"`
}

// SiteTrend is the whole trend picture for one site.
type SiteTrend struct {
	SiteID               string            `json:"site_id"`
	Matrix               model.Matrix      `json:"matrix"`
	Unit                 normalize.Unit    `json:"signal_unit"`
	AsOf                 timeutil.Stamp    `json:"as_of"`
	Baseline             baseline.Baseline `json:"baseline"`
	Exceedances          []Exceedance      `json:"exceedances"`
	FlaggedDays          int               `json:"flagged_days"`
	ConsecutiveFlagged   int               `json:"consecutive_flagged_days"`
	SustainedRise        bool              `json:"sustained_rise"`
	Slope                Slope             `json:"slope"`
	LatestDay            string            `json:"latest_day"`
	LatestValue          float64           `json:"latest_value"`
	LatestExceeded       bool              `json:"latest_exceeded"`
	DetectionDays        int               `json:"detection_days"`
	ObservedDays         int               `json:"observed_days"`
	FirstDetectionDay    string            `json:"first_detection_day"`
	FoldChangeOverWindow float64           `json:"fold_change_over_window"`
	Summary              string            `json:"summary"`
}

// EvaluateSite runs every day of one site's series through the exceedance tests,
// then fits a slope over the trend window.
//
// Each day is compared against a baseline built only from days before it, so
// walking the series forward reproduces what the system would have said on that
// day. Three independent tests can flag a day, and any one of them is enough.
func EvaluateSite(cfg config.Config, site model.Site, series normalize.DailySeries,
	asOf timeutil.Stamp) SiteTrend {
	result := SiteTrend{
		SiteID:       series.SiteID,
		Matrix:       site.Matrix,
		Unit:         series.Unit,
		AsOf:         asOf.StartOfDay(),
		ObservedDays: len(series.Days),
	}
	threshold := cfg.AbsoluteThresholdFor(site)
	for _, bin := range series.Days {
		if asOf.IsSet() && bin.At.After(asOf.StartOfDay()) {
			continue
		}
		reference := baseline.ComputeAt(cfg, series, bin.At)
		exceedance := judge(cfg, site, bin, reference, threshold, series.Unit)
		result.Exceedances = append(result.Exceedances, exceedance)
		if bin.Detected {
			result.DetectionDays++
			if result.FirstDetectionDay == "" {
				result.FirstDetectionDay = bin.Day
			}
		}
		if exceedance.Exceeded {
			result.FlaggedDays++
		}
	}
	result.ConsecutiveFlagged = trailingRun(result.Exceedances)
	result.SustainedRise = result.ConsecutiveFlagged >= cfg.Detection.SustainedIntervals
	if len(result.Exceedances) > 0 {
		last := result.Exceedances[len(result.Exceedances)-1]
		result.LatestDay = last.Day
		result.LatestValue = last.Value
		result.LatestExceeded = last.Exceeded
		result.Baseline = baseline.ComputeAt(cfg, series, last.At)
	} else {
		result.Baseline = baseline.Baseline{
			SiteID: series.SiteID,
			Unit:   series.Unit,
			AsOf:   result.AsOf,
			Reason: "no observed days at or before the reporting instant",
		}
	}
	result.Slope = fitWindow(cfg, series, asOf)
	result.FoldChangeOverWindow = result.Slope.FoldChangeOverSpan(cfg.Detection.TrendWindowDays)
	result.Summary = summarise(cfg, result)
	return result
}

func judge(cfg config.Config, site model.Site, bin normalize.DailyBin,
	reference baseline.Baseline, threshold float64, unit normalize.Unit) Exceedance {
	exceedance := Exceedance{
		SiteID:             site.SiteID,
		Day:                bin.Day,
		At:                 bin.At,
		Value:              bin.Value,
		Unit:               unit,
		Detected:           bin.Detected,
		BaselineMedian:     reference.Median,
		BaselineDispersion: reference.Dispersion,
		BaselinePoints:     reference.Points,
		BaselineUsable:     reference.Sufficient,
		AbsoluteThreshold:  numeric.RoundSignificant(threshold, cfg.Quantification.ReportSignificantDigits),
	}
	if reference.Sufficient {
		exceedance.Ratio = reference.Ratio(bin.Value)
		exceedance.RobustZ = reference.RobustZ(bin.Value)
		if reference.Median > 0 && bin.Value > reference.Median*cfg.Detection.BaselineMultiple {
			exceedance.Triggers = append(exceedance.Triggers, TriggerBaselineMultiple)
		}
		if reference.Dispersion > 0 && exceedance.RobustZ > cfg.Detection.RobustZThreshold {
			exceedance.Triggers = append(exceedance.Triggers, TriggerRobustZ)
		}
	}
	if bin.Value > threshold {
		exceedance.Triggers = append(exceedance.Triggers, TriggerAbsolute)
	}
	sort.Slice(exceedance.Triggers, func(i, j int) bool {
		return exceedance.Triggers[i] < exceedance.Triggers[j]
	})
	exceedance.Exceeded = len(exceedance.Triggers) > 0
	exceedance.Explanation = explain(exceedance, reference, cfg)
	return exceedance
}

func explain(exceedance Exceedance, reference baseline.Baseline, cfg config.Config) string {
	if !exceedance.Exceeded {
		if !reference.Sufficient {
			return fmt.Sprintf("%.6g is below the %.6g absolute threshold; no usable baseline yet (%s)",
				exceedance.Value, exceedance.AbsoluteThreshold, reference.Reason)
		}
		return fmt.Sprintf("%.6g sits at %.2fx the %.6g baseline median, inside every threshold",
			exceedance.Value, exceedance.Ratio, reference.Median)
	}
	parts := make([]string, 0, len(exceedance.Triggers))
	for _, trigger := range exceedance.Triggers {
		switch trigger {
		case TriggerBaselineMultiple:
			parts = append(parts, fmt.Sprintf("%.2fx baseline median exceeds the %.2fx multiple",
				exceedance.Ratio, cfg.Detection.BaselineMultiple))
		case TriggerRobustZ:
			parts = append(parts, fmt.Sprintf("robust z %.2f exceeds %.2f",
				exceedance.RobustZ, cfg.Detection.RobustZThreshold))
		case TriggerAbsolute:
			parts = append(parts, fmt.Sprintf("%.6g exceeds the %.6g absolute threshold",
				exceedance.Value, exceedance.AbsoluteThreshold))
		}
	}
	return joinWithSemicolons(parts)
}

func joinWithSemicolons(parts []string) string {
	out := ""
	for index, part := range parts {
		if index > 0 {
			out += "; "
		}
		out += part
	}
	return out
}

// trailingRun counts how many of the most recent days in a row were flagged.
// Only a run that reaches the end of the series counts, because a run that
// stopped three days ago is not a current sustained rise.
func trailingRun(exceedances []Exceedance) int {
	run := 0
	for index := len(exceedances) - 1; index >= 0; index-- {
		if !exceedances[index].Exceeded {
			break
		}
		run++
	}
	return run
}

func fitWindow(cfg config.Config, series normalize.DailySeries, asOf timeutil.Stamp) Slope {
	upper := asOf
	if !upper.IsSet() && len(series.Days) > 0 {
		upper = series.Days[len(series.Days)-1].At
	}
	window := timeutil.Window{
		From: upper.StartOfDay().AddDays(-cfg.Detection.TrendWindowDays),
		To:   upper.StartOfDay().AddDays(1),
	}
	indices := make([]int, 0, len(series.Days))
	values := make([]float64, 0, len(series.Days))
	for _, bin := range series.Days {
		if !window.Contains(bin.At) {
			continue
		}
		indices = append(indices, bin.At.DayIndex())
		values = append(values, bin.Value)
	}
	return FitLog10(indices, values, cfg.Quantification.MinCopiesPerLitreFloor,
		cfg.Detection.MinTrendPoints, cfg.Detection.RisingSlopePerDay, cfg.Detection.FallingSlopePerDay)
}

func summarise(cfg config.Config, result SiteTrend) string {
	switch {
	case result.ObservedDays == 0:
		return "no quality-passing observations"
	case result.SustainedRise:
		return fmt.Sprintf("%d consecutive flagged day(s) meets the %d interval sustained-rise rule; slope %s",
			result.ConsecutiveFlagged, cfg.Detection.SustainedIntervals, result.Slope.Direction)
	case result.LatestExceeded:
		return fmt.Sprintf("latest day %s is flagged but the run of %d day(s) is short of the %d interval rule",
			result.LatestDay, result.ConsecutiveFlagged, cfg.Detection.SustainedIntervals)
	case result.FlaggedDays > 0:
		return fmt.Sprintf("%d flagged day(s) in history, none current; slope %s",
			result.FlaggedDays, result.Slope.Direction)
	default:
		return fmt.Sprintf("no flagged day in %d observed day(s); slope %s",
			result.ObservedDays, result.Slope.Direction)
	}
}

// EvaluateAll runs every site with a series, in ascending site order. Sites in
// the network with no usable series are reported with empty trends so that a
// silent site is visible rather than absent.
func EvaluateAll(cfg config.Config, network model.Network, allSeries []normalize.DailySeries,
	asOf timeutil.Stamp) []SiteTrend {
	bySite := make(map[string]normalize.DailySeries, len(allSeries))
	for _, series := range allSeries {
		bySite[series.SiteID] = series
	}
	out := make([]SiteTrend, 0, len(network.Sites))
	for _, id := range network.SiteIDs() {
		site, ok := network.SiteByID(id)
		if !ok {
			continue
		}
		if !site.Matrix.Quantitative() {
			continue
		}
		series, ok := bySite[id]
		if !ok {
			series = normalize.DailySeries{SiteID: id, Unit: normalize.UnitCopiesPerLitre}
		}
		out = append(out, EvaluateSite(cfg, site, series, asOf))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SiteID < out[j].SiteID })
	return out
}

// Index maps site identifiers to trends.
func Index(trends []SiteTrend) map[string]SiteTrend {
	out := make(map[string]SiteTrend, len(trends))
	for _, item := range trends {
		out[item.SiteID] = item
	}
	return out
}

// Flagged returns the site identifiers whose most recent day was flagged.
func Flagged(trends []SiteTrend) []string {
	out := make([]string, 0, len(trends))
	for _, item := range trends {
		if item.LatestExceeded {
			out = append(out, item.SiteID)
		}
	}
	sort.Strings(out)
	return out
}
