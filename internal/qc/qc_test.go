package qc

import (
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/timeutil"
)

func ptr(v float64) *float64 { return &v }

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

// cleanSample and cleanResult pass every rule. Each test perturbs one field and
// asserts that exactly the matching rule fails, which keeps the rules from
// quietly overlapping.
func cleanSample() model.Sample {
	cold := 4.0
	return model.Sample{
		SampleID:       "SMP-1",
		SiteID:         "SITE-1",
		Matrix:         model.MatrixWastewaterInfluent,
		CollectedAt:    stamp("2026-03-14T08:00:00Z"),
		VolumeML:       200,
		TransportTempC: 4,
		Custody: []model.CustodyStep{
			{Action: model.CustodyCollected, Holder: "crew", At: stamp("2026-03-14T08:00:00Z"), TemperatureC: &cold},
			{Action: model.CustodyTransferred, Holder: "courier", At: stamp("2026-03-14T12:00:00Z"), TemperatureC: &cold},
			{Action: model.CustodyReceived, Holder: "bench", At: stamp("2026-03-14T15:00:00Z"), TemperatureC: &cold},
			{Action: model.CustodyExtracted, Holder: "bench", At: stamp("2026-03-15T06:00:00Z")},
		},
	}
}

func cleanResult() model.Result {
	return model.Result{
		ResultID:         "RES-1",
		SampleID:         "SMP-1",
		TargetGene:       "influenza_a_matrix_gene",
		AnalysedAt:       stamp("2026-03-15T10:00:00Z"),
		Replicates:       []model.Replicate{{Well: "A1", Ct: ptr(31.5)}, {Well: "A2", Ct: ptr(31.9)}},
		DilutionFactor:   1,
		ExtractVolumeUL:  100,
		TemplateVolumeUL: 5,
		RecoveryFraction: 0.6,
		Curve: model.StandardCurve{
			Slope: -3.32, Intercept: 38.5, RSquared: 0.995,
			LODCopiesPerReaction: 5, LOQCopiesPerReaction: 20,
		},
		Controls: model.Controls{
			PositiveControlExpected: 10000,
			PositiveControlObserved: 9500,
			InhibitionControlCt:     ptr(24.6),
			InhibitionReferenceCt:   24.4,
		},
	}
}

func assess(t *testing.T, sample model.Sample, result model.Result) Assessment {
	t.Helper()
	cfg := config.Default()
	measurement, err := quant.Quantify(cfg, sample, result)
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	return Assess(cfg, sample, result, measurement)
}

func failedOnly(t *testing.T, assessment Assessment, name string) {
	t.Helper()
	if assessment.Verdict != Fail {
		t.Fatalf("verdict = %s, want fail (failed: %v)", assessment.Verdict, assessment.Failed)
	}
	if len(assessment.Failed) != 1 || assessment.Failed[0] != name {
		t.Fatalf("failed checks = %v, want only %s", assessment.Failed, name)
	}
	if assessment.UsableForTrend {
		t.Fatal("a failing result was left usable for trends")
	}
}

func TestCleanResultPasses(t *testing.T) {
	assessment := assess(t, cleanSample(), cleanResult())
	if assessment.Verdict != Pass {
		t.Fatalf("verdict = %s, failed %v, warned %v",
			assessment.Verdict, assessment.Failed, assessment.Warned)
	}
	if !assessment.UsableForTrend {
		t.Fatal("a clean result was barred from trends")
	}
	if len(assessment.Checks) != len(CheckNames()) {
		t.Fatalf("checks = %d, want %d", len(assessment.Checks), len(CheckNames()))
	}
	for index := 1; index < len(assessment.Checks); index++ {
		if assessment.Checks[index-1].Name > assessment.Checks[index].Name {
			t.Fatalf("checks are not in name order: %v", assessment.Checks)
		}
	}
	for _, check := range assessment.Checks {
		if check.Detail == "" {
			t.Errorf("check %s carries no explanation", check.Name)
		}
	}
}

func TestReplicateAgreement(t *testing.T) {
	result := cleanResult()
	result.Replicates = []model.Replicate{
		{Well: "A1", Ct: ptr(30.0)}, {Well: "A2", Ct: ptr(33.0)},
	}
	failedOnly(t, assess(t, cleanSample(), result), CheckReplicateAgreement)

	// A single amplifying well cannot be compared with anything, so it warns.
	lonely := cleanResult()
	lonely.Replicates = []model.Replicate{{Well: "A1", Ct: ptr(31.5)}, {Well: "A2", Ct: nil}}
	assessment := assess(t, cleanSample(), lonely)
	if assessment.Verdict != Warn {
		t.Fatalf("verdict = %s, want warn", assessment.Verdict)
	}
	if !assessment.UsableForTrend {
		t.Fatal("a warning should not bar a result from trends")
	}

	// No amplification at all is not an agreement question.
	silent := cleanResult()
	silent.Replicates = []model.Replicate{{Well: "A1", Ct: nil}, {Well: "A2", Ct: nil}}
	if got := assess(t, cleanSample(), silent); got.Verdict != Pass {
		t.Fatalf("a non-detect gave verdict %s (%v)", got.Verdict, got.Failed)
	}
}

