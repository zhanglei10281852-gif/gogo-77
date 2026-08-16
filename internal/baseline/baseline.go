// Package baseline builds the rolling reference a site is judged against.
//
// A baseline is a median and a robust dispersion taken over a trailing window
// of daily values. The median and the scaled median absolute deviation are used
// rather than a mean and standard deviation because the interesting event is a
// single very high day, and that day would otherwise inflate the very reference
// it is being compared with.
//
// Every baseline is computed as of a stated day, using only days strictly
// inside the window and, by default, excluding the day under test. That makes
// the baseline for a given day independent of that day's own value, which is
// what an exceedance test needs to mean anything.
package baseline

import (
	"fmt"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
)

// Baseline is a site's trailing reference as of one day.
type Baseline struct {
	SiteID       string          `json:"site_id"`
	Unit         normalize.Unit  `json:"signal_unit"`
	AsOf         timeutil.Stamp  `json:"as_of"`
	Window       timeutil.Window `json:"window"`
	Points       int             `json:"points"`
	Sufficient   bool            `json:"sufficient"`
	Median       float64         `json:"median"`
	Dispersion   float64         `json:"dispersion"`
	P90          float64         `json:"p90"`
	Minimum      float64         `json:"minimum"`
	Maximum      float64         `json:"maximum"`
	DetectedDays int             `json:"detected_days"`
	Reason       string          `json:"reason"`
}

// RobustZ places value on the baseline's robust scale. When the dispersion is
// zero the result is zero, because an unvarying history says nothing about how
// unusual a new value is; the multiple and absolute tests cover that case.
func (b Baseline) RobustZ(value float64) float64 {
	if !b.Sufficient || b.Dispersion <= 0 {
		return 0
	}
	return numeric.Round4((value - b.Median) / b.Dispersion)
}

// Ratio is value divided by the baseline median, or zero when there is no
// usable median to divide by.
func (b Baseline) Ratio(value float64) float64 {
	if !b.Sufficient || b.Median <= 0 {
		return 0
	}
	return numeric.Round4(value / b.Median)
}

// WindowFor returns the half-open day window a baseline as of asOf covers.
func WindowFor(cfg config.Config, asOf timeutil.Stamp) timeutil.Window {
	upper := asOf.StartOfDay()
	if !cfg.Baseline.ExcludeCurrentDay {
		upper = upper.AddDays(1)
	}
	return timeutil.Window{From: upper.AddDays(-cfg.Baseline.WindowDays), To: upper}
}

// ComputeAt builds the baseline for one site as of asOf from its daily series.
func ComputeAt(cfg config.Config, series normalize.DailySeries, asOf timeutil.Stamp) Baseline {
	window := WindowFor(cfg, asOf)
	result := Baseline{
		SiteID: series.SiteID,
		Unit:   series.Unit,
		AsOf:   asOf.StartOfDay(),
		Window: window,
	}
	values := make([]float64, 0, len(series.Days))
	for _, bin := range series.Days {
		if !window.Contains(bin.At) {
			continue
		}
		values = append(values, bin.Value)
		if bin.Detected {
			result.DetectedDays++
		}
	}
	result.Points = len(values)
	if result.Points < cfg.Baseline.MinPoints {
		result.Reason = fmt.Sprintf("%d day(s) in the %d day window is below the %d point minimum",
			result.Points, cfg.Baseline.WindowDays, cfg.Baseline.MinPoints)
		return result
	}
	digits := cfg.Quantification.ReportSignificantDigits
	median, err := numeric.Median(values)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	dispersion, err := numeric.RobustDispersion(values)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	if dispersion < cfg.Baseline.MinDispersion*median {
		dispersion = cfg.Baseline.MinDispersion * median
	}
	p90, err := numeric.Quantile(values, 0.9)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	minimum, _ := numeric.Min(values)
	maximum, _ := numeric.Max(values)
	result.Median = numeric.RoundSignificant(median, digits)
	result.Dispersion = numeric.RoundSignificant(dispersion, digits)
	result.P90 = numeric.RoundSignificant(p90, digits)
	result.Minimum = numeric.RoundSignificant(minimum, digits)
	result.Maximum = numeric.RoundSignificant(maximum, digits)
	result.Sufficient = true
	result.Reason = fmt.Sprintf("median of %d day(s) between %s and %s",
		result.Points, window.From.Day(), window.To.Day())
	return result
}

// Compute builds the baseline as of the newest day present in the series, which
// is what a report on the current state of a site wants.
func Compute(cfg config.Config, series normalize.DailySeries) Baseline {
	if len(series.Days) == 0 {
		return Baseline{
			SiteID: series.SiteID,
			Unit:   series.Unit,
			Reason: "site has no quality-passing days",
		}
	}
	newest := series.Days[len(series.Days)-1].At
	return ComputeAt(cfg, series, newest)
}

// ComputeAll builds one baseline per site, in ascending site order.
func ComputeAll(cfg config.Config, allSeries []normalize.DailySeries, asOf timeutil.Stamp) []Baseline {
	out := make([]Baseline, 0, len(allSeries))
	for _, series := range allSeries {
		if asOf.IsSet() {
			out = append(out, ComputeAt(cfg, series, asOf))
		} else {
			out = append(out, Compute(cfg, series))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SiteID < out[j].SiteID })
	return out
}

// Index maps site identifiers to baselines.
func Index(baselines []Baseline) map[string]Baseline {
	out := make(map[string]Baseline, len(baselines))
	for _, item := range baselines {
		out[item.SiteID] = item
	}
	return out
}

// Describe renders one baseline as a single text line.
func (b Baseline) Describe() string {
	if !b.Sufficient {
		return fmt.Sprintf("%-14s insufficient  %s", b.SiteID, b.Reason)
	}
	return fmt.Sprintf("%-14s median %-14.6g dispersion %-14.6g p90 %-14.6g %d day(s)",
		b.SiteID, b.Median, b.Dispersion, b.P90, b.Points)
}
