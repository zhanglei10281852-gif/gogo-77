// Package normalize converts a concentration into the quantity a site is
// actually tracked on.
//
// A raw copies-per-litre figure is not comparable across days: the same amount
// of shed material looks weaker after rain and stronger during low flow. Three
// corrections are applied, each of them optional and each recorded so that the
// reader can see which ones ran:
//
//   - dilution correction against a faecal-strength indicator, which rescales a
//     concentration towards the site's own typical strength;
//   - flow normalisation, which multiplies by litres per day to give a daily
//     load in copies per day;
//   - population normalisation, which divides that load by the served
//     population to give copies per person per day.
//
// Only a piped influent stream has a defensible daily flow, so a waterway grab
// or sediment sample stays on a concentration basis. Each site therefore keeps
// one internally consistent unit, which is what the baseline needs.
package normalize

import (
	"fmt"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/timeutil"
)

// Unit names the basis a site's tracked signal is expressed in.
type Unit string

// The two possible bases.
const (
	UnitCopiesPerDay   Unit = "copies_per_day"
	UnitCopiesPerLitre Unit = "copies_per_litre"
)

// FlowSource records where the daily flow figure came from.
type FlowSource string

// The possible provenances of a flow figure.
const (
	FlowObserved    FlowSource = "observed"
	FlowSiteMean    FlowSource = "site_mean"
	FlowUnavailable FlowSource = "unavailable"
)

// Point is one normalised observation, the unit of everything downstream.
type Point struct {
	ResultID    string         `json:"result_id"`
	SampleID    string         `json:"sample_id"`
	SiteID      string         `json:"site_id"`
	Matrix      model.Matrix   `json:"matrix"`
	TargetGene  string         `json:"target_gene"`
	Day         string         `json:"day"`
	CollectedAt timeutil.Stamp `json:"collected_at"`

	Status   quant.Status `json:"status"`
	Detected bool         `json:"detected"`
	Usable   bool         `json:"usable_for_trend"`
	Verdict  qc.Severity  `json:"qc_verdict"`

	CopiesPerLitre          float64 `json:"copies_per_litre"`
	IndicatorFactor         float64 `json:"indicator_factor"`
	CorrectedCopiesPerLitre float64 `json:"corrected_copies_per_litre"`
	LowerCopiesPerLitre     float64 `json:"lower_copies_per_litre"`
	UpperCopiesPerLitre     float64 `json:"upper_copies_per_litre"`

	FlowLitresPerDay float64    `json:"flow_litres_per_day"`
	FlowSource       FlowSource `json:"flow_source"`
	LoadCopiesPerDay float64    `json:"load_copies_per_day"`
	ServedPopulation int        `json:"served_population"`
	LoadPerPersonDay float64    `json:"load_copies_per_person_per_day"`

	SignalValue float64 `json:"signal_value"`
	SignalUnit  Unit    `json:"signal_unit"`
	SignalLower float64 `json:"signal_lower"`
	SignalUpper float64 `json:"signal_upper"`

	Notes []string `json:"notes"`
}

// IndicatorFactor is the dilution correction implied by a faecal-strength
// indicator: the site's reference strength divided by what this sample showed,
// clamped to the configured range so that one odd indicator reading cannot
// swamp the signal it is meant to steady.
func IndicatorFactor(cfg config.Config, site model.Site, sample model.Sample) (float64, bool, string) {
	norm := cfg.Normalisation
	if !norm.EnableIndicatorCorrection {
		return 1, false, "indicator correction disabled by configuration"
	}
	if site.IndicatorRefCopiesPerLitre <= 0 {
		return 1, false, "site declares no indicator reference strength"
	}
	if sample.IndicatorCopiesPerLitre <= 0 {
		return 1, false, "sample carries no indicator measurement"
	}
	raw := site.IndicatorRefCopiesPerLitre / sample.IndicatorCopiesPerLitre
	clamped := numeric.Clamp(raw, norm.IndicatorCorrectionMin, norm.IndicatorCorrectionMax)
	if clamped != raw {
		return numeric.Round4(clamped), true, fmt.Sprintf(
			"indicator factor %.4f clamped to %.4f by the configured %.2f..%.2f range",
			raw, clamped, norm.IndicatorCorrectionMin, norm.IndicatorCorrectionMax)
	}
	return numeric.Round4(clamped), true, fmt.Sprintf("indicator factor %.4f applied", clamped)
}

// FlowLitresPerDay chooses the flow for a sample, preferring the flow observed
// on the day of collection over the site's long-run mean.
func FlowLitresPerDay(site model.Site, sample model.Sample) (float64, FlowSource) {
	if sample.ObservedFlowM3 > 0 {
		return sample.ObservedFlowM3 * 1000, FlowObserved
	}
	if site.MeanDailyFlowM3 > 0 {
		return site.FlowLitresPerDay(), FlowSiteMean
	}
	return 0, FlowUnavailable
}

