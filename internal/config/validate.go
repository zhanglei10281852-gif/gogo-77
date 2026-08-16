package config

import (
	"fmt"
	"sort"
	"strings"

	"FluWatershed/internal/model"
)

// Validate checks every field of the configuration. All problems are collected
// before returning so that a badly copied profile can be fixed in one pass
// rather than one error at a time.
func (c Config) Validate() error {
	var problems model.Problems
	if c.SchemaVersion != SchemaVersion {
		problems.Add("config", "schema_version", "must be %q, got %q", SchemaVersion, c.SchemaVersion)
	}
	if !model.ValidID(c.ProfileID) {
		problems.Add("config", "profile_id", "%q is not a valid identifier", c.ProfileID)
	}
	problems = append(problems, c.Quantification.validate()...)
	problems = append(problems, c.Quality.validate()...)
	problems = append(problems, c.Normalisation.validate()...)
	problems = append(problems, c.Baseline.validate()...)
	problems = append(problems, c.Detection.validate()...)
	problems = append(problems, c.Corroboration.validate()...)
	problems = append(problems, c.Alerting.validate()...)
	return problems.Err()
}

func (q Quantification) validate() model.Problems {
	var problems model.Problems
	const scope = "quantification"
	if q.MinQuantifiableReplicates < 1 || q.MinQuantifiableReplicates > 12 {
		problems.Add(scope, "min_quantifiable_replicates", "%d is outside 1..12", q.MinQuantifiableReplicates)
	}
	switch q.NonDetectRule {
	case NonDetectZero, NonDetectHalfLOD, NonDetectLOD:
	default:
		problems.Add(scope, "non_detect_rule", "%q must be one of zero, half_lod, lod", q.NonDetectRule)
	}
	switch q.DNQRule {
	case DNQZero, DNQHalfLOQ, DNQLOQ, DNQLOD:
	default:
		problems.Add(scope, "detected_not_quantifiable_rule", "%q must be one of zero, half_loq, loq, lod", q.DNQRule)
	}
	if q.UncertaintyCoverageK <= 0 || q.UncertaintyCoverageK > 6 {
		problems.Add(scope, "uncertainty_coverage_k", "%.4f is outside (0,6]", q.UncertaintyCoverageK)
	}
	if q.SingleReplicateLog10SD <= 0 || q.SingleReplicateLog10SD > 2 {
		problems.Add(scope, "single_replicate_log10_sd", "%.4f is outside (0,2]", q.SingleReplicateLog10SD)
	}
	if q.ReportSignificantDigits < 2 || q.ReportSignificantDigits > 12 {
		problems.Add(scope, "report_significant_digits", "%d is outside 2..12", q.ReportSignificantDigits)
	}
	if q.MinCopiesPerLitreFloor <= 0 {
		problems.Add(scope, "min_copies_per_litre_floor", "%.4f must be positive", q.MinCopiesPerLitreFloor)
	}
	if trimmed := strings.TrimSpace(q.PrimaryTargetGene); trimmed != q.PrimaryTargetGene {
		problems.Add(scope, "primary_target_gene", "%q must not carry leading or trailing spaces", q.PrimaryTargetGene)
	}
	return problems
}

