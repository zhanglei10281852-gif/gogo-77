// Package corroborate combines independent lines of evidence into one graded
// signal per site.
//
// No single stream is trusted on its own. A wastewater rise can be a flow
// artefact, a waterway grab can be a one-off contamination, and a cluster of
// dead birds can have any number of causes. What raises confidence is agreement
// between streams that fail in different ways, so the score is a weighted mean
// over the streams that actually apply to a site, and a configurable minimum
// number of contributing streams caps the grade when only one has anything to
// say.
//
// A stream is applicable when the site has coverage for it, and present when
// that coverage produced something. Weighting over applicable streams rather
// than over all four means a catchment with no sediment programme is not
// permanently penalised for it.
package corroborate

import (
	"fmt"
	"sort"
	"strings"

	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

// Stream names one line of evidence.
type Stream string

// The four evidence streams.
const (
	StreamWastewater Stream = "wastewater_trend"
	StreamWaterway   Stream = "waterway_grab"
	StreamSediment   Stream = "sediment"
	StreamCarcass    Stream = "carcass_events"
)

// Streams returns the streams in report order.
func Streams() []Stream {
	return []Stream{StreamWastewater, StreamWaterway, StreamSediment, StreamCarcass}
}

// Grade is the graded output of corroboration.
type Grade string

// The four grades, weakest first.
const (
	GradeNone     Grade = "none"
	GradeLow      Grade = "low"
	GradeModerate Grade = "moderate"
	GradeHigh     Grade = "high"
)

// Grades returns the grades weakest first.
func Grades() []Grade { return []Grade{GradeNone, GradeLow, GradeModerate, GradeHigh} }

// Rank orders a grade for comparison; a higher number is a stronger signal.
func Rank(g Grade) int {
	switch g {
	case GradeHigh:
		return 3
	case GradeModerate:
		return 2
	case GradeLow:
		return 1
	default:
		return 0
	}
}

// ParseGrade converts a configured grade name into a Grade.
func ParseGrade(text string) (Grade, error) {
	for _, grade := range Grades() {
		if string(grade) == text {
			return grade, nil
		}
	}
	return GradeNone, fmt.Errorf("%q is not a recognised grade", text)
}

// Evidence is one stream's assessment for one site.
type Evidence struct {
	Stream       Stream   `json:"stream"`
	Applicable   bool     `json:"applicable"`
	Present      bool     `json:"present"`
	Strength     float64  `json:"strength"`
	Weight       float64  `json:"weight"`
	Contribution float64  `json:"contribution"`
	SourceSites  []string `json:"source_sites"`
	SourceEvents []string `json:"source_events"`
	Detail       string   `json:"detail"`
}

// Signal is the corroborated verdict for one site.
type Signal struct {
	SiteID            string         `json:"site_id"`
	Matrix            model.Matrix   `json:"matrix"`
	AsOf              timeutil.Stamp `json:"as_of"`
	Evidence          []Evidence     `json:"evidence"`
	ApplicableStreams int            `json:"applicable_streams"`
	PresentStreams    int            `json:"present_streams"`
	Score             float64        `json:"score"`
	RawGrade          Grade          `json:"raw_grade"`
	Grade             Grade          `json:"grade"`
	MinStreamsMet     bool           `json:"minimum_streams_met"`
	Capped            bool           `json:"grade_capped"`
	Rationale         string         `json:"rationale"`
}

// Strength scores used when a stream contributes. They are fixed rather than
// configurable so that a grade means the same thing between profiles; what a
// profile tunes is how much each stream is worth and where the cut points sit.
const (
	strengthSustained = 1.00
	strengthFlagged   = 0.75
	strengthRising    = 0.50
	strengthDetected  = 0.25
	strengthConfirmed = 0.60
)

// trendStrength grades one site trend on the fixed strength scale.
func trendStrength(item trend.SiteTrend) (float64, string) {
	switch {
	case item.SustainedRise:
		return strengthSustained, fmt.Sprintf("%s shows %d consecutive flagged day(s)", item.SiteID, item.ConsecutiveFlagged)
	case item.LatestExceeded:
		return strengthFlagged, fmt.Sprintf("%s is flagged on its latest day %s", item.SiteID, item.LatestDay)
	case item.Slope.Direction == trend.DirectionRising:
		return strengthRising, fmt.Sprintf("%s is rising at %.4f log10 per day", item.SiteID, item.Slope.Slope)
	case item.DetectionDays > 0:
		return strengthDetected, fmt.Sprintf("%s detected on %d day(s) without exceeding a threshold", item.SiteID, item.DetectionDays)
	default:
		return 0, fmt.Sprintf("%s shows no detection in %d observed day(s)", item.SiteID, item.ObservedDays)
	}
}

// Graph is the subset of catchment behaviour corroboration needs. Declaring the
// dependency as an interface keeps it one-directional and lets the package be
// exercised against a stub topology.
type Graph interface {
	Site(id string) (model.Site, bool)
	Ancestors(id string) []string
	SiteIDs() []string
}

// Evaluate corroborates one site.
func Evaluate(cfg config.Config, graph Graph, site model.Site, trends map[string]trend.SiteTrend,
	events []model.CarcassEvent, asOf timeutil.Stamp) Signal {
	signal := Signal{SiteID: site.SiteID, Matrix: site.Matrix, AsOf: asOf}
	scope := append([]string{site.SiteID}, graph.Ancestors(site.SiteID)...)
	sort.Strings(scope)

	signal.Evidence = append(signal.Evidence,
		matrixEvidence(cfg, graph, StreamWastewater, model.MatrixWastewaterInfluent, scope, trends))
	signal.Evidence = append(signal.Evidence,
		matrixEvidence(cfg, graph, StreamWaterway, model.MatrixWaterwayGrab, scope, trends))
	signal.Evidence = append(signal.Evidence,
		matrixEvidence(cfg, graph, StreamSediment, model.MatrixSediment, scope, trends))
	signal.Evidence = append(signal.Evidence, carcassEvidence(cfg, site, events, asOf))

	weighted := 0.0
	weightSum := 0.0
	for index := range signal.Evidence {
		evidence := &signal.Evidence[index]
		if !evidence.Applicable {
			continue
		}
		signal.ApplicableStreams++
		weightSum += evidence.Weight
		weighted += evidence.Weight * evidence.Strength
		evidence.Contribution = numeric.Round4(evidence.Weight * evidence.Strength)
		if evidence.Strength > 0 {
			evidence.Present = true
			signal.PresentStreams++
		}
	}
	signal.Score = numeric.Round4(numeric.SafeDiv(weighted, weightSum, 0))
	signal.RawGrade = gradeFor(cfg, signal.Score)
	signal.Grade = signal.RawGrade
	signal.MinStreamsMet = signal.PresentStreams >= cfg.Corroboration.MinEvidenceStreams
	if !signal.MinStreamsMet && Rank(signal.Grade) > Rank(GradeLow) {
		signal.Grade = GradeLow
		signal.Capped = true
	}
	signal.Rationale = rationale(cfg, signal)
	return signal
}

func gradeFor(cfg config.Config, score float64) Grade {
	corr := cfg.Corroboration
	switch {
	case score >= corr.HighGradeScore:
		return GradeHigh
	case score >= corr.ModerateGradeScore:
		return GradeModerate
	case score >= corr.LowGradeScore:
		return GradeLow
	default:
		return GradeNone
	}
}

// matrixEvidence gathers the trend evidence for every site of one matrix inside
// the site's own catchment scope, taking the strongest as the stream strength.
func matrixEvidence(cfg config.Config, graph Graph, stream Stream, matrix model.Matrix,
	scope []string, trends map[string]trend.SiteTrend) Evidence {
	evidence := Evidence{
		Stream: stream,
		Weight: numeric.Round4(cfg.EvidenceWeight(matrix)),
	}
	details := make([]string, 0, len(scope))
	for _, id := range scope {
		candidate, ok := graph.Site(id)
		if !ok || candidate.Matrix != matrix {
			continue
		}
		evidence.Applicable = true
		evidence.SourceSites = append(evidence.SourceSites, id)
		item, has := trends[id]
		if !has {
			details = append(details, fmt.Sprintf("%s has no evaluated trend", id))
			continue
		}
		strength, detail := trendStrength(item)
		details = append(details, detail)
		if strength > evidence.Strength {
			evidence.Strength = strength
		}
	}
	if evidence.Weight <= 0 {
		evidence.Applicable = false
	}
	sort.Strings(evidence.SourceSites)
	sort.Strings(details)
	if !evidence.Applicable {
		evidence.Detail = fmt.Sprintf("no %s coverage carries weight in this catchment scope", matrix)
		return evidence
	}
	evidence.Strength = numeric.Round4(evidence.Strength)
	evidence.Detail = strings.Join(details, "; ")
	return evidence
}

// carcassEvidence looks for wild-bird mortality inside the configured radius and
// time window of the site. Strength grows with the number of carcasses up to the
// configured saturation count, and a confirmed H5 finding raises a small cluster
// to at least the confirmed-finding strength.
func carcassEvidence(cfg config.Config, site model.Site, events []model.CarcassEvent,
	asOf timeutil.Stamp) Evidence {
	corr := cfg.Corroboration
	evidence := Evidence{Stream: StreamCarcass, Weight: numeric.Round4(corr.CarcassWeight)}
	if evidence.Weight <= 0 {
		evidence.Detail = "carcass evidence carries no weight in this profile"
		return evidence
	}
	window := timeutil.Window{
		From: asOf.StartOfDay().AddDays(-corr.CarcassWindowDays),
		To:   asOf.StartOfDay().AddDays(1),
	}
	withinRadius := 0
	carcasses := 0
	confirmed := false
	nearestKm := -1.0
	for _, event := range events {
		distance := numeric.GreatCircleKm(site.Latitude, site.Longitude, event.Latitude, event.Longitude)
		if distance > corr.CarcassRadiusKm {
			continue
		}
		withinRadius++
		if nearestKm < 0 || distance < nearestKm {
			nearestKm = distance
		}
		if !window.Contains(event.ObservedAt) {
			continue
		}
		evidence.SourceEvents = append(evidence.SourceEvents, event.EventID)
		carcasses += event.CarcassCount
		if event.H5Confirmed {
			confirmed = true
		}
	}
	if withinRadius == 0 {
		evidence.Detail = fmt.Sprintf("no carcass observation within %.1f km of the site", corr.CarcassRadiusKm)
		return evidence
	}
	evidence.Applicable = true
	sort.Strings(evidence.SourceEvents)
	if len(evidence.SourceEvents) == 0 {
		evidence.Detail = fmt.Sprintf("%d carcass observation(s) within %.1f km but none inside the %d day window",
			withinRadius, corr.CarcassRadiusKm, corr.CarcassWindowDays)
		return evidence
	}
	strength := numeric.Clamp(float64(carcasses)/float64(corr.CarcassCountForFull), 0, 1)
	if confirmed && strength < strengthConfirmed {
		strength = strengthConfirmed
	}
	evidence.Strength = numeric.Round4(strength)
	confirmedText := "no confirmed H5 finding"
	if confirmed {
		confirmedText = "at least one confirmed H5 finding"
	}
	evidence.Detail = fmt.Sprintf(
		"%d carcass(es) across %d event(s) within %.1f km and %d day(s), nearest %.1f km, %s",
		carcasses, len(evidence.SourceEvents), corr.CarcassRadiusKm, corr.CarcassWindowDays,
		nearestKm, confirmedText)
	return evidence
}

func rationale(cfg config.Config, signal Signal) string {
	parts := make([]string, 0, 4)
	for _, evidence := range signal.Evidence {
		if !evidence.Applicable {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.2f x %.2f", evidence.Stream, evidence.Strength, evidence.Weight))
	}
	base := fmt.Sprintf("score %.4f from %s over %d applicable stream(s)",
		signal.Score, strings.Join(parts, " + "), signal.ApplicableStreams)
	if signal.Capped {
		return base + fmt.Sprintf("; grade held at %s because only %d of %d required stream(s) contributed",
			signal.Grade, signal.PresentStreams, cfg.Corroboration.MinEvidenceStreams)
	}
	return base + fmt.Sprintf("; %d contributing stream(s) meets the %d stream minimum",
		signal.PresentStreams, cfg.Corroboration.MinEvidenceStreams)
}

// EvaluateAll corroborates every site in the graph, in ascending site order.
func EvaluateAll(cfg config.Config, graph *catchment.Graph, trends []trend.SiteTrend,
	events []model.CarcassEvent, asOf timeutil.Stamp) []Signal {
	index := trend.Index(trends)
	out := make([]Signal, 0, len(graph.SiteIDs()))
	for _, id := range graph.SiteIDs() {
		site, ok := graph.Site(id)
		if !ok {
			continue
		}
		out = append(out, Evaluate(cfg, graph, site, index, events, asOf))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SiteID < out[j].SiteID })
	return out
}

// Index maps site identifiers to signals.
func Index(signals []Signal) map[string]Signal {
	out := make(map[string]Signal, len(signals))
	for _, signal := range signals {
		out[signal.SiteID] = signal
	}
	return out
}

// Strongest returns the highest grade present across the signals.
func Strongest(signals []Signal) Grade {
	best := GradeNone
	for _, signal := range signals {
		if Rank(signal.Grade) > Rank(best) {
			best = signal.Grade
		}
	}
	return best
}