func TestStandardCurveChecks(t *testing.T) {
	poorFit := cleanResult()
	poorFit.Curve.RSquared = 0.90
	failedOnly(t, assess(t, cleanSample(), poorFit), CheckStandardCurve)

	shallow := cleanResult()
	shallow.Curve.Slope = -2.4
	failedOnly(t, assess(t, cleanSample(), shallow), CheckStandardCurve)

	steep := cleanResult()
	steep.Curve.Slope = -4.5
	failedOnly(t, assess(t, cleanSample(), steep), CheckStandardCurve)
}

func TestNegativeControlChecks(t *testing.T) {
	contaminated := cleanResult()
	contaminated.Controls.NegativeControlCt = ptr(30.8)
	failedOnly(t, assess(t, cleanSample(), contaminated), CheckNegativeControl)

	blank := cleanResult()
	blank.Controls.ExtractionBlankDetected = true
	failedOnly(t, assess(t, cleanSample(), blank), CheckNegativeControl)

	// Amplification later than the contamination limit is acceptable.
	late := cleanResult()
	late.Controls.NegativeControlCt = ptr(39.5)
	if got := assess(t, cleanSample(), late); got.Verdict != Pass {
		t.Fatalf("a late no-template control failed: %v", got.Failed)
	}
}

func TestPositiveControlRecovery(t *testing.T) {
	low := cleanResult()
	low.Controls.PositiveControlObserved = 2000
	failedOnly(t, assess(t, cleanSample(), low), CheckPositiveControl)

	high := cleanResult()
	high.Controls.PositiveControlObserved = 40000
	failedOnly(t, assess(t, cleanSample(), high), CheckPositiveControl)
}

func TestInhibitionChecks(t *testing.T) {
	inhibited := cleanResult()
	inhibited.Controls.InhibitionControlCt = ptr(28.9)
	failedOnly(t, assess(t, cleanSample(), inhibited), CheckInhibition)

	absent := cleanResult()
	absent.Controls.InhibitionControlCt = nil
	failedOnly(t, assess(t, cleanSample(), absent), CheckInhibition)

	early := cleanResult()
	early.Controls.InhibitionControlCt = ptr(21.0)
	assessment := assess(t, cleanSample(), early)
	if assessment.Verdict != Warn {
		t.Fatalf("verdict = %s, want warn", assessment.Verdict)
	}
	if len(assessment.Warned) != 1 || assessment.Warned[0] != CheckInhibition {
		t.Fatalf("warned = %v", assessment.Warned)
	}
}

func TestHoldingTimeChecks(t *testing.T) {
	late := cleanResult()
	late.AnalysedAt = stamp("2026-03-17T10:00:00Z") // 74 hours against a 48 hour limit
	sample := cleanSample()
	sample.Custody = append(sample.Custody, model.CustodyStep{
		Action: model.CustodyStored, Holder: "bench", At: stamp("2026-03-16T10:00:00Z"),
	})
	failedOnly(t, assess(t, sample, late), CheckHoldingTime)

	backwards := cleanResult()
	backwards.AnalysedAt = stamp("2026-03-13T10:00:00Z")
	assessment := assess(t, cleanSample(), backwards)
	if assessment.Verdict != Fail {
		t.Fatalf("analysis before collection was accepted: %v", assessment)
	}

	// Past three quarters of the limit but still inside it warns.
	nearLimit := cleanResult()
	nearLimit.AnalysedAt = stamp("2026-03-16T00:00:00Z") // 40 of 48 hours
	warned := assess(t, cleanSample(), nearLimit)
	if warned.Verdict != Warn {
		t.Fatalf("verdict = %s, want warn (failed %v)", warned.Verdict, warned.Failed)
	}

	// A matrix with no configured limit is never rejected on holding time.
	cfg := config.Default()
	delete(cfg.Quality.MaxHoldingHours, string(model.MatrixWastewaterInfluent))
	measurement, err := quant.Quantify(cfg, cleanSample(), late)
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	relaxed := Assess(cfg, cleanSample(), late, measurement)
	for _, check := range relaxed.Checks {
		if check.Name == CheckHoldingTime && check.Severity != Pass {
			t.Fatalf("an unconfigured matrix was still judged: %s", check.Detail)
		}
	}
}