func (q Quality) validate() model.Problems {
	var problems model.Problems
	const scope = "quality"
	if q.ReplicateCtTolerance <= 0 || q.ReplicateCtTolerance > 10 {
		problems.Add(scope, "replicate_ct_tolerance", "%.4f is outside (0,10]", q.ReplicateCtTolerance)
	}
	if q.MinCurveRSquared < 0.5 || q.MinCurveRSquared > 1 {
		problems.Add(scope, "min_curve_r_squared", "%.4f is outside 0.5..1", q.MinCurveRSquared)
	}
	if q.CurveSlopeMin >= 0 || q.CurveSlopeMax >= 0 {
		problems.Add(scope, "curve_slope_min/max", "both bounds must be negative")
	}
	if q.CurveSlopeMin > q.CurveSlopeMax {
		problems.Add(scope, "curve_slope_min", "%.4f must not exceed curve_slope_max %.4f",
			q.CurveSlopeMin, q.CurveSlopeMax)
	}
	if q.NegativeControlMinCt <= 0 || q.NegativeControlMinCt > 60 {
		problems.Add(scope, "negative_control_min_ct", "%.4f is outside (0,60]", q.NegativeControlMinCt)
	}
	if q.PositiveRecoveryMin <= 0 {
		problems.Add(scope, "positive_recovery_min", "%.4f must be positive", q.PositiveRecoveryMin)
	}
	if q.PositiveRecoveryMax <= q.PositiveRecoveryMin {
		problems.Add(scope, "positive_recovery_max", "%.4f must exceed positive_recovery_min %.4f",
			q.PositiveRecoveryMax, q.PositiveRecoveryMin)
	}
	if q.InhibitionMaxCtShift <= 0 || q.InhibitionMaxCtShift > 15 {
		problems.Add(scope, "inhibition_max_ct_shift", "%.4f is outside (0,15]", q.InhibitionMaxCtShift)
	}
	if len(q.MaxHoldingHours) == 0 {
		problems.Add(scope, "max_holding_hours_by_matrix", "at least one matrix limit is required")
	}
	keys := make([]string, 0, len(q.MaxHoldingHours))
	for key := range q.MaxHoldingHours {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !model.Matrix(key).Valid() {
			problems.Add(scope, "max_holding_hours_by_matrix", "%q is not a recognised matrix", key)
		}
		if hours := q.MaxHoldingHours[key]; hours <= 0 || hours > 24*30 {
			problems.Add(scope, "max_holding_hours_by_matrix", "%s limit %.2f is outside (0,720]", key, hours)
		}
	}
	if q.TransportTempMinC >= q.TransportTempMaxC {
		problems.Add(scope, "transport_temperature_min_c", "%.2f must be below transport_temperature_max_c %.2f",
			q.TransportTempMinC, q.TransportTempMaxC)
	}
	if q.TransportTempMinC < -80 || q.TransportTempMaxC > 60 {
		problems.Add(scope, "transport_temperature", "bounds must stay inside -80..60")
	}
	if q.MaxCustodyGapHours <= 0 || q.MaxCustodyGapHours > 24*30 {
		problems.Add(scope, "max_custody_gap_hours", "%.2f is outside (0,720]", q.MaxCustodyGapHours)
	}
	return problems
}

func (n Normalisation) validate() model.Problems {
	var problems model.Problems
	const scope = "normalisation"
	if n.IndicatorCorrectionMin <= 0 {
		problems.Add(scope, "indicator_correction_min", "%.4f must be positive", n.IndicatorCorrectionMin)
	}
	if n.IndicatorCorrectionMax <= n.IndicatorCorrectionMin {
		problems.Add(scope, "indicator_correction_max", "%.4f must exceed indicator_correction_min %.4f",
			n.IndicatorCorrectionMax, n.IndicatorCorrectionMin)
	}
	if n.IndicatorCorrectionMax > 100 {
		problems.Add(scope, "indicator_correction_max", "%.4f is implausibly large", n.IndicatorCorrectionMax)
	}
	if n.PerCapitaScale <= 0 {
		problems.Add(scope, "per_capita_scale", "%.4f must be positive", n.PerCapitaScale)
	}
	if n.EnablePopulationNormalisation && !n.EnableFlowNormalisation {
		problems.Add(scope, "enable_population_normalisation",
			"population normalisation needs flow normalisation because it divides a daily load")
	}
	return problems
}

func (b Baseline) validate() model.Problems {
	var problems model.Problems
	const scope = "baseline"
	if b.WindowDays < 3 || b.WindowDays > 365 {
		problems.Add(scope, "window_days", "%d is outside 3..365", b.WindowDays)
	}
	if b.MinPoints < 2 {
		problems.Add(scope, "min_points", "%d must be at least 2", b.MinPoints)
	}
	if b.MinPoints > b.WindowDays {
		problems.Add(scope, "min_points", "%d cannot exceed window_days %d", b.MinPoints, b.WindowDays)
	}
	if b.MinDispersion < 0 {
		problems.Add(scope, "min_dispersion", "%.4f cannot be negative", b.MinDispersion)
	}
	return problems
}

