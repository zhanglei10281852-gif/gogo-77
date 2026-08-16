package quant

import (
	"math"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/timeutil"
)

// The fixtures below use a curve of Ct = -3.32*log10(copies) + 38.5, which puts
// exactly one copy per reaction at Ct 38.5 and multiplies the copy number by ten
// for every 3.32 cycles earlier. That makes the expected numbers exact rather
// than approximate, so the test can assert equality on the arithmetic.
const (
	testSlope     = -3.32
	testIntercept = 38.5
)

func ctFor(copiesPerReaction float64) float64 {
	return testSlope*math.Log10(copiesPerReaction) + testIntercept
}

func ptr(v float64) *float64 { return &v }

func testCurve() model.StandardCurve {
	return model.StandardCurve{
		Slope:                testSlope,
		Intercept:            testIntercept,
		RSquared:             0.995,
		LODCopiesPerReaction: 5,
		LOQCopiesPerReaction: 20,
	}
}

// testSample has a volume of 200 mL and the result below extracts into 100 uL
// with 5 uL going into each reaction at a recovery of 0.5, so the conversion
// factor is 1 * (100/5) / 0.2 L / 0.5 = 200 copies per litre for every copy per
// reaction.
func testSample() model.Sample {
	return model.Sample{
		SampleID:    "SMP-1",
		SiteID:      "SITE-1",
		Matrix:      model.MatrixWastewaterInfluent,
		CollectedAt: timeutil.MustParse("2026-03-14T08:00:00Z"),
		VolumeML:    200,
	}
}

func testResult(replicates []model.Replicate) model.Result {
	return model.Result{
		ResultID:         "RES-1",
		SampleID:         "SMP-1",
		TargetGene:       "influenza_a_matrix_gene",
		AnalysedAt:       timeutil.MustParse("2026-03-15T10:00:00Z"),
		Replicates:       replicates,
		DilutionFactor:   1,
		ExtractVolumeUL:  100,
		TemplateVolumeUL: 5,
		RecoveryFraction: 0.5,
		Curve:            testCurve(),
		Controls: model.Controls{
			PositiveControlExpected: 10000,
			PositiveControlObserved: 9500,
			InhibitionControlCt:     ptr(24.5),
			InhibitionReferenceCt:   24.4,
		},
	}
}

func TestCopiesPerReactionInvertsTheCurve(t *testing.T) {
	curve := testCurve()
	cases := []struct {
		ct   float64
		want float64
	}{
		{38.5, 1},
		{38.5 - 3.32, 10},
		{38.5 - 6.64, 100},
		{38.5 - 9.96, 1000},
	}
	for _, item := range cases {
		got, err := CopiesPerReaction(item.ct, curve)
		if err != nil {
			t.Fatalf("CopiesPerReaction(%v): %v", item.ct, err)
		}
		if math.Abs(got-item.want) > item.want*1e-9 {
			t.Errorf("CopiesPerReaction(%v) = %v, want %v", item.ct, got, item.want)
		}
	}
	if _, err := CopiesPerReaction(30, model.StandardCurve{}); err == nil {
		t.Error("a zero slope was accepted")
	}
	if _, err := CopiesPerReaction(-500, curve); err == nil {
		t.Error("an absurdly low Ct did not overflow guard")
	}
}

func TestConversionFactor(t *testing.T) {
	factor, err := ConversionFactor(testResult(nil), testSample())
	if err != nil {
		t.Fatalf("ConversionFactor: %v", err)
	}
	if math.Abs(factor-200) > 1e-9 {
		t.Fatalf("factor = %v, want 200", factor)
	}
	// Doubling the dilution doubles the reported concentration.
	diluted := testResult(nil)
	diluted.DilutionFactor = 2
	factor, err = ConversionFactor(diluted, testSample())
	if err != nil {
		t.Fatalf("ConversionFactor: %v", err)
	}
	if math.Abs(factor-400) > 1e-9 {
		t.Fatalf("diluted factor = %v, want 400", factor)
	}
	// Halving recovery doubles it too, because half the material was lost.
	poor := testResult(nil)
	poor.RecoveryFraction = 0.25
	factor, err = ConversionFactor(poor, testSample())
	if err != nil {
		t.Fatalf("ConversionFactor: %v", err)
	}
	if math.Abs(factor-400) > 1e-9 {
		t.Fatalf("low recovery factor = %v, want 400", factor)
	}
	bad := testSample()
	bad.VolumeML = 0
	if _, err := ConversionFactor(testResult(nil), bad); err == nil {
		t.Error("a zero sample volume was accepted")
	}
}

