package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"FluWatershed/internal/model"
)

func TestDefaultProfileIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("the built-in profile does not validate: %v", err)
	}
}

func TestDefaultProfileRoundTripsThroughStrictJSON(t *testing.T) {
	encoded, err := Default().Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	reloaded, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if reloaded.Fingerprint() != Default().Fingerprint() {
		t.Fatal("a round trip changed the fingerprint")
	}
	again, err := reloaded.Encode()
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if string(again) != string(encoded) {
		t.Fatal("two encodings of the same profile differ")
	}
}

func TestParseRejectsUnknownMember(t *testing.T) {
	encoded, err := Default().Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	broken := strings.Replace(string(encoded), `"profile_id"`, `"profileId"`, 1)
	if _, err := Parse([]byte(broken)); err == nil {
		t.Fatal("a misspelled member was accepted")
	}
}

func TestLoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	encoded, err := Default().Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProfileID != "reference-profile" {
		t.Fatalf("profile id = %q", cfg.ProfileID)
	}
	if _, err := Load(filepath.Join(dir, "absent.json")); err == nil {
		t.Fatal("a missing profile was accepted")
	}
	invalid := strings.Replace(string(encoded), `"baseline_multiple": 3`, `"baseline_multiple": 0.5`, 1)
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte(invalid), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(badPath); err == nil {
		t.Fatal("an out of range value was accepted")
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"schema", func(c *Config) { c.SchemaVersion = "x" }, "schema_version"},
		{"profile id", func(c *Config) { c.ProfileID = "" }, "profile_id"},
		{"replicates", func(c *Config) { c.Quantification.MinQuantifiableReplicates = 0 }, "min_quantifiable_replicates"},
		{"non detect rule", func(c *Config) { c.Quantification.NonDetectRule = "guess" }, "non_detect_rule"},
		{"dnq rule", func(c *Config) { c.Quantification.DNQRule = "guess" }, "detected_not_quantifiable_rule"},
		{"coverage", func(c *Config) { c.Quantification.UncertaintyCoverageK = 0 }, "uncertainty_coverage_k"},
		{"single sd", func(c *Config) { c.Quantification.SingleReplicateLog10SD = 5 }, "single_replicate_log10_sd"},
		{"digits", func(c *Config) { c.Quantification.ReportSignificantDigits = 1 }, "report_significant_digits"},
		{"floor", func(c *Config) { c.Quantification.MinCopiesPerLitreFloor = 0 }, "min_copies_per_litre_floor"},
		{"gene spaces", func(c *Config) { c.Quantification.PrimaryTargetGene = " gene " }, "primary_target_gene"},
		{"ct tolerance", func(c *Config) { c.Quality.ReplicateCtTolerance = 0 }, "replicate_ct_tolerance"},
		{"r squared", func(c *Config) { c.Quality.MinCurveRSquared = 0.1 }, "min_curve_r_squared"},
		{"slope sign", func(c *Config) { c.Quality.CurveSlopeMin = 1 }, "must be negative"},
		{"slope order", func(c *Config) { c.Quality.CurveSlopeMin = -2; c.Quality.CurveSlopeMax = -3 }, "curve_slope_min"},
		{"negative control", func(c *Config) { c.Quality.NegativeControlMinCt = 0 }, "negative_control_min_ct"},
		{"recovery order", func(c *Config) { c.Quality.PositiveRecoveryMax = 0.1 }, "positive_recovery_max"},
		{"inhibition", func(c *Config) { c.Quality.InhibitionMaxCtShift = 0 }, "inhibition_max_ct_shift"},
		{"no holding limits", func(c *Config) { c.Quality.MaxHoldingHours = nil }, "max_holding_hours_by_matrix"},
		{"bad matrix key", func(c *Config) { c.Quality.MaxHoldingHours["mystery"] = 12 }, "not a recognised matrix"},
		{"bad holding value", func(c *Config) {
			c.Quality.MaxHoldingHours[string(model.MatrixSediment)] = 0
		}, "max_holding_hours_by_matrix"},
		{"temperature order", func(c *Config) { c.Quality.TransportTempMinC = 20 }, "transport_temperature_min_c"},
		{"temperature range", func(c *Config) { c.Quality.TransportTempMaxC = 90 }, "transport_temperature"},
		{"custody gap", func(c *Config) { c.Quality.MaxCustodyGapHours = 0 }, "max_custody_gap_hours"},
		{"indicator min", func(c *Config) { c.Normalisation.IndicatorCorrectionMin = 0 }, "indicator_correction_min"},
		{"indicator order", func(c *Config) { c.Normalisation.IndicatorCorrectionMax = 0.1 }, "indicator_correction_max"},
		{"indicator huge", func(c *Config) { c.Normalisation.IndicatorCorrectionMax = 500 }, "implausibly large"},
		{"per capita", func(c *Config) { c.Normalisation.PerCapitaScale = 0 }, "per_capita_scale"},
		{"population needs flow", func(c *Config) { c.Normalisation.EnableFlowNormalisation = false }, "needs flow normalisation"},
		{"window", func(c *Config) { c.Baseline.WindowDays = 1 }, "window_days"},
		{"min points", func(c *Config) { c.Baseline.MinPoints = 1 }, "min_points"},
		{"points over window", func(c *Config) { c.Baseline.MinPoints = 400 }, "min_points"},
		{"dispersion", func(c *Config) { c.Baseline.MinDispersion = -1 }, "min_dispersion"},
		{"multiple", func(c *Config) { c.Detection.BaselineMultiple = 1 }, "baseline_multiple"},
		{"z", func(c *Config) { c.Detection.RobustZThreshold = 0 }, "robust_z_threshold"},
		{"load threshold", func(c *Config) { c.Detection.DefaultAbsoluteLoad = 0 }, "copies_per_day"},
		{"concentration threshold", func(c *Config) { c.Detection.DefaultAbsoluteConcentration = 0 }, "copies_per_litre"},
		{"sustained", func(c *Config) { c.Detection.SustainedIntervals = 1 }, "sustained_intervals"},
		{"trend window", func(c *Config) { c.Detection.TrendWindowDays = 1 }, "trend_window_days"},
		{"trend points", func(c *Config) { c.Detection.MinTrendPoints = 2 }, "min_trend_points"},
		{"rising", func(c *Config) { c.Detection.RisingSlopePerDay = 0 }, "rising_slope"},
		{"falling", func(c *Config) { c.Detection.FallingSlopePerDay = 1 }, "falling_slope"},
		{"tolerance", func(c *Config) { c.Detection.UpstreamTravelToleranceHr = -1 }, "upstream_travel_tolerance_hours"},
		{"weight range", func(c *Config) { c.Corroboration.CarcassWeight = 2 }, "carcass_weight"},
		{"weights zero", func(c *Config) {
			c.Corroboration.WastewaterWeight = 0
			c.Corroboration.WaterwayGrabWeight = 0
			c.Corroboration.SedimentWeight = 0
			c.Corroboration.CarcassWeight = 0
		}, "at least one stream"},
		{"weights over one", func(c *Config) { c.Corroboration.WastewaterWeight = 0.9 }, "must not exceed 1"},
		{"radius", func(c *Config) { c.Corroboration.CarcassRadiusKm = 0 }, "carcass_radius_km"},
		{"carcass window", func(c *Config) { c.Corroboration.CarcassWindowDays = 0 }, "carcass_window_days"},
		{"min streams", func(c *Config) { c.Corroboration.MinEvidenceStreams = 9 }, "min_evidence_streams"},
		{"saturation", func(c *Config) { c.Corroboration.CarcassCountForFull = 0 }, "carcass_count_for_full_weight"},
		{"low grade", func(c *Config) { c.Corroboration.LowGradeScore = 0.9 }, "low_grade_score"},
		{"moderate grade", func(c *Config) { c.Corroboration.ModerateGradeScore = 0.95 }, "moderate_grade_score"},
		{"high grade", func(c *Config) { c.Corroboration.HighGradeScore = 1.5 }, "high_grade_score"},
		{"open grade", func(c *Config) { c.Alerting.OpenGrade = "extreme" }, "open_grade"},
		{"sustain", func(c *Config) { c.Alerting.SustainAfterUpdates = 0 }, "sustain_after_updates"},
		{"resolve", func(c *Config) { c.Alerting.ResolveAfterUpdates = 0 }, "resolve_after_updates"},
		{"cooldown", func(c *Config) { c.Alerting.CooldownHours = -1 }, "cooldown_hours"},
		{"identifier", func(c *Config) { c.Alerting.IdentifierBytes = 2 }, "identifier_bytes"},
	}
	for _, item := range cases {
		cfg := Default()
		item.mutate(&cfg)
		err := cfg.Validate()
		if err == nil {
			t.Errorf("%s: the change was accepted", item.name)
			continue
		}
		if !strings.Contains(err.Error(), item.want) {
			t.Errorf("%s: error %v does not mention %q", item.name, err, item.want)
		}
	}
}