func (d Detection) validate() model.Problems {
	var problems model.Problems
	const scope = "detection"
	if d.BaselineMultiple <= 1 || d.BaselineMultiple > 1000 {
		problems.Add(scope, "baseline_multiple", "%.4f is outside (1,1000]", d.BaselineMultiple)
	}
	if d.RobustZThreshold <= 0 || d.RobustZThreshold > 20 {
		problems.Add(scope, "robust_z_threshold", "%.4f is outside (0,20]", d.RobustZThreshold)
	}
	if d.DefaultAbsoluteLoad <= 0 {
		problems.Add(scope, "default_absolute_threshold_copies_per_day", "%.4g must be positive",
			d.DefaultAbsoluteLoad)
	}
	if d.DefaultAbsoluteConcentration <= 0 {
		problems.Add(scope, "default_absolute_threshold_copies_per_litre", "%.4g must be positive",
			d.DefaultAbsoluteConcentration)
	}
	if d.SustainedIntervals < 2 || d.SustainedIntervals > 60 {
		problems.Add(scope, "sustained_intervals", "%d is outside 2..60", d.SustainedIntervals)
	}
	if d.TrendWindowDays < 3 || d.TrendWindowDays > 365 {
		problems.Add(scope, "trend_window_days", "%d is outside 3..365", d.TrendWindowDays)
	}
	if d.MinTrendPoints < 3 {
		problems.Add(scope, "min_trend_points", "%d must be at least 3", d.MinTrendPoints)
	}
	if d.MinTrendPoints > d.TrendWindowDays {
		problems.Add(scope, "min_trend_points", "%d cannot exceed trend_window_days %d",
			d.MinTrendPoints, d.TrendWindowDays)
	}
	if d.RisingSlopePerDay <= 0 {
		problems.Add(scope, "rising_slope_log10_per_day", "%.4f must be positive", d.RisingSlopePerDay)
	}
	if d.FallingSlopePerDay >= 0 {
		problems.Add(scope, "falling_slope_log10_per_day", "%.4f must be negative", d.FallingSlopePerDay)
	}
	if d.UpstreamTravelToleranceHr < 0 || d.UpstreamTravelToleranceHr > 24*30 {
		problems.Add(scope, "upstream_travel_tolerance_hours", "%.2f is outside 0..720",
			d.UpstreamTravelToleranceHr)
	}
	return problems
}

func (c Corroboration) validate() model.Problems {
	var problems model.Problems
	const scope = "corroboration"
	weights := map[string]float64{
		"wastewater_weight":    c.WastewaterWeight,
		"waterway_grab_weight": c.WaterwayGrabWeight,
		"carcass_weight":       c.CarcassWeight,
		"sediment_weight":      c.SedimentWeight,
	}
	names := make([]string, 0, len(weights))
	for name := range weights {
		names = append(names, name)
	}
	sort.Strings(names)
	total := 0.0
	for _, name := range names {
		value := weights[name]
		if value < 0 || value > 1 {
			problems.Add(scope, name, "%.4f is outside 0..1", value)
		}
		total += value
	}
	if total <= 0 {
		problems.Add(scope, "weights", "at least one stream must carry weight")
	}
	if total > 1.0001 {
		problems.Add(scope, "weights", "the four stream weights sum to %.4f, which must not exceed 1", total)
	}
	if c.CarcassRadiusKm <= 0 || c.CarcassRadiusKm > 1000 {
		problems.Add(scope, "carcass_radius_km", "%.2f is outside (0,1000]", c.CarcassRadiusKm)
	}
	if c.CarcassWindowDays < 1 || c.CarcassWindowDays > 365 {
		problems.Add(scope, "carcass_window_days", "%d is outside 1..365", c.CarcassWindowDays)
	}
	if c.MinEvidenceStreams < 1 || c.MinEvidenceStreams > 4 {
		problems.Add(scope, "min_evidence_streams", "%d is outside 1..4", c.MinEvidenceStreams)
	}
	if c.CarcassCountForFull < 1 {
		problems.Add(scope, "carcass_count_for_full_weight", "%d must be at least 1", c.CarcassCountForFull)
	}
	if c.LowGradeScore <= 0 || c.LowGradeScore >= c.ModerateGradeScore {
		problems.Add(scope, "low_grade_score", "%.4f must be positive and below moderate_grade_score %.4f",
			c.LowGradeScore, c.ModerateGradeScore)
	}
	if c.ModerateGradeScore >= c.HighGradeScore {
		problems.Add(scope, "moderate_grade_score", "%.4f must be below high_grade_score %.4f",
			c.ModerateGradeScore, c.HighGradeScore)
	}
	if c.HighGradeScore > 1 {
		problems.Add(scope, "high_grade_score", "%.4f must not exceed 1", c.HighGradeScore)
	}
	return problems
}

