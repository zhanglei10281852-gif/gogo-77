// Package quant turns raw assay output into a concentration.
//
// The chain is deliberately explicit, one documented multiplication at a time,
// because every step is a place where a surveillance number can go quietly
// wrong:
//
//  1. a replicate Ct becomes copies per reaction through the standard curve,
//     log10(copies) = (Ct - intercept) / slope;
//  2. copies per reaction becomes copies per litre of original sample by
//     multiplying the dilution factor and the extract-to-template ratio, then
//     dividing by the concentrated sample volume and the recovery fraction;
//  3. replicates are classified and aggregated under one stated rule;
//  4. a coverage band is derived from the log10 spread of the replicates.
//
// Nothing here consults quality control. A result may be perfectly
// quantifiable and still be barred from trends by the qc package; keeping the
// two apart means a rejected result can still be reported with its number.
package quant

import (
	"fmt"
	"math"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
)

// ReplicateClass is where one well fell relative to the assay limits.
type ReplicateClass string

// The three possible placements of a well.
const (
	ClassNonDetect    ReplicateClass = "non_detect"
	ClassBelowLOQ     ReplicateClass = "below_loq"
	ClassQuantifiable ReplicateClass = "quantifiable"
)

// Status is the aggregate verdict for a whole result.
type Status string

// The aggregate verdicts.
const (
	StatusQuantified            Status = "quantified"
	StatusDetectedNotQuantified Status = "detected_not_quantified"
	StatusNonDetect             Status = "non_detect"
)

// ReplicateOutcome is one well after conversion.
type ReplicateOutcome struct {
	Well              string         `json:"well"`
	Ct                *float64       `json:"ct"`
	CopiesPerReaction float64        `json:"copies_per_reaction"`
	CopiesPerLitre    float64        `json:"copies_per_litre"`
	Class             ReplicateClass `json:"class"`
	BelowLOD          bool           `json:"below_lod"`
}

// Measurement is the quantified form of one assay result.
type Measurement struct {
	ResultID    string         `json:"result_id"`
	SampleID    string         `json:"sample_id"`
	SiteID      string         `json:"site_id"`
	Matrix      model.Matrix   `json:"matrix"`
	TargetGene  string         `json:"target_gene"`
	Day         string         `json:"day"`
	CollectedAt timeutil.Stamp `json:"collected_at"`
	AnalysedAt  timeutil.Stamp `json:"analysed_at"`

	Replicates             []ReplicateOutcome `json:"replicates"`
	TotalReplicates        int                `json:"total_replicates"`
	DetectedReplicates     int                `json:"detected_replicates"`
	QuantifiableReplicates int                `json:"quantifiable_replicates"`

	Status   Status `json:"status"`
	Detected bool   `json:"detected"`

	CopiesPerLitre      float64 `json:"copies_per_litre"`
	QuantifiedMean      float64 `json:"quantifiable_mean_copies_per_litre"`
	Log10CopiesPerLitre float64 `json:"log10_copies_per_litre"`
	LowerCopiesPerLitre float64 `json:"lower_copies_per_litre"`
	UpperCopiesPerLitre float64 `json:"upper_copies_per_litre"`
	Log10StandardError  float64 `json:"log10_standard_error"`

	LODCopiesPerLitre float64 `json:"lod_copies_per_litre"`
	LOQCopiesPerLitre float64 `json:"loq_copies_per_litre"`
	ConversionFactor  float64 `json:"conversion_factor_per_litre"`
	CtSpread          float64 `json:"ct_spread"`

	Notes []string `json:"notes"`
}

// CopiesPerReaction inverts the standard curve. The curve is stored as
// Ct = slope*log10(copies) + intercept with a negative slope, so a lower Ct
// yields more copies.
func CopiesPerReaction(ct float64, curve model.StandardCurve) (float64, error) {
	if curve.Slope == 0 {
		return 0, fmt.Errorf("standard curve slope is zero")
	}
	exponent := (ct - curve.Intercept) / curve.Slope
	if exponent > 24 {
		return 0, fmt.Errorf("ct %.3f implies more than 1e24 copies per reaction", ct)
	}
	value := math.Pow(10, exponent)
	if !numeric.Finite(value) {
		return 0, fmt.Errorf("ct %.3f produced a non-finite copy number", ct)
	}
	return value, nil
}

