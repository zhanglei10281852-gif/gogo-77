// Package pipeline wires the analysis stages together.
//
// The order is fixed and each stage consumes only what the stages before it
// produced: quantify, then quality control, then normalisation, then daily
// series, then baselines, then exceedance and trend, then catchment roll-up,
// then corroboration. Running the whole chain from the same inputs and the same
// configuration always produces the same document, because the reporting instant
// is derived from the data rather than read from a clock.
package pipeline

import (
	"fmt"
	"sort"

	"FluWatershed/internal/baseline"
	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/corroborate"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/strictjson"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

// SnapshotSchemaVersion tags a stored analysis document.
const SnapshotSchemaVersion = "fluwatershed-signals/v1"

// Counts is the size of the input set an analysis ran over.
type Counts struct {
	Sites        int `json:"sites"`
	Links        int `json:"links"`
	Samples      int `json:"samples"`
	Results      int `json:"results"`
	Events       int `json:"events"`
	Measurements int `json:"measurements"`
	UsablePoints int `json:"usable_points"`
	SeriesDays   int `json:"series_days"`
}

// Analysis is the whole computed picture, and the document the store persists as
// its signal snapshot.
type Analysis struct {
	SchemaVersion     string                  `json:"schema_version"`
	CatchmentID       string                  `json:"catchment_id"`
	AsOf              timeutil.Stamp          `json:"as_of"`
	ConfigFingerprint string                  `json:"config_fingerprint"`
	Counts            Counts                  `json:"counts"`
	QualityTally      qc.Tally                `json:"quality_tally"`
	Measurements      []quant.Measurement     `json:"measurements"`
	Assessments       []qc.Assessment         `json:"assessments"`
	Points            []normalize.Point       `json:"points"`
	Series            []normalize.DailySeries `json:"series"`
	Baselines         []baseline.Baseline     `json:"baselines"`
	Trends            []trend.SiteTrend       `json:"trends"`
	States            []catchment.SiteState   `json:"site_states"`
	RollUps           []catchment.RollUp      `json:"roll_ups"`
	Signals           []corroborate.Signal    `json:"signals"`
	Topology          []string                `json:"topological_order"`
	Headwaters        []string                `json:"headwaters"`
	Outlets           []string                `json:"outlets"`
	Skipped           []string                `json:"skipped"`
}

// ResolveAsOf picks the reporting instant.
//
// An explicit override wins. Otherwise the newest instant anywhere in the input
// is used: the latest analysis time, collection time or carcass observation.
// Deriving it from the data is what keeps a rerun identical, and it also means a
// historical data set is analysed as of its own end rather than as of today.
func ResolveAsOf(bundle model.Bundle, override timeutil.Stamp) (timeutil.Stamp, error) {
	if override.IsSet() {
		return override, nil
	}
	stamps := make([]timeutil.Stamp, 0, len(bundle.Samples)+len(bundle.Results)+len(bundle.Events))
	for _, sample := range bundle.Samples {
		stamps = append(stamps, sample.CollectedAt)
	}
	for _, result := range bundle.Results {
		stamps = append(stamps, result.AnalysedAt)
	}
	for _, event := range bundle.Events {
		stamps = append(stamps, event.ObservedAt)
	}
	latest, ok := timeutil.Latest(stamps)
	if !ok {
		return timeutil.Stamp{}, fmt.Errorf("input carries no timestamp to report as of; pass an explicit instant")
	}
	return latest, nil
}

// Run executes every stage against a validated bundle.
func Run(cfg config.Config, bundle model.Bundle, asOf timeutil.Stamp) (Analysis, error) {
	bundle.SortAll()
	graph, err := catchment.Build(bundle.Network)
	if err != nil {
		return Analysis{}, err
	}
	measurements, skipped, err := quant.QuantifyBundle(cfg, bundle)
	if err != nil {
		return Analysis{}, err
	}
	assessments, assessmentIndex := qc.AssessAll(cfg, bundle, measurements)
	points, err := normalize.ApplyAll(cfg, bundle, measurements, assessmentIndex)
	if err != nil {
		return Analysis{}, err
	}
	series := normalize.BuildAllSeries(cfg, points)
	baselines := baseline.ComputeAll(cfg, series, asOf)
	trends := trend.EvaluateAll(cfg, bundle.Network, series, asOf)
	states := catchment.BuildStates(cfg, graph, points, trends)
	rollUps := catchment.Propagate(cfg, graph, states)
	signals := corroborate.EvaluateAll(cfg, graph, trends, bundle.Events, asOf)

	analysis := Analysis{
		SchemaVersion:     SnapshotSchemaVersion,
		CatchmentID:       bundle.Network.CatchmentID,
		AsOf:              asOf,
		ConfigFingerprint: cfg.Fingerprint(),
		QualityTally:      qc.Count(assessments),
		Measurements:      measurements,
		Assessments:       assessments,
		Points:            points,
		Series:            series,
		Baselines:         baselines,
		Trends:            trends,
		States:            states,
		RollUps:           rollUps,
		Signals:           signals,
		Topology:          graph.Order(),
		Headwaters:        graph.Headwaters(),
		Outlets:           graph.Outlets(),
		Skipped:           skipped,
	}
	analysis.Counts = Counts{
		Sites:        len(bundle.Network.Sites),
		Links:        len(bundle.Network.Links),
		Samples:      len(bundle.Samples),
		Results:      len(bundle.Results),
		Events:       len(bundle.Events),
		Measurements: len(measurements),
	}
	for _, point := range points {
		if point.Usable {
			analysis.Counts.UsablePoints++
		}
	}
	for _, item := range series {
		analysis.Counts.SeriesDays += len(item.Days)
	}
	sort.Strings(analysis.Skipped)
	return analysis, nil
}

// Encode renders the analysis as indented JSON.
func (a Analysis) Encode() ([]byte, error) { return strictjson.Indented(a) }

// SignalIndex maps site identifiers to corroborated signals.
func (a Analysis) SignalIndex() map[string]corroborate.Signal { return corroborate.Index(a.Signals) }

// TrendIndex maps site identifiers to trends.
func (a Analysis) TrendIndex() map[string]trend.SiteTrend { return trend.Index(a.Trends) }

// FailedResults lists the result identifiers barred from trends by quality
// control, in ascending order.
func (a Analysis) FailedResults() []string {
	out := make([]string, 0, len(a.Assessments))
	for _, assessment := range a.Assessments {
		if assessment.Verdict == qc.Fail {
			out = append(out, assessment.ResultID)
		}
	}
	sort.Strings(out)
	return out
}
