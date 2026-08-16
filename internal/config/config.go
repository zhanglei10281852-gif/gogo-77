// Package config holds the tunable policy for a FluWatershed run.
//
// The configuration is a strict JSON document: an unknown member is an error,
// not a warning, so a renamed knob cannot silently fall back to a default. All
// numeric fields are required to be present and inside a stated range; the
// Default function returns a complete, valid document that can be written out
// and edited rather than reconstructed by hand.
package config

import (
	"fmt"
	"sort"
	"strings"

	"FluWatershed/internal/model"
	"FluWatershed/internal/strictjson"
)

// SchemaVersion is the only configuration schema this build understands.
const SchemaVersion = "fluwatershed-config/v1"

// NonDetectRule names the substitution applied to a non-detecting result when
// it is placed on a trend series.
type NonDetectRule string

// The available non-detect substitutions.
const (
	NonDetectZero    NonDetectRule = "zero"
	NonDetectHalfLOD NonDetectRule = "half_lod"
	NonDetectLOD     NonDetectRule = "lod"
)

// DNQRule names the substitution applied to a result that amplified but stayed
// below the limit of quantification.
type DNQRule string

// The available detected-not-quantifiable substitutions.
const (
	DNQZero    DNQRule = "zero"
	DNQHalfLOQ DNQRule = "half_loq"
	DNQLOQ     DNQRule = "loq"
	DNQLOD     DNQRule = "lod"
)

// Quantification governs the Ct-to-concentration conversion.
type Quantification struct {
	MinQuantifiableReplicates int           `json:"min_quantifiable_replicates"`
	NonDetectRule             NonDetectRule `json:"non_detect_rule"`
	DNQRule                   DNQRule       `json:"detected_not_quantifiable_rule"`
	UncertaintyCoverageK      float64       `json:"uncertainty_coverage_k"`
	SingleReplicateLog10SD    float64       `json:"single_replicate_log10_sd"`
	ReportSignificantDigits   int           `json:"report_significant_digits"`
	MinCopiesPerLitreFloor    float64       `json:"min_copies_per_litre_floor"`
	// PrimaryTargetGene names the gene a site's trend series is built from. When a
	// laboratory runs several targets on the same extract, averaging them would
	// blend quantities that respond differently, so one target carries the trend
	// and the others stay available for reporting. An empty value lets every
	// target contribute, which is only sensible when a single target is in use.
	PrimaryTargetGene string `json:"primary_target_gene"`
}

// Quality governs which results are allowed to influence a trend.
type Quality struct {
	ReplicateCtTolerance     float64            `json:"replicate_ct_tolerance"`
	MinCurveRSquared         float64            `json:"min_curve_r_squared"`
	CurveSlopeMin            float64            `json:"curve_slope_min"`
	CurveSlopeMax            float64            `json:"curve_slope_max"`
	NegativeControlMinCt     float64            `json:"negative_control_min_ct"`
	PositiveRecoveryMin      float64            `json:"positive_recovery_min"`
	PositiveRecoveryMax      float64            `json:"positive_recovery_max"`
	InhibitionMaxCtShift     float64            `json:"inhibition_max_ct_shift"`
	MaxHoldingHours          map[string]float64 `json:"max_holding_hours_by_matrix"`
	TransportTempMinC        float64            `json:"transport_temperature_min_c"`
	TransportTempMaxC        float64            `json:"transport_temperature_max_c"`
	MaxCustodyGapHours       float64            `json:"max_custody_gap_hours"`
	RequireCustodyContinuity bool               `json:"require_custody_continuity"`
}

// Normalisation governs flow, population and dilution correction.
type Normalisation struct {
	EnableFlowNormalisation       bool    `json:"enable_flow_normalisation"`
	EnablePopulationNormalisation bool    `json:"enable_population_normalisation"`
	EnableIndicatorCorrection     bool    `json:"enable_indicator_correction"`
	IndicatorCorrectionMin        float64 `json:"indicator_correction_min"`
	IndicatorCorrectionMax        float64 `json:"indicator_correction_max"`
	PerCapitaScale                float64 `json:"per_capita_scale"`
}

// Baseline governs the rolling reference each site is compared against.
type Baseline struct {
	WindowDays        int     `json:"window_days"`
	MinPoints         int     `json:"min_points"`
	ExcludeCurrentDay bool    `json:"exclude_current_day"`
	MinDispersion     float64 `json:"min_dispersion"`
}

// Detection governs exceedance, sustained-rise and slope tests.
type Detection struct {
	BaselineMultiple float64 `json:"baseline_multiple"`
	RobustZThreshold float64 `json:"robust_z_threshold"`
	// DefaultAbsoluteLoad applies to sites whose signal is a daily load, and
	// DefaultAbsoluteConcentration to sites still expressed as a concentration.
	// Two defaults are needed because the units differ by orders of magnitude and
	// a single number would be meaningless for one of them.
	DefaultAbsoluteLoad          float64 `json:"default_absolute_threshold_copies_per_day"`
	DefaultAbsoluteConcentration float64 `json:"default_absolute_threshold_copies_per_litre"`
	SustainedIntervals           int     `json:"sustained_intervals"`
	TrendWindowDays              int     `json:"trend_window_days"`
	MinTrendPoints               int     `json:"min_trend_points"`
	RisingSlopePerDay            float64 `json:"rising_slope_log10_per_day"`
	FallingSlopePerDay           float64 `json:"falling_slope_log10_per_day"`
	UpstreamTravelToleranceHr    float64 `json:"upstream_travel_tolerance_hours"`
}