// Apply normalises one measurement against its site and sample.
func Apply(cfg config.Config, site model.Site, sample model.Sample,
	measurement quant.Measurement, assessment qc.Assessment) Point {
	digits := cfg.Quantification.ReportSignificantDigits
	point := Point{
		ResultID:       measurement.ResultID,
		SampleID:       measurement.SampleID,
		SiteID:         measurement.SiteID,
		Matrix:         measurement.Matrix,
		TargetGene:     measurement.TargetGene,
		Day:            measurement.Day,
		CollectedAt:    measurement.CollectedAt,
		Status:         measurement.Status,
		Detected:       measurement.Detected,
		Usable:         assessment.UsableForTrend,
		Verdict:        assessment.Verdict,
		CopiesPerLitre: measurement.CopiesPerLitre,
		SignalUnit:     UnitCopiesPerLitre,
	}
	factor, applied, note := IndicatorFactor(cfg, site, sample)
	point.IndicatorFactor = factor
	if applied {
		point.Notes = append(point.Notes, note)
	}
	point.CorrectedCopiesPerLitre = numeric.RoundSignificant(measurement.CopiesPerLitre*factor, digits)
	point.LowerCopiesPerLitre = numeric.RoundSignificant(measurement.LowerCopiesPerLitre*factor, digits)
	point.UpperCopiesPerLitre = numeric.RoundSignificant(measurement.UpperCopiesPerLitre*factor, digits)
	point.SignalValue = point.CorrectedCopiesPerLitre
	point.SignalLower = point.LowerCopiesPerLitre
	point.SignalUpper = point.UpperCopiesPerLitre

	flow, source := FlowLitresPerDay(site, sample)
	point.FlowLitresPerDay = numeric.RoundSignificant(flow, digits)
	point.FlowSource = source
	point.ServedPopulation = site.ServedPopulation

	if cfg.Normalisation.EnableFlowNormalisation && measurement.Matrix.FlowNormalisable() {
		if source == FlowUnavailable {
			point.Notes = append(point.Notes, "flow normalisation requested but no flow figure is available")
		} else {
			point.LoadCopiesPerDay = numeric.RoundSignificant(point.CorrectedCopiesPerLitre*flow, digits)
			point.SignalValue = point.LoadCopiesPerDay
			point.SignalUnit = UnitCopiesPerDay
			point.SignalLower = numeric.RoundSignificant(point.LowerCopiesPerLitre*flow, digits)
			point.SignalUpper = numeric.RoundSignificant(point.UpperCopiesPerLitre*flow, digits)
			point.Notes = append(point.Notes, fmt.Sprintf("daily load derived from %s flow of %.6g L/day", source, flow))
		}
	}
	if cfg.Normalisation.EnablePopulationNormalisation && point.LoadCopiesPerDay > 0 {
		if site.ServedPopulation > 0 {
			perPerson := point.LoadCopiesPerDay / float64(site.ServedPopulation) * cfg.Normalisation.PerCapitaScale
			point.LoadPerPersonDay = numeric.RoundSignificant(perPerson, digits)
		} else {
			point.Notes = append(point.Notes, "population normalisation skipped because served_population is zero")
		}
	}
	sort.Strings(point.Notes)
	return point
}

// ApplyAll normalises every measurement in a bundle.
func ApplyAll(cfg config.Config, bundle model.Bundle, measurements []quant.Measurement,
	assessments map[string]qc.Assessment) ([]Point, error) {
	samples := bundle.SampleByID()
	sites := make(map[string]model.Site, len(bundle.Network.Sites))
	for _, site := range bundle.Network.Sites {
		sites[site.SiteID] = site
	}
	points := make([]Point, 0, len(measurements))
	for _, measurement := range measurements {
		sample, ok := samples[measurement.SampleID]
		if !ok {
			return nil, fmt.Errorf("measurement %s references missing sample %s", measurement.ResultID, measurement.SampleID)
		}
		site, ok := sites[sample.SiteID]
		if !ok {
			return nil, fmt.Errorf("sample %s references missing site %s", sample.SampleID, sample.SiteID)
		}
		assessment, ok := assessments[measurement.ResultID]
		if !ok {
			return nil, fmt.Errorf("measurement %s has no quality assessment", measurement.ResultID)
		}
		points = append(points, Apply(cfg, site, sample, measurement, assessment))
	}
	SortPoints(points)
	return points, nil
}