// AlertGrades lists the grade names an alert threshold may name, weakest first.
func AlertGrades() []string { return []string{"low", "moderate", "high"} }

func (a Alerting) validate() model.Problems {
	var problems model.Problems
	const scope = "alerting"
	valid := false
	for _, grade := range AlertGrades() {
		if a.OpenGrade == grade {
			valid = true
		}
	}
	if !valid {
		problems.Add(scope, "open_grade", "%q must be one of %s", a.OpenGrade, strings.Join(AlertGrades(), ", "))
	}
	if a.SustainAfterUpdates < 1 || a.SustainAfterUpdates > 60 {
		problems.Add(scope, "sustain_after_updates", "%d is outside 1..60", a.SustainAfterUpdates)
	}
	if a.ResolveAfterUpdates < 1 || a.ResolveAfterUpdates > 60 {
		problems.Add(scope, "resolve_after_updates", "%d is outside 1..60", a.ResolveAfterUpdates)
	}
	if a.CooldownHours < 0 || a.CooldownHours > 24*365 {
		problems.Add(scope, "cooldown_hours", "%.2f is outside 0..8760", a.CooldownHours)
	}
	if a.IdentifierBytes < 4 || a.IdentifierBytes > 32 {
		problems.Add(scope, "identifier_bytes", "%d is outside 4..32", a.IdentifierBytes)
	}
	return problems
}

// Describe renders the configuration as aligned key/value lines for the text
// output mode. The ordering is fixed, not map order.
func (c Config) Describe() []string {
	return []string{
		fmt.Sprintf("profile                     %s", c.ProfileID),
		fmt.Sprintf("min quantifiable replicates %d", c.Quantification.MinQuantifiableReplicates),
		fmt.Sprintf("non-detect rule             %s", c.Quantification.NonDetectRule),
		fmt.Sprintf("below-LOQ rule              %s", c.Quantification.DNQRule),
		fmt.Sprintf("coverage factor k           %.2f", c.Quantification.UncertaintyCoverageK),
		fmt.Sprintf("replicate Ct tolerance      %.2f", c.Quality.ReplicateCtTolerance),
		fmt.Sprintf("transport window            %.1f..%.1f C", c.Quality.TransportTempMinC, c.Quality.TransportTempMaxC),
		fmt.Sprintf("baseline window             %d day(s), min %d point(s)", c.Baseline.WindowDays, c.Baseline.MinPoints),
		fmt.Sprintf("baseline multiple           %.2fx", c.Detection.BaselineMultiple),
		fmt.Sprintf("robust z threshold          %.2f", c.Detection.RobustZThreshold),
		fmt.Sprintf("absolute thresholds         %.4g copies/day, %.4g copies/L",
			c.Detection.DefaultAbsoluteLoad, c.Detection.DefaultAbsoluteConcentration),
		fmt.Sprintf("sustained intervals         %d", c.Detection.SustainedIntervals),
		fmt.Sprintf("carcass search              %.1f km / %d day(s)", c.Corroboration.CarcassRadiusKm, c.Corroboration.CarcassWindowDays),
		fmt.Sprintf("minimum evidence streams    %d", c.Corroboration.MinEvidenceStreams),
		fmt.Sprintf("grade cut points            %.2f / %.2f / %.2f", c.Corroboration.LowGradeScore, c.Corroboration.ModerateGradeScore, c.Corroboration.HighGradeScore),
		fmt.Sprintf("alert opens at              %s", c.Alerting.OpenGrade),
		fmt.Sprintf("cooldown                    %.1f hour(s)", c.Alerting.CooldownHours),
	}
}