func TestTransportTemperatureChecks(t *testing.T) {
	warm := cleanSample()
	warm.TransportTempC = 14.6
	failedOnly(t, assess(t, warm, cleanResult()), CheckTransportTemp)

	frozen := cleanSample()
	hot := 21.0
	frozen.Custody[1].TemperatureC = &hot
	assessment := assess(t, frozen, cleanResult())
	failedOnly(t, assessment, CheckTransportTemp)
	for _, check := range assessment.Checks {
		if check.Name == CheckTransportTemp && check.Observed != 21 {
			t.Fatalf("the worst reading should be reported, got %v", check.Observed)
		}
	}
}

func TestCustodyChecks(t *testing.T) {
	empty := cleanSample()
	empty.Custody = nil
	failedOnly(t, assess(t, empty, cleanResult()), CheckCustody)

	outOfOrder := cleanSample()
	outOfOrder.Custody[2].At = stamp("2026-03-14T09:00:00Z")
	failedOnly(t, assess(t, outOfOrder, cleanResult()), CheckCustody)

	mismatched := cleanSample()
	mismatched.CollectedAt = stamp("2026-03-14T07:00:00Z")
	failedOnly(t, assess(t, mismatched, cleanResult()), CheckCustody)

	gap := cleanSample()
	gap.Custody[3].At = stamp("2026-03-16T04:00:00Z") // 37 hours after receipt
	late := cleanResult()
	late.AnalysedAt = stamp("2026-03-16T06:00:00Z")
	assessment := assess(t, gap, late)
	if assessment.Verdict != Fail {
		t.Fatalf("a long custody gap was accepted: %v", assessment)
	}

	analysedFirst := cleanResult()
	analysedFirst.AnalysedAt = stamp("2026-03-15T05:00:00Z") // before the extraction step
	assessment = assess(t, cleanSample(), analysedFirst)
	if assessment.Verdict != Fail {
		t.Fatalf("analysis before the final handoff was accepted: %v", assessment)
	}
}

func TestCustodyBecomesAWarningWhenContinuityIsOptional(t *testing.T) {
	cfg := config.Default()
	cfg.Quality.RequireCustodyContinuity = false
	sample := cleanSample()
	sample.Custody[2].At = stamp("2026-03-14T09:00:00Z")
	measurement, err := quant.Quantify(cfg, sample, cleanResult())
	if err != nil {
		t.Fatalf("Quantify: %v", err)
	}
	assessment := Assess(cfg, sample, cleanResult(), measurement)
	if assessment.Verdict != Warn {
		t.Fatalf("verdict = %s, want warn", assessment.Verdict)
	}
	if !assessment.UsableForTrend {
		t.Fatal("an optional continuity breach barred the result from trends")
	}
}

func TestAssessAllTalliesAndSorts(t *testing.T) {
	cfg := config.Default()
	good := cleanResult()
	bad := cleanResult()
	bad.ResultID = "RES-2"
	bad.Controls.ExtractionBlankDetected = true
	sample := cleanSample()
	bundle := model.Bundle{Samples: []model.Sample{sample}, Results: []model.Result{good, bad}}
	measurements, _, err := quant.QuantifyBundle(cfg, bundle)
	if err != nil {
		t.Fatalf("QuantifyBundle: %v", err)
	}
	assessments, index := AssessAll(cfg, bundle, measurements)
	if len(assessments) != 2 {
		t.Fatalf("assessments = %d", len(assessments))
	}
	if assessments[0].ResultID != "RES-1" || assessments[1].ResultID != "RES-2" {
		t.Fatalf("order = %s, %s", assessments[0].ResultID, assessments[1].ResultID)
	}
	if index["RES-2"].Verdict != Fail {
		t.Fatalf("index lookup gave %s", index["RES-2"].Verdict)
	}
	tally := Count(assessments)
	if tally.Total != 2 || tally.Passed != 1 || tally.Failed != 1 {
		t.Fatalf("tally = %+v", tally)
	}
	reasons := FailureReasons(assessments)
	if len(reasons) != 1 || reasons[0].Name != CheckNegativeControl || reasons[0].Observed != 1 {
		t.Fatalf("failure reasons = %+v", reasons)
	}
}

func TestSeverityOrdering(t *testing.T) {
	if worse(Pass, Warn) != Warn {
		t.Error("warn should beat pass")
	}
	if worse(Warn, Fail) != Fail {
		t.Error("fail should beat warn")
	}
	if worse(Fail, Warn) != Fail {
		t.Error("fail should stay ahead of warn")
	}
	if rank(Pass) != 0 || rank(Warn) != 1 || rank(Fail) != 2 {
		t.Error("severity ranks are wrong")
	}
}