// Corroboration governs how independent evidence streams combine.
type Corroboration struct {
	WastewaterWeight    float64 `json:"wastewater_weight"`
	WaterwayGrabWeight  float64 `json:"waterway_grab_weight"`
	CarcassWeight       float64 `json:"carcass_weight"`
	SedimentWeight      float64 `json:"sediment_weight"`
	CarcassRadiusKm     float64 `json:"carcass_radius_km"`
	CarcassWindowDays   int     `json:"carcass_window_days"`
	MinEvidenceStreams  int     `json:"min_evidence_streams"`
	LowGradeScore       float64 `json:"low_grade_score"`
	ModerateGradeScore  float64 `json:"moderate_grade_score"`
	HighGradeScore      float64 `json:"high_grade_score"`
	CarcassCountForFull int     `json:"carcass_count_for_full_weight"`
}

// Alerting governs the alert state machine.
type Alerting struct {
	OpenGrade           string  `json:"open_grade"`
	SustainAfterUpdates int     `json:"sustain_after_updates"`
	ResolveAfterUpdates int     `json:"resolve_after_updates"`
	CooldownHours       float64 `json:"cooldown_hours"`
	IdentifierBytes     int     `json:"identifier_bytes"`
}

// Config is the whole policy document.
type Config struct {
	SchemaVersion  string         `json:"schema_version"`
	ProfileID      string         `json:"profile_id"`
	Quantification Quantification `json:"quantification"`
	Quality        Quality        `json:"quality"`
	Normalisation  Normalisation  `json:"normalisation"`
	Baseline       Baseline       `json:"baseline"`
	Detection      Detection      `json:"detection"`
	Corroboration  Corroboration  `json:"corroboration"`
	Alerting       Alerting       `json:"alerting"`
}

// Default returns a complete configuration with values chosen to exercise
// every stage on the bundled fictional example without being trivially
// permissive.
func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		ProfileID:     "reference-profile",
		Quantification: Quantification{
			MinQuantifiableReplicates: 2,
			NonDetectRule:             NonDetectZero,
			DNQRule:                   DNQHalfLOQ,
			UncertaintyCoverageK:      1.96,
			SingleReplicateLog10SD:    0.25,
			ReportSignificantDigits:   6,
			MinCopiesPerLitreFloor:    1,
			PrimaryTargetGene:         "influenza_a_matrix_gene",
		},
		Quality: Quality{
			ReplicateCtTolerance: 1.5,
			MinCurveRSquared:     0.98,
			CurveSlopeMin:        -3.8,
			CurveSlopeMax:        -3.0,
			NegativeControlMinCt: 38,
			PositiveRecoveryMin:  0.5,
			PositiveRecoveryMax:  2.0,
			InhibitionMaxCtShift: 2.0,
			MaxHoldingHours: map[string]float64{
				string(model.MatrixWastewaterInfluent): 48,
				string(model.MatrixWaterwayGrab):       36,
				string(model.MatrixSediment):           72,
			},
			TransportTempMinC:        0,
			TransportTempMaxC:        10,
			MaxCustodyGapHours:       36,
			RequireCustodyContinuity: true,
		},
		Normalisation: Normalisation{
			EnableFlowNormalisation:       true,
			EnablePopulationNormalisation: true,
			EnableIndicatorCorrection:     true,
			IndicatorCorrectionMin:        0.25,
			IndicatorCorrectionMax:        4.0,
			PerCapitaScale:                1,
		},
		Baseline: Baseline{
			WindowDays:        21,
			MinPoints:         4,
			ExcludeCurrentDay: true,
			MinDispersion:     0.05,
		},
		Detection: Detection{
			BaselineMultiple:             3.0,
			RobustZThreshold:             3.0,
			DefaultAbsoluteLoad:          2.0e11,
			DefaultAbsoluteConcentration: 30000,
			SustainedIntervals:           2,
			TrendWindowDays:              21,
			MinTrendPoints:               4,
			RisingSlopePerDay:            0.02,
			FallingSlopePerDay:           -0.02,
			UpstreamTravelToleranceHr:    24,
		},
		Corroboration: Corroboration{
			WastewaterWeight:    0.45,
			WaterwayGrabWeight:  0.25,
			CarcassWeight:       0.2,
			SedimentWeight:      0.1,
			CarcassRadiusKm:     25,
			CarcassWindowDays:   14,
			MinEvidenceStreams:  2,
			LowGradeScore:       0.2,
			ModerateGradeScore:  0.45,
			HighGradeScore:      0.7,
			CarcassCountForFull: 25,
		},
		Alerting: Alerting{
			OpenGrade:           "moderate",
			SustainAfterUpdates: 2,
			ResolveAfterUpdates: 2,
			CooldownHours:       72,
			IdentifierBytes:     8,
		},
	}
}