// SortPoints puts points into canonical order.
func SortPoints(points []Point) {
	sort.SliceStable(points, func(i, j int) bool {
		left, right := points[i], points[j]
		if left.SiteID != right.SiteID {
			return left.SiteID < right.SiteID
		}
		if !left.CollectedAt.Equal(right.CollectedAt) {
			return left.CollectedAt.Before(right.CollectedAt)
		}
		if left.TargetGene != right.TargetGene {
			return left.TargetGene < right.TargetGene
		}
		return left.ResultID < right.ResultID
	})
}

// UsableBySite groups the quality-passing points by site, preserving order.
func UsableBySite(points []Point) map[string][]Point {
	grouped := make(map[string][]Point)
	for _, point := range points {
		if !point.Usable {
			continue
		}
		grouped[point.SiteID] = append(grouped[point.SiteID], point)
	}
	return grouped
}

// BySite groups every point by site, usable or not.
func BySite(points []Point) map[string][]Point {
	grouped := make(map[string][]Point)
	for _, point := range points {
		grouped[point.SiteID] = append(grouped[point.SiteID], point)
	}
	return grouped
}

// DailySeries collapses a site's points to one value per calendar day. When a
// day holds several results, the mean of their signal values is used and every
// contributing result is listed, which keeps a day with a repeat assay from
// counting twice in a baseline.
type DailySeries struct {
	SiteID     string     `json:"site_id"`
	TargetGene string     `json:"target_gene"`
	Unit       Unit       `json:"signal_unit"`
	Days       []DailyBin `json:"days"`
}

// DailyBin is one day of one site's series.
type DailyBin struct {
	Day         string         `json:"day"`
	At          timeutil.Stamp `json:"at"`
	Value       float64        `json:"value"`
	Detected    bool           `json:"detected"`
	Quantified  bool           `json:"quantified"`
	ResultIDs   []string       `json:"result_ids"`
	Contributed int            `json:"contributed_results"`
}

// selectTargets narrows a site's points to the primary target gene.
//
// When several targets are run on the same extract they answer different
// questions, and averaging them would blend quantities that do not move
// together. The primary target therefore carries the trend on its own. If the
// site never ran that target, every point is kept rather than dropping the site
// silently, and the returned flag says which happened.
func selectTargets(cfg config.Config, points []Point) ([]Point, bool) {
	primary := cfg.Quantification.PrimaryTargetGene
	if primary == "" {
		return points, false
	}
	kept := make([]Point, 0, len(points))
	for _, point := range points {
		if point.TargetGene == primary {
			kept = append(kept, point)
		}
	}
	if len(kept) == 0 {
		return points, false
	}
	return kept, true
}

// BuildDailySeries collapses usable points for one site into a daily series.
// Points must already be in canonical order.
func BuildDailySeries(cfg config.Config, siteID string, points []Point) DailySeries {
	series := DailySeries{SiteID: siteID, Unit: UnitCopiesPerLitre}
	selected, narrowed := selectTargets(cfg, points)
	series.TargetGene = cfg.Quantification.PrimaryTargetGene
	if !narrowed {
		series.TargetGene = "all targets"
	}
	buckets := make(map[string][]Point)
	order := make([]string, 0, len(selected))
	for _, point := range selected {
		if !point.Usable {
			continue
		}
		if _, seen := buckets[point.Day]; !seen {
			order = append(order, point.Day)
		}
		buckets[point.Day] = append(buckets[point.Day], point)
		series.Unit = point.SignalUnit
	}
	sort.Strings(order)
	for _, day := range order {
		bucket := buckets[day]
		values := make([]float64, 0, len(bucket))
		ids := make([]string, 0, len(bucket))
		bin := DailyBin{Day: day, At: bucket[0].CollectedAt.StartOfDay(), Contributed: len(bucket)}
		for _, point := range bucket {
			values = append(values, point.SignalValue)
			ids = append(ids, point.ResultID)
			if point.Detected {
				bin.Detected = true
			}
			if point.Status == quant.StatusQuantified {
				bin.Quantified = true
			}
		}
		mean, err := numeric.Mean(values)
		if err == nil {
			bin.Value = numeric.RoundSignificant(mean, cfg.Quantification.ReportSignificantDigits)
		}
		sort.Strings(ids)
		bin.ResultIDs = ids
		series.Days = append(series.Days, bin)
	}
	return series
}

// BuildAllSeries builds a daily series for every site that has usable points,
// returned in ascending site order.
func BuildAllSeries(cfg config.Config, points []Point) []DailySeries {
	grouped := UsableBySite(points)
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]DailySeries, 0, len(ids))
	for _, id := range ids {
		out = append(out, BuildDailySeries(cfg, id, grouped[id]))
	}
	return out
}