// ConversionFactor is the multiplier from copies per reaction to copies per
// litre of original sample.
//
//	factor = dilution * (extract_ul / template_ul) / (volume_ml / 1000) / recovery
//
// The extract-to-template ratio scales a single reaction up to the whole
// extract, the volume term divides by the litres that extract represents, and
// the recovery fraction corrects for material lost during concentration.
func ConversionFactor(result model.Result, sample model.Sample) (float64, error) {
	if result.TemplateVolumeUL <= 0 {
		return 0, fmt.Errorf("template_volume_ul must be positive")
	}
	if sample.VolumeML <= 0 {
		return 0, fmt.Errorf("sample volume_ml must be positive")
	}
	if result.RecoveryFraction <= 0 {
		return 0, fmt.Errorf("recovery_fraction must be positive")
	}
	if result.DilutionFactor < 1 {
		return 0, fmt.Errorf("dilution_factor must be at least 1")
	}
	litres := sample.VolumeML / 1000
	factor := result.DilutionFactor * (result.ExtractVolumeUL / result.TemplateVolumeUL)
	factor = factor / litres / result.RecoveryFraction
	if !numeric.Finite(factor) || factor <= 0 {
		return 0, fmt.Errorf("conversion factor %.6g is not usable", factor)
	}
	return factor, nil
}

// Quantify converts one result against its sample under the given policy.
func Quantify(cfg config.Config, sample model.Sample, result model.Result) (Measurement, error) {
	factor, err := ConversionFactor(result, sample)
	if err != nil {
		return Measurement{}, fmt.Errorf("result %s: %w", result.ResultID, err)
	}
	measurement := Measurement{
		ResultID:         result.ResultID,
		SampleID:         result.SampleID,
		SiteID:           sample.SiteID,
		Matrix:           sample.Matrix,
		TargetGene:       result.TargetGene,
		Day:              sample.CollectedAt.Day(),
		CollectedAt:      sample.CollectedAt,
		AnalysedAt:       result.AnalysedAt,
		TotalReplicates:  len(result.Replicates),
		ConversionFactor: numeric.RoundSignificant(factor, cfg.Quantification.ReportSignificantDigits),
		LODCopiesPerLitre: numeric.RoundSignificant(
			result.Curve.LODCopiesPerReaction*factor, cfg.Quantification.ReportSignificantDigits),
		LOQCopiesPerLitre: numeric.RoundSignificant(
			result.Curve.LOQCopiesPerReaction*factor, cfg.Quantification.ReportSignificantDigits),
	}

	quantifiableLog10 := make([]float64, 0, len(result.Replicates))
	quantifiableLinear := make([]float64, 0, len(result.Replicates))
	for _, replicate := range result.Replicates {
		outcome := ReplicateOutcome{Well: replicate.Well, Class: ClassNonDetect}
		if replicate.Detected() {
			ct := *replicate.Ct
			perReaction, convErr := CopiesPerReaction(ct, result.Curve)
			if convErr != nil {
				return Measurement{}, fmt.Errorf("result %s well %s: %w", result.ResultID, replicate.Well, convErr)
			}
			perLitre := perReaction * factor
			ctCopy := ct
			outcome.Ct = &ctCopy
			outcome.CopiesPerReaction = numeric.RoundSignificant(perReaction, cfg.Quantification.ReportSignificantDigits)
			outcome.CopiesPerLitre = numeric.RoundSignificant(perLitre, cfg.Quantification.ReportSignificantDigits)
			outcome.BelowLOD = perReaction < result.Curve.LODCopiesPerReaction
			measurement.DetectedReplicates++
			if perReaction >= result.Curve.LOQCopiesPerReaction {
				outcome.Class = ClassQuantifiable
				measurement.QuantifiableReplicates++
				quantifiableLinear = append(quantifiableLinear, perLitre)
				quantifiableLog10 = append(quantifiableLog10, math.Log10(perLitre))
			} else {
				outcome.Class = ClassBelowLOQ
			}
		}
		measurement.Replicates = append(measurement.Replicates, outcome)
	}

	if detected := result.DetectedCts(); len(detected) >= 2 {
		spread, spreadErr := numeric.Spread(detected)
		if spreadErr == nil {
			measurement.CtSpread = numeric.Round2(spread)
		}
	}

	measurement.Detected = measurement.DetectedReplicates > 0
	applyAggregation(cfg, &measurement, quantifiableLinear, quantifiableLog10)
	applyBand(cfg, &measurement, quantifiableLog10)
	sort.Strings(measurement.Notes)
	return measurement, nil
}