func TestHoldingLimitLookup(t *testing.T) {
	cfg := Default()
	if hours, ok := cfg.HoldingLimitHours(model.MatrixWastewaterInfluent); !ok || hours != 48 {
		t.Fatalf("influent limit = %v ok=%v", hours, ok)
	}
	if _, ok := cfg.HoldingLimitHours(model.MatrixCarcassEvent); ok {
		t.Fatal("a carcass event should carry no holding limit")
	}
}

func TestEvidenceWeights(t *testing.T) {
	cfg := Default()
	if cfg.EvidenceWeight(model.MatrixWastewaterInfluent) != cfg.Corroboration.WastewaterWeight {
		t.Error("influent weight mismatch")
	}
	if cfg.EvidenceWeight(model.MatrixWaterwayGrab) != cfg.Corroboration.WaterwayGrabWeight {
		t.Error("grab weight mismatch")
	}
	if cfg.EvidenceWeight(model.MatrixSediment) != cfg.Corroboration.SedimentWeight {
		t.Error("sediment weight mismatch")
	}
	if cfg.EvidenceWeight(model.MatrixCarcassEvent) != cfg.Corroboration.CarcassWeight {
		t.Error("carcass weight mismatch")
	}
	if cfg.EvidenceWeight("mystery") != 0 {
		t.Error("an unknown matrix carried weight")
	}
	total := cfg.TotalEvidenceWeight()
	if total <= 0 || total > 1.0001 {
		t.Fatalf("total weight = %v", total)
	}
}