func TestQuantifyQuantifiedReplicates(t *testing.T) {
	cfg := config.Default()
	// One hundred copies per reaction is above the limit of quantification of 20,
	// and 100 * 200 = 20000 copies per litre.
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(100))},
		{Well: "A3", Ct: ptr(ctFor(100))},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusQuantified {
		t.Fatalf("status = %s", measurement.Status)
	}
	if math.Abs(measurement.CopiesPerLitre-20000) > 0.01 {
		t.Fatalf("copies per litre = %v, want 20000", measurement.CopiesPerLitre)
	}
	if measurement.QuantifiableReplicates != 3 || measurement.DetectedReplicates != 3 {
		t.Fatalf("replicate counts = %d/%d", measurement.QuantifiableReplicates, measurement.DetectedReplicates)
	}
	if math.Abs(measurement.LOQCopiesPerLitre-4000) > 0.01 {
		t.Fatalf("LOQ per litre = %v, want 4000", measurement.LOQCopiesPerLitre)
	}
	if math.Abs(measurement.LODCopiesPerLitre-1000) > 0.01 {
		t.Fatalf("LOD per litre = %v, want 1000", measurement.LODCopiesPerLitre)
	}
	if math.Abs(measurement.Log10CopiesPerLitre-math.Log10(20000)) > 1e-4 {
		t.Fatalf("log10 = %v", measurement.Log10CopiesPerLitre)
	}
	if measurement.CtSpread != 0 {
		t.Fatalf("identical replicates gave a Ct spread of %v", measurement.CtSpread)
	}
}

func TestQuantifyAveragesOnlyQuantifiableReplicates(t *testing.T) {
	cfg := config.Default()
	// 100 and 200 copies per reaction are quantifiable and average to 150, which
	// is 30000 copies per litre. The third well at 10 copies is below the limit of
	// quantification and must not drag the mean down.
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(200))},
		{Well: "A3", Ct: ptr(ctFor(10))},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusQuantified {
		t.Fatalf("status = %s", measurement.Status)
	}
	if measurement.QuantifiableReplicates != 2 {
		t.Fatalf("quantifiable = %d, want 2", measurement.QuantifiableReplicates)
	}
	if measurement.DetectedReplicates != 3 {
		t.Fatalf("detected = %d, want 3", measurement.DetectedReplicates)
	}
	if math.Abs(measurement.CopiesPerLitre-30000) > 0.05 {
		t.Fatalf("copies per litre = %v, want 30000", measurement.CopiesPerLitre)
	}
	if math.Abs(measurement.QuantifiedMean-30000) > 0.05 {
		t.Fatalf("quantifiable mean = %v", measurement.QuantifiedMean)
	}
	if measurement.Replicates[2].Class != ClassBelowLOQ {
		t.Fatalf("third well class = %s", measurement.Replicates[2].Class)
	}
	if measurement.Replicates[2].BelowLOD {
		t.Fatal("ten copies per reaction sits above the limit of detection of five")
	}
	faint, err := Quantify(config.Default(), testSample(), testResult([]model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(2))},
		{Well: "A2", Ct: nil},
	}))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if !faint.Replicates[0].BelowLOD {
		t.Fatal("two copies per reaction sits below the limit of detection of five")
	}
}