// applyAggregation implements the stated aggregation rule: the reported
// concentration is the arithmetic mean of the quantifiable replicates in linear
// space, provided enough of them exist. A linear mean is used rather than a
// geometric one because loads add downstream, and a load built from geometric
// means would not sum correctly at a confluence.
func applyAggregation(cfg config.Config, measurement *Measurement, linear, log10Values []float64) {
	quantification := cfg.Quantification
	if len(linear) > 0 {
		mean, err := numeric.Mean(linear)
		if err == nil {
			measurement.QuantifiedMean = numeric.RoundSignificant(mean, quantification.ReportSignificantDigits)
		}
	}
	switch {
	case measurement.QuantifiableReplicates >= quantification.MinQuantifiableReplicates &&
		measurement.QuantifiableReplicates > 0:
		measurement.Status = StatusQuantified
		measurement.CopiesPerLitre = measurement.QuantifiedMean
	case measurement.DetectedReplicates > 0:
		measurement.Status = StatusDetectedNotQuantified
		measurement.CopiesPerLitre = dnqSubstitute(quantification.DNQRule, *measurement)
		if measurement.QuantifiableReplicates > 0 {
			measurement.Notes = append(measurement.Notes, fmt.Sprintf(
				"only %d of %d required quantifiable replicate(s); substituted under rule %s",
				measurement.QuantifiableReplicates, quantification.MinQuantifiableReplicates,
				quantification.DNQRule))
		} else {
			measurement.Notes = append(measurement.Notes, fmt.Sprintf(
				"amplified in %d well(s) but none reached the limit of quantification; substituted under rule %s",
				measurement.DetectedReplicates, quantification.DNQRule))
		}
	default:
		measurement.Status = StatusNonDetect
		measurement.CopiesPerLitre = nonDetectSubstitute(quantification.NonDetectRule, *measurement)
		measurement.Notes = append(measurement.Notes,
			fmt.Sprintf("no amplification in any well; substituted under rule %s", quantification.NonDetectRule))
	}
	if len(log10Values) == 1 {
		measurement.Notes = append(measurement.Notes,
			"single quantifiable replicate; uncertainty uses the configured default spread")
	}
	if measurement.CopiesPerLitre > 0 {
		measurement.Log10CopiesPerLitre = numeric.Round4(math.Log10(measurement.CopiesPerLitre))
	}
}

func dnqSubstitute(rule config.DNQRule, measurement Measurement) float64 {
	switch rule {
	case config.DNQHalfLOQ:
		return measurement.LOQCopiesPerLitre / 2
	case config.DNQLOQ:
		return measurement.LOQCopiesPerLitre
	case config.DNQLOD:
		return measurement.LODCopiesPerLitre
	default:
		return 0
	}
}

func nonDetectSubstitute(rule config.NonDetectRule, measurement Measurement) float64 {
	switch rule {
	case config.NonDetectHalfLOD:
		return measurement.LODCopiesPerLitre / 2
	case config.NonDetectLOD:
		return measurement.LODCopiesPerLitre
	default:
		return 0
	}
}

// applyBand derives a coverage interval in log10 space and maps it back to
// copies per litre. With two or more quantifiable replicates the standard error
// of their log10 mean is used; with fewer, the configured single-replicate
// spread stands in, so a lone well never appears more precise than it is.
func applyBand(cfg config.Config, measurement *Measurement, log10Values []float64) {
	if measurement.CopiesPerLitre <= 0 {
		return
	}
	quantification := cfg.Quantification
	standardError := quantification.SingleReplicateLog10SD
	if len(log10Values) >= 2 {
		sd, err := numeric.StdDev(log10Values)
		if err == nil {
			standardError = sd / math.Sqrt(float64(len(log10Values)))
		}
		if standardError <= 0 {
			standardError = quantification.SingleReplicateLog10SD
		}
	}
	margin := quantification.UncertaintyCoverageK * standardError
	centre := math.Log10(measurement.CopiesPerLitre)
	digits := quantification.ReportSignificantDigits
	measurement.Log10StandardError = numeric.Round4(standardError)
	measurement.LowerCopiesPerLitre = numeric.RoundSignificant(math.Pow(10, centre-margin), digits)
	measurement.UpperCopiesPerLitre = numeric.RoundSignificant(math.Pow(10, centre+margin), digits)
}

// QuantifyBundle quantifies every result in the bundle. Results whose sample is
// missing, or whose sample matrix carries no concentration, are skipped and
// reported in the returned skip list rather than silently dropped.
func QuantifyBundle(cfg config.Config, bundle model.Bundle) ([]Measurement, []string, error) {
	samples := bundle.SampleByID()
	measurements := make([]Measurement, 0, len(bundle.Results))
	var skipped []string
	for _, result := range bundle.Results {
		sample, ok := samples[result.SampleID]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("%s: no sample %s", result.ResultID, result.SampleID))
			continue
		}
		if !sample.Matrix.Quantitative() {
			skipped = append(skipped, fmt.Sprintf("%s: matrix %s carries no concentration", result.ResultID, sample.Matrix))
			continue
		}
		measurement, err := Quantify(cfg, sample, result)
		if err != nil {
			return nil, nil, err
		}
		measurements = append(measurements, measurement)
	}
	SortMeasurements(measurements)
	sort.Strings(skipped)
	return measurements, skipped, nil
}

// SortMeasurements puts measurements into canonical order: site, then day, then
// target gene, then result identifier.
func SortMeasurements(measurements []Measurement) {
	sort.SliceStable(measurements, func(i, j int) bool {
		left, right := measurements[i], measurements[j]
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

// Index maps result identifiers to their measurement.
func Index(measurements []Measurement) map[string]Measurement {
	out := make(map[string]Measurement, len(measurements))
	for _, measurement := range measurements {
		out[measurement.ResultID] = measurement
	}
	return out
}