func TestAbsoluteThresholdChoosesTheRightUnit(t *testing.T) {
	cfg := Default()
	influent := model.Site{SiteID: "WW", Matrix: model.MatrixWastewaterInfluent, MeanDailyFlowM3: 5000}
	grab := model.Site{SiteID: "RIV", Matrix: model.MatrixWaterwayGrab}
	if got := cfg.AbsoluteThresholdFor(influent); got != cfg.Detection.DefaultAbsoluteLoad {
		t.Fatalf("influent threshold = %v, want the load default", got)
	}
	if got := cfg.AbsoluteThresholdFor(grab); got != cfg.Detection.DefaultAbsoluteConcentration {
		t.Fatalf("grab threshold = %v, want the concentration default", got)
	}
	// Without flow normalisation an influent site stays on a concentration.
	noFlow := cfg
	noFlow.Normalisation.EnableFlowNormalisation = false
	noFlow.Normalisation.EnablePopulationNormalisation = false
	if got := noFlow.AbsoluteThresholdFor(influent); got != cfg.Detection.DefaultAbsoluteConcentration {
		t.Fatalf("threshold without flow normalisation = %v", got)
	}
	// A site specific threshold always wins.
	influent.AbsoluteThreshold = 42
	if got := cfg.AbsoluteThresholdFor(influent); got != 42 {
		t.Fatalf("site override ignored, got %v", got)
	}
}

func TestFingerprintReactsToPolicyChanges(t *testing.T) {
	base := Default()
	if base.Fingerprint() != Default().Fingerprint() {
		t.Fatal("the fingerprint is not stable across two identical profiles")
	}
	changed := Default()
	changed.Detection.BaselineMultiple = 4
	if changed.Fingerprint() == base.Fingerprint() {
		t.Fatal("a changed threshold left the fingerprint untouched")
	}
	reordered := Default()
	reordered.Quality.MaxHoldingHours = map[string]float64{
		string(model.MatrixSediment):           72,
		string(model.MatrixWaterwayGrab):       36,
		string(model.MatrixWastewaterInfluent): 48,
	}
	if reordered.Fingerprint() != base.Fingerprint() {
		t.Fatal("map iteration order leaked into the fingerprint")
	}
}

func TestDescribeCoversEveryStage(t *testing.T) {
	lines := Default().Describe()
	if len(lines) < 10 {
		t.Fatalf("description is too thin: %v", lines)
	}
	joined := strings.Join(lines, "\n")
	for _, needle := range []string{"profile", "baseline", "cooldown", "carcass", "absolute thresholds"} {
		if !strings.Contains(joined, needle) {
			t.Errorf("description does not mention %q", needle)
		}
	}
	if got := AlertGrades(); strings.Join(got, ",") != "low,moderate,high" {
		t.Fatalf("alert grades = %v", got)
	}
}