func TestQuantifyBelowLimitOfQuantification(t *testing.T) {
	cfg := config.Default()
	if cfg.Quantification.DNQRule != config.DNQHalfLOQ {
		t.Fatalf("test assumes the half-LOQ rule, got %s", cfg.Quantification.DNQRule)
	}
	replicates := []model.Replicate{
		{Well: "A1", Ct: nil},
		{Well: "A2", Ct: ptr(ctFor(9))},
		{Well: "A3", Ct: nil},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusDetectedNotQuantified {
		t.Fatalf("status = %s", measurement.Status)
	}
	if !measurement.Detected {
		t.Fatal("a well amplified but the measurement is not marked detected")
	}
	// Half of the limit of quantification, 4000 copies per litre, is 2000.
	if math.Abs(measurement.CopiesPerLitre-2000) > 0.01 {
		t.Fatalf("substituted value = %v, want 2000", measurement.CopiesPerLitre)
	}
	if len(measurement.Notes) == 0 {
		t.Fatal("a substitution should be explained in the notes")
	}
}

func TestQuantifyNonDetect(t *testing.T) {
	cfg := config.Default()
	replicates := []model.Replicate{
		{Well: "A1", Ct: nil},
		{Well: "A2", Ct: nil},
		{Well: "A3", Ct: nil},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusNonDetect {
		t.Fatalf("status = %s", measurement.Status)
	}
	if measurement.Detected {
		t.Fatal("no well amplified but the measurement is marked detected")
	}
	if measurement.CopiesPerLitre != 0 {
		t.Fatalf("the zero rule should give 0, got %v", measurement.CopiesPerLitre)
	}
	if measurement.LowerCopiesPerLitre != 0 || measurement.UpperCopiesPerLitre != 0 {
		t.Fatal("a non-detect should carry no coverage band")
	}
}

func TestSubstitutionRulesAreHonoured(t *testing.T) {
	replicates := []model.Replicate{{Well: "A1", Ct: nil}, {Well: "A2", Ct: nil}}
	cases := []struct {
		rule config.NonDetectRule
		want float64
	}{
		{config.NonDetectZero, 0},
		{config.NonDetectHalfLOD, 500},
		{config.NonDetectLOD, 1000},
	}
	for _, item := range cases {
		cfg := config.Default()
		cfg.Quantification.NonDetectRule = item.rule
		measurement, err := Quantify(cfg, testSample(), testResult(replicates))
		if err != nil {
			t.Fatalf("Quantify with %s: %v", item.rule, err)
		}
		if math.Abs(measurement.CopiesPerLitre-item.want) > 0.01 {
			t.Errorf("rule %s gave %v, want %v", item.rule, measurement.CopiesPerLitre, item.want)
		}
	}
	dnqCases := []struct {
		rule config.DNQRule
		want float64
	}{
		{config.DNQZero, 0},
		{config.DNQHalfLOQ, 2000},
		{config.DNQLOQ, 4000},
		{config.DNQLOD, 1000},
	}
	detected := []model.Replicate{{Well: "A1", Ct: ptr(ctFor(9))}, {Well: "A2", Ct: nil}}
	for _, item := range dnqCases {
		cfg := config.Default()
		cfg.Quantification.DNQRule = item.rule
		measurement, err := Quantify(cfg, testSample(), testResult(detected))
		if err != nil {
			t.Fatalf("Quantify with %s: %v", item.rule, err)
		}
		if math.Abs(measurement.CopiesPerLitre-item.want) > 0.01 {
			t.Errorf("rule %s gave %v, want %v", item.rule, measurement.CopiesPerLitre, item.want)
		}
	}
}

func TestMinimumQuantifiableReplicatesForcesSubstitution(t *testing.T) {
	cfg := config.Default()
	cfg.Quantification.MinQuantifiableReplicates = 3
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(100))},
		{Well: "A3", Ct: nil},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusDetectedNotQuantified {
		t.Fatalf("status = %s, want a substitution", measurement.Status)
	}
	if math.Abs(measurement.QuantifiedMean-20000) > 0.05 {
		t.Fatalf("the quantifiable mean should still be reported, got %v", measurement.QuantifiedMean)
	}
	if math.Abs(measurement.CopiesPerLitre-2000) > 0.01 {
		t.Fatalf("reported value = %v, want the half-LOQ substitution", measurement.CopiesPerLitre)
	}
}

func TestCoverageBandWidensWithReplicateSpread(t *testing.T) {
	cfg := config.Default()
	tight := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(105))},
		{Well: "A3", Ct: ptr(ctFor(95))},
	}
	wide := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(400))},
		{Well: "A3", Ct: ptr(ctFor(25))},
	}
	tightMeasurement, err := Quantify(cfg, testSample(), testResult(tight))
	if err != nil {
		t.Fatalf("tight: %v", err)
	}
	wideMeasurement, err := Quantify(cfg, testSample(), testResult(wide))
	if err != nil {
		t.Fatalf("wide: %v", err)
	}
	tightRatio := tightMeasurement.UpperCopiesPerLitre / tightMeasurement.LowerCopiesPerLitre
	wideRatio := wideMeasurement.UpperCopiesPerLitre / wideMeasurement.LowerCopiesPerLitre
	if wideRatio <= tightRatio {
		t.Fatalf("spread did not widen the band: tight %v, wide %v", tightRatio, wideRatio)
	}
	if tightMeasurement.LowerCopiesPerLitre >= tightMeasurement.CopiesPerLitre {
		t.Fatal("the lower bound is not below the estimate")
	}
	if tightMeasurement.UpperCopiesPerLitre <= tightMeasurement.CopiesPerLitre {
		t.Fatal("the upper bound is not above the estimate")
	}
}

