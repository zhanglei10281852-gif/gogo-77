// Package qc decides whether a measured result is allowed to influence a trend.
//
// Each rule is a separate named check that records what it observed and what
// limit it was held to, so a rejection can always be explained without rerunning
// anything. A check reports pass, warn or fail; the assessment takes the worst
// of them. A failing assessment marks the result unusable, and the baseline,
// trend and roll-up stages honour that flag: a result that failed quality
// control never reaches a baseline, an exceedance test or a catchment load.
package qc

import (
	"fmt"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/quant"
)

// Severity is the outcome of a single check or a whole assessment.
type Severity string

// The three outcomes, weakest first.
const (
	Pass Severity = "pass"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

// rank orders severities so that the worst can be taken.
func rank(s Severity) int {
	switch s {
	case Fail:
		return 2
	case Warn:
		return 1
	default:
		return 0
	}
}

// worse returns whichever severity is more serious.
func worse(a, b Severity) Severity {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// Check names of every rule, exported so reports and tests refer to one string.
const (
	CheckReplicateAgreement = "replicate_agreement"
	CheckStandardCurve      = "standard_curve"
	CheckNegativeControl    = "negative_control"
	CheckPositiveControl    = "positive_control_recovery"
	CheckInhibition         = "inhibition_control"
	CheckHoldingTime        = "holding_time"
	CheckTransportTemp      = "transport_temperature"
	CheckCustody            = "custody_continuity"
)

// CheckNames returns every rule name in the order checks are reported.
func CheckNames() []string {
	return []string{
		CheckReplicateAgreement, CheckStandardCurve, CheckNegativeControl,
		CheckPositiveControl, CheckInhibition, CheckHoldingTime,
		CheckTransportTemp, CheckCustody,
	}
}

// Check is one rule applied to one result.
type Check struct {
	Name     string   `json:"name"`
	Severity Severity `json:"severity"`
	Detail   string   `json:"detail"`
	Observed float64  `json:"observed"`
	Limit    float64  `json:"limit"`
}

// Assessment is the full quality verdict for one result.
type Assessment struct {
	ResultID       string       `json:"result_id"`
	SampleID       string       `json:"sample_id"`
	SiteID         string       `json:"site_id"`
	Matrix         model.Matrix `json:"matrix"`
	Day            string       `json:"day"`
	Verdict        Severity     `json:"verdict"`
	UsableForTrend bool         `json:"usable_for_trend"`
	Checks         []Check      `json:"checks"`
	Failed         []string     `json:"failed_checks"`
	Warned         []string     `json:"warned_checks"`
}

// Assess applies every rule to one measurement and its source records.
func Assess(cfg config.Config, sample model.Sample, result model.Result, measurement quant.Measurement) Assessment {
	assessment := Assessment{
		ResultID: result.ResultID,
		SampleID: result.SampleID,
		SiteID:   sample.SiteID,
		Matrix:   sample.Matrix,
		Day:      sample.CollectedAt.Day(),
		Verdict:  Pass,
	}
	assessment.add(replicateAgreement(cfg, result, measurement))
	assessment.add(standardCurve(cfg, result))
	assessment.add(negativeControl(cfg, result))
	assessment.add(positiveControl(cfg, result))
	assessment.add(inhibition(cfg, result))
	assessment.add(holdingTime(cfg, sample, result))
	assessment.add(transportTemperature(cfg, sample))
	assessment.add(custody(cfg, sample, result))
	sort.SliceStable(assessment.Checks, func(i, j int) bool {
		return assessment.Checks[i].Name < assessment.Checks[j].Name
	})
	assessment.UsableForTrend = assessment.Verdict != Fail
	sort.Strings(assessment.Failed)
	sort.Strings(assessment.Warned)
	return assessment
}

func (a *Assessment) add(check Check) {
	a.Checks = append(a.Checks, check)
	a.Verdict = worse(a.Verdict, check.Severity)
	switch check.Severity {
	case Fail:
		a.Failed = append(a.Failed, check.Name)
	case Warn:
		a.Warned = append(a.Warned, check.Name)
	}
}

// replicateAgreement compares the Ct spread of the amplifying wells against the
// configured tolerance. A single amplifying well cannot disagree with itself, so
// it passes with a note; no amplification at all is not an agreement question.
func replicateAgreement(cfg config.Config, result model.Result, measurement quant.Measurement) Check {
	tolerance := cfg.Quality.ReplicateCtTolerance
	detected := result.DetectedCts()
	check := Check{Name: CheckReplicateAgreement, Severity: Pass, Limit: numeric.Round2(tolerance)}
	switch len(detected) {
	case 0:
		check.Detail = "no amplifying wells to compare"
		return check
	case 1:
		check.Detail = "single amplifying well; agreement not assessable"
		check.Severity = Warn
		return check
	}
	spread := measurement.CtSpread
	check.Observed = numeric.Round2(spread)
	if spread > tolerance {
		check.Severity = Fail
		check.Detail = fmt.Sprintf("Ct spread %.2f across %d wells exceeds the %.2f tolerance",
			spread, len(detected), tolerance)
		return check
	}
	check.Detail = fmt.Sprintf("Ct spread %.2f across %d wells is within the %.2f tolerance",
		spread, len(detected), tolerance)
	return check
}

func standardCurve(cfg config.Config, result model.Result) Check {
	quality := cfg.Quality
	curve := result.Curve
	check := Check{
		Name:     CheckStandardCurve,
		Severity: Pass,
		Observed: numeric.Round4(curve.RSquared),
		Limit:    numeric.Round4(quality.MinCurveRSquared),
	}
	if curve.RSquared < quality.MinCurveRSquared {
		check.Severity = Fail
		check.Detail = fmt.Sprintf("curve r-squared %.4f is below the %.4f minimum",
			curve.RSquared, quality.MinCurveRSquared)
		return check
	}
	if curve.Slope < quality.CurveSlopeMin || curve.Slope > quality.CurveSlopeMax {
		check.Severity = Fail
		check.Observed = numeric.Round4(curve.Slope)
		check.Detail = fmt.Sprintf("curve slope %.4f is outside the accepted %.4f..%.4f window",
			curve.Slope, quality.CurveSlopeMin, quality.CurveSlopeMax)
		return check
	}
	check.Detail = fmt.Sprintf("curve slope %.4f and r-squared %.4f are acceptable", curve.Slope, curve.RSquared)
	return check
}

// negativeControl fails when the no-template control amplified early enough to
// indicate carry-over contamination, and also when the extraction blank was
// reported as detected.
func negativeControl(cfg config.Config, result model.Result) Check {
	limit := cfg.Quality.NegativeControlMinCt
	check := Check{Name: CheckNegativeControl, Severity: Pass, Limit: numeric.Round2(limit)}
	if result.Controls.ExtractionBlankDetected {
		check.Severity = Fail
		check.Detail = "extraction blank reported as detected"
		return check
	}
	if result.Controls.NegativeControlCt == nil {
		check.Detail = "no-template control did not amplify"
		return check
	}
	observed := *result.Controls.NegativeControlCt
	check.Observed = numeric.Round2(observed)
	if observed < limit {
		check.Severity = Fail
		check.Detail = fmt.Sprintf("no-template control amplified at Ct %.2f, below the %.2f contamination limit",
			observed, limit)
		return check
	}
	check.Detail = fmt.Sprintf("no-template control amplified late at Ct %.2f, above the %.2f limit", observed, limit)
	return check
}

func positiveControl(cfg config.Config, result model.Result) Check {
	quality := cfg.Quality
	controls := result.Controls
	recovery := numeric.SafeDiv(controls.PositiveControlObserved, controls.PositiveControlExpected, 0)
	check := Check{
		Name:     CheckPositiveControl,
		Severity: Pass,
		Observed: numeric.Round4(recovery),
		Limit:    numeric.Round4(quality.PositiveRecoveryMin),
	}
	if recovery < quality.PositiveRecoveryMin || recovery > quality.PositiveRecoveryMax {
		check.Severity = Fail
		check.Detail = fmt.Sprintf("positive control recovered %.1f%% of the expected copies, outside %.1f%%..%.1f%%",
			recovery*100, quality.PositiveRecoveryMin*100, quality.PositiveRecoveryMax*100)
		return check
	}
	check.Detail = fmt.Sprintf("positive control recovered %.1f%% of the expected copies", recovery*100)
	return check
}

// inhibition compares the spiked inhibition control against its uninhibited
// reference. A late control means the extract is suppressing amplification, so
// the reported concentration is an underestimate of unknown size.
func inhibition(cfg config.Config, result model.Result) Check {
	limit := cfg.Quality.InhibitionMaxCtShift
	check := Check{Name: CheckInhibition, Severity: Pass, Limit: numeric.Round2(limit)}
	controls := result.Controls
	if controls.InhibitionControlCt == nil {
		check.Severity = Fail
		check.Detail = "inhibition control did not amplify at all, which indicates complete suppression"
		return check
	}
	shift := *controls.InhibitionControlCt - controls.InhibitionReferenceCt
	check.Observed = numeric.Round2(shift)
	switch {
	case shift > limit:
		check.Severity = Fail
		check.Detail = fmt.Sprintf("inhibition control is %.2f cycles late against the %.2f limit", shift, limit)
	case shift < -limit:
		check.Severity = Warn
		check.Detail = fmt.Sprintf("inhibition control is %.2f cycles early, which suggests a reference mismatch", -shift)
	default:
		check.Detail = fmt.Sprintf("inhibition control shift %.2f cycles is within the %.2f limit", shift, limit)
	}
	return check
}

// holdingTime measures collection to analysis against the per-matrix limit.
// Crossing three quarters of the limit warns; crossing it fails. Analysis dated
// before collection fails outright as an impossible record.
func holdingTime(cfg config.Config, sample model.Sample, result model.Result) Check {
	check := Check{Name: CheckHoldingTime, Severity: Pass}
	limit, ok := cfg.HoldingLimitHours(sample.Matrix)
	if !ok {
		check.Detail = fmt.Sprintf("no holding-time limit configured for matrix %s", sample.Matrix)
		return check
	}
	check.Limit = numeric.Round2(limit)
	if !sample.CollectedAt.IsSet() || !result.AnalysedAt.IsSet() {
		check.Severity = Fail
		check.Detail = "holding time cannot be computed without both collection and analysis times"
		return check
	}
	hours := result.AnalysedAt.HoursSince(sample.CollectedAt)
	check.Observed = numeric.Round2(hours)
	switch {
	case hours < 0:
		check.Severity = Fail
		check.Detail = fmt.Sprintf("analysis precedes collection by %.2f hour(s)", -hours)
	case hours > limit:
		check.Severity = Fail
		check.Detail = fmt.Sprintf("holding time %.2f hour(s) exceeds the %.2f hour limit for %s",
			hours, limit, sample.Matrix)
	case hours > limit*0.75:
		check.Severity = Warn
		check.Detail = fmt.Sprintf("holding time %.2f hour(s) is within the %.2f hour limit but past three quarters of it",
			hours, limit)
	default:
		check.Detail = fmt.Sprintf("holding time %.2f hour(s) is comfortably inside the %.2f hour limit", hours, limit)
	}
	return check
}

// transportTemperature checks the reported transport temperature and every
// custody step that recorded one. The worst reading decides the check.
func transportTemperature(cfg config.Config, sample model.Sample) Check {
	quality := cfg.Quality
	check := Check{
		Name:     CheckTransportTemp,
		Severity: Pass,
		Observed: numeric.Round2(sample.TransportTempC),
		Limit:    numeric.Round2(quality.TransportTempMaxC),
	}
	readings := []float64{sample.TransportTempC}
	for _, step := range sample.Custody {
		if step.TemperatureC != nil {
			readings = append(readings, *step.TemperatureC)
		}
	}
	worstReading := sample.TransportTempC
	violation := false
	for _, reading := range readings {
		if reading < quality.TransportTempMinC || reading > quality.TransportTempMaxC {
			if !violation || excursion(reading, quality) > excursion(worstReading, quality) {
				worstReading = reading
			}
			violation = true
		}
	}
	if violation {
		check.Severity = Fail
		check.Observed = numeric.Round2(worstReading)
		check.Detail = fmt.Sprintf("temperature %.2f C is outside the %.2f..%.2f C transport window",
			worstReading, quality.TransportTempMinC, quality.TransportTempMaxC)
		return check
	}
	check.Detail = fmt.Sprintf("all %d temperature reading(s) sit inside the %.2f..%.2f C window",
		len(readings), quality.TransportTempMinC, quality.TransportTempMaxC)
	return check
}

func excursion(reading float64, quality config.Quality) float64 {
	if reading < quality.TransportTempMinC {
		return quality.TransportTempMinC - reading
	}
	if reading > quality.TransportTempMaxC {
		return reading - quality.TransportTempMaxC
	}
	return 0
}

// custody verifies the physical chain: steps in time order, the collection step
// matching the stated collection time, no unexplained gap longer than the
// configured maximum, and analysis not preceding the last handoff.
func custody(cfg config.Config, sample model.Sample, result model.Result) Check {
	quality := cfg.Quality
	check := Check{Name: CheckCustody, Severity: Pass, Limit: numeric.Round2(quality.MaxCustodyGapHours)}
	severity := Fail
	if !quality.RequireCustodyContinuity {
		severity = Warn
	}
	if len(sample.Custody) == 0 {
		check.Severity = severity
		check.Detail = "no custody steps recorded"
		return check
	}
	if ordered, index := model.CustodyOrdered(sample.Custody); !ordered {
		check.Severity = severity
		check.Detail = fmt.Sprintf("custody step %d at %s precedes the step before it",
			index+1, sample.Custody[index].At)
		return check
	}
	first := sample.Custody[0]
	if first.At.IsSet() && sample.CollectedAt.IsSet() && !first.At.Equal(sample.CollectedAt) {
		check.Severity = severity
		check.Detail = fmt.Sprintf("collection step time %s does not match collected_at %s",
			first.At, sample.CollectedAt)
		return check
	}
	largestGap := 0.0
	gapIndex := 0
	for index := 1; index < len(sample.Custody); index++ {
		gap := sample.Custody[index].At.HoursSince(sample.Custody[index-1].At)
		if gap > largestGap {
			largestGap = gap
			gapIndex = index
		}
	}
	last, ok := sample.LastCustodyAt()
	if ok && result.AnalysedAt.IsSet() {
		tail := result.AnalysedAt.HoursSince(last)
		if tail < 0 {
			check.Severity = severity
			check.Observed = numeric.Round2(-tail)
			check.Detail = fmt.Sprintf("analysis at %s precedes the final custody step at %s",
				result.AnalysedAt, last)
			return check
		}
		if tail > largestGap {
			largestGap = tail
			gapIndex = len(sample.Custody)
		}
	}
	check.Observed = numeric.Round2(largestGap)
	if largestGap > quality.MaxCustodyGapHours {
		check.Severity = severity
		check.Detail = fmt.Sprintf("largest custody gap %.2f hour(s) before step %d exceeds the %.2f hour limit",
			largestGap, gapIndex+1, quality.MaxCustodyGapHours)
		return check
	}
	check.Detail = fmt.Sprintf("%d custody step(s) are continuous with a largest gap of %.2f hour(s)",
		len(sample.Custody), largestGap)
	return check
}

// AssessAll assesses every measurement in one pass and returns the assessments
// keyed by result identifier alongside the ordered slice.
func AssessAll(cfg config.Config, bundle model.Bundle, measurements []quant.Measurement) ([]Assessment, map[string]Assessment) {
	samples := bundle.SampleByID()
	results := make(map[string]model.Result, len(bundle.Results))
	for _, result := range bundle.Results {
		results[result.ResultID] = result
	}
	assessments := make([]Assessment, 0, len(measurements))
	index := make(map[string]Assessment, len(measurements))
	for _, measurement := range measurements {
		sample, hasSample := samples[measurement.SampleID]
		result, hasResult := results[measurement.ResultID]
		if !hasSample || !hasResult {
			continue
		}
		assessment := Assess(cfg, sample, result, measurement)
		assessments = append(assessments, assessment)
		index[assessment.ResultID] = assessment
	}
	sort.SliceStable(assessments, func(i, j int) bool {
		left, right := assessments[i], assessments[j]
		if left.SiteID != right.SiteID {
			return left.SiteID < right.SiteID
		}
		if left.Day != right.Day {
			return left.Day < right.Day
		}
		return left.ResultID < right.ResultID
	})
	return assessments, index
}

// Tally counts assessments by verdict, for report headers.
type Tally struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

// Count summarises a slice of assessments.
func Count(assessments []Assessment) Tally {
	tally := Tally{Total: len(assessments)}
	for _, assessment := range assessments {
		switch assessment.Verdict {
		case Fail:
			tally.Failed++
		case Warn:
			tally.Warned++
		default:
			tally.Passed++
		}
	}
	return tally
}

// FailureReasons counts how often each named check failed, in name order.
func FailureReasons(assessments []Assessment) []Check {
	counts := make(map[string]int)
	for _, assessment := range assessments {
		for _, name := range assessment.Failed {
			counts[name]++
		}
	}
	out := make([]Check, 0, len(counts))
	for _, name := range CheckNames() {
		if counts[name] == 0 {
			continue
		}
		out = append(out, Check{
			Name:     name,
			Severity: Fail,
			Observed: float64(counts[name]),
			Detail:   fmt.Sprintf("%d result(s) failed %s", counts[name], name),
		})
	}
	return out
}