// Load reads and validates a configuration document from disk.
func Load(path string) (Config, error) {
	var cfg Config
	if err := strictjson.File(path, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse reads and validates a configuration document held in memory.
func Parse(raw []byte) (Config, error) {
	var cfg Config
	if err := strictjson.Document(raw, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Encode renders the configuration as indented JSON.
func (c Config) Encode() ([]byte, error) { return strictjson.Indented(c) }

// HoldingLimitHours returns the holding-time limit for a matrix and whether one
// is configured. A matrix with no limit is never rejected on holding time.
func (c Config) HoldingLimitHours(matrix model.Matrix) (float64, bool) {
	limit, ok := c.Quality.MaxHoldingHours[string(matrix)]
	return limit, ok
}

// EvidenceWeight returns the corroboration weight for a matrix.
func (c Config) EvidenceWeight(matrix model.Matrix) float64 {
	switch matrix {
	case model.MatrixWastewaterInfluent:
		return c.Corroboration.WastewaterWeight
	case model.MatrixWaterwayGrab:
		return c.Corroboration.WaterwayGrabWeight
	case model.MatrixSediment:
		return c.Corroboration.SedimentWeight
	case model.MatrixCarcassEvent:
		return c.Corroboration.CarcassWeight
	default:
		return 0
	}
}

// TotalEvidenceWeight is the sum of the four stream weights, used to normalise
// a combined score into the unit interval.
func (c Config) TotalEvidenceWeight() float64 {
	return c.Corroboration.WastewaterWeight + c.Corroboration.WaterwayGrabWeight +
		c.Corroboration.SedimentWeight + c.Corroboration.CarcassWeight
}

// AbsoluteThresholdFor returns the absolute alarm level for a site in that
// site's own signal unit.
//
// A site-specific threshold always wins. Otherwise the unit decides: a site
// whose signal became a daily load takes the load default, and a site still
// carrying a concentration takes the concentration default. Getting this wrong
// in either direction would make the absolute test either useless or constantly
// triggered, so the choice mirrors exactly what the normalisation stage did.
func (c Config) AbsoluteThresholdFor(site model.Site) float64 {
	if site.AbsoluteThreshold > 0 {
		return site.AbsoluteThreshold
	}
	if c.Normalisation.EnableFlowNormalisation && site.Matrix.FlowNormalisable() && site.MeanDailyFlowM3 > 0 {
		return c.Detection.DefaultAbsoluteLoad
	}
	return c.Detection.DefaultAbsoluteConcentration
}

// Fingerprint is a stable, human-readable digest of the fields that change
// numeric output. It is stored beside computed signals so that a snapshot can
// be told apart from one produced under different policy.
func (c Config) Fingerprint() string {
	parts := []string{
		fmt.Sprintf("profile=%s", c.ProfileID),
		fmt.Sprintf("minrep=%d", c.Quantification.MinQuantifiableReplicates),
		fmt.Sprintf("nd=%s", c.Quantification.NonDetectRule),
		fmt.Sprintf("dnq=%s", c.Quantification.DNQRule),
		fmt.Sprintf("k=%.4f", c.Quantification.UncertaintyCoverageK),
		fmt.Sprintf("cttol=%.4f", c.Quality.ReplicateCtTolerance),
		fmt.Sprintf("window=%d", c.Baseline.WindowDays),
		fmt.Sprintf("minpts=%d", c.Baseline.MinPoints),
		fmt.Sprintf("mult=%.4f", c.Detection.BaselineMultiple),
		fmt.Sprintf("z=%.4f", c.Detection.RobustZThreshold),
		fmt.Sprintf("absload=%.4g", c.Detection.DefaultAbsoluteLoad),
		fmt.Sprintf("abscon=%.4g", c.Detection.DefaultAbsoluteConcentration),
		fmt.Sprintf("sustain=%d", c.Detection.SustainedIntervals),
		fmt.Sprintf("weights=%.4f/%.4f/%.4f/%.4f",
			c.Corroboration.WastewaterWeight, c.Corroboration.WaterwayGrabWeight,
			c.Corroboration.SedimentWeight, c.Corroboration.CarcassWeight),
		fmt.Sprintf("grades=%.4f/%.4f/%.4f",
			c.Corroboration.LowGradeScore, c.Corroboration.ModerateGradeScore,
			c.Corroboration.HighGradeScore),
		fmt.Sprintf("cooldown=%.4f", c.Alerting.CooldownHours),
	}
	matrices := make([]string, 0, len(c.Quality.MaxHoldingHours))
	for matrix, hours := range c.Quality.MaxHoldingHours {
		matrices = append(matrices, fmt.Sprintf("%s:%.2f", matrix, hours))
	}
	sort.Strings(matrices)
	parts = append(parts, "hold="+strings.Join(matrices, ","))
	return strings.Join(parts, ";")
}