func TestSingleQuantifiableReplicateUsesConfiguredSpread(t *testing.T) {
	cfg := config.Default()
	cfg.Quantification.MinQuantifiableReplicates = 1
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: nil},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if measurement.Status != StatusQuantified {
		t.Fatalf("status = %s", measurement.Status)
	}
	if math.Abs(measurement.Log10StandardError-cfg.Quantification.SingleReplicateLog10SD) > 1e-9 {
		t.Fatalf("standard error = %v, want the configured %v",
			measurement.Log10StandardError, cfg.Quantification.SingleReplicateLog10SD)
	}
	want := 20000 * math.Pow(10, -cfg.Quantification.UncertaintyCoverageK*cfg.Quantification.SingleReplicateLog10SD)
	if math.Abs(measurement.LowerCopiesPerLitre-want) > want*1e-5 {
		t.Fatalf("lower bound = %v, want about %v", measurement.LowerCopiesPerLitre, want)
	}
}

func TestCtSpreadIsReported(t *testing.T) {
	cfg := config.Default()
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(30.0)},
		{Well: "A2", Ct: ptr(32.4)},
		{Well: "A3", Ct: ptr(31.1)},
	}
	measurement, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	if math.Abs(measurement.CtSpread-2.4) > 1e-9 {
		t.Fatalf("Ct spread = %v, want 2.4", measurement.CtSpread)
	}
}

func TestQuantifyBundleSkipsUnmeasurableMatrices(t *testing.T) {
	cfg := config.Default()
	sample := testSample()
	carcass := model.Sample{
		SampleID:    "SMP-2",
		SiteID:      "SITE-2",
		Matrix:      model.MatrixCarcassEvent,
		CollectedAt: timeutil.MustParse("2026-03-14T08:00:00Z"),
	}
	orphan := testResult(nil)
	orphan.ResultID = "RES-ORPHAN"
	orphan.SampleID = "SMP-MISSING"
	carcassResult := testResult(nil)
	carcassResult.ResultID = "RES-CARCASS"
	carcassResult.SampleID = "SMP-2"
	good := testResult([]model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(100))},
	})
	bundle := model.Bundle{
		Samples: []model.Sample{sample, carcass},
		Results: []model.Result{good, orphan, carcassResult},
	}
	measurements, skipped, err := QuantifyBundle(cfg, bundle)
	if err != nil {
		t.Fatalf("QuantifyBundle: %v", err)
	}
	if len(measurements) != 1 {
		t.Fatalf("measurements = %d, want 1", len(measurements))
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %v, want two entries", skipped)
	}
	if index := Index(measurements); index["RES-1"].SampleID != "SMP-1" {
		t.Fatalf("index lookup failed: %+v", index)
	}
}

func TestQuantifyIsOrderIndependent(t *testing.T) {
	cfg := config.Default()
	replicates := []model.Replicate{
		{Well: "A1", Ct: ptr(ctFor(100))},
		{Well: "A2", Ct: ptr(ctFor(250))},
		{Well: "A3", Ct: ptr(ctFor(40))},
	}
	reversed := []model.Replicate{replicates[2], replicates[1], replicates[0]}
	first, err := Quantify(cfg, testSample(), testResult(replicates))
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	second, err := Quantify(cfg, testSample(), testResult(reversed))
	if err != nil {
		t.Fatalf("reversed: %v", err)
	}
	if first.CopiesPerLitre != second.CopiesPerLitre {
		t.Fatalf("well order changed the answer: %v and %v", first.CopiesPerLitre, second.CopiesPerLitre)
	}
	if first.LowerCopiesPerLitre != second.LowerCopiesPerLitre {
		t.Fatal("well order changed the coverage band")
	}
}
