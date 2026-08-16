package normalize

import (
	"math"
	"strings"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/timeutil"
)

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func influentSite() model.Site {
	return model.Site{
		SiteID: "WW-ONE", Label: "one", Matrix: model.MatrixWastewaterInfluent,
		Latitude: 45.2, Longitude: -95.4, ServedPopulation: 10000, MeanDailyFlowM3: 5000,
		IndicatorRefCopiesPerLitre: 2.0e7,
	}
}

func grabSite() model.Site {
	return model.Site{
		SiteID: "RIV-ONE", Label: "river", Matrix: model.MatrixWaterwayGrab,
		Latitude: 45.1, Longitude: -95.3,
	}
}

func influentSample() model.Sample {
	return model.Sample{
		SampleID: "SMP-1", SiteID: "WW-ONE", Matrix: model.MatrixWastewaterInfluent,
		CollectedAt: stamp("2026-03-14T08:00:00Z"), VolumeML: 200, TransportTempC: 4,
	}
}

// measurement builds a quantified measurement with a stated concentration,
// bypassing the assay arithmetic so that the normalisation stage can be tested
// on its own.
func measurement(siteID string, matrix model.Matrix, day string, concentration float64) quant.Measurement {
	return quant.Measurement{
		ResultID:            "RES-" + day,
		SampleID:            "SMP-1",
		SiteID:              siteID,
		Matrix:              matrix,
		TargetGene:          "influenza_a_matrix_gene",
		Day:                 day,
		CollectedAt:         stamp(day + "T08:00:00Z"),
		AnalysedAt:          stamp(day + "T18:00:00Z"),
		Status:              quant.StatusQuantified,
		Detected:            true,
		CopiesPerLitre:      concentration,
		QuantifiedMean:      concentration,
		LowerCopiesPerLitre: concentration / 2,
		UpperCopiesPerLitre: concentration * 2,
	}
}

func passing() qc.Assessment {
	return qc.Assessment{ResultID: "RES", Verdict: qc.Pass, UsableForTrend: true}
}

func TestIndicatorFactorRescalesTowardsReference(t *testing.T) {
	cfg := config.Default()
	site := influentSite()
	sample := influentSample()
	// Half the usual indicator strength means the sample was twice diluted.
	sample.IndicatorCopiesPerLitre = 1.0e7
	factor, applied, note := IndicatorFactor(cfg, site, sample)
	if !applied {
		t.Fatalf("the correction was skipped: %s", note)
	}
	if math.Abs(factor-2) > 1e-9 {
		t.Fatalf("factor = %v, want 2", factor)
	}
	// A very weak indicator would imply an absurd factor, so it is clamped.
	sample.IndicatorCopiesPerLitre = 1.0e5
	factor, applied, note = IndicatorFactor(cfg, site, sample)
	if !applied || factor != cfg.Normalisation.IndicatorCorrectionMax {
		t.Fatalf("factor = %v, want the clamp at %v", factor, cfg.Normalisation.IndicatorCorrectionMax)
	}
	if !strings.Contains(note, "clamped") {
		t.Fatalf("the clamp should be explained: %q", note)
	}
	// No indicator on the sample, or none on the site, means no correction.
	sample.IndicatorCopiesPerLitre = 0
	if factor, applied, _ = IndicatorFactor(cfg, site, sample); applied || factor != 1 {
		t.Fatalf("factor = %v applied=%v", factor, applied)
	}
	sample.IndicatorCopiesPerLitre = 2.0e7
	bare := site
	bare.IndicatorRefCopiesPerLitre = 0
	if factor, applied, _ = IndicatorFactor(cfg, bare, sample); applied || factor != 1 {
		t.Fatalf("factor = %v applied=%v", factor, applied)
	}
	off := cfg
	off.Normalisation.EnableIndicatorCorrection = false
	if factor, applied, _ = IndicatorFactor(off, site, sample); applied || factor != 1 {
		t.Fatalf("factor = %v applied=%v with correction disabled", factor, applied)
	}
}

func TestFlowSourcePrefersObserved(t *testing.T) {
	site := influentSite()
	sample := influentSample()
	flow, source := FlowLitresPerDay(site, sample)
	if source != FlowSiteMean || flow != 5e6 {
		t.Fatalf("flow = %v source = %s", flow, source)
	}
	sample.ObservedFlowM3 = 6000
	flow, source = FlowLitresPerDay(site, sample)
	if source != FlowObserved || flow != 6e6 {
		t.Fatalf("flow = %v source = %s", flow, source)
	}
	bare := grabSite()
	flow, source = FlowLitresPerDay(bare, model.Sample{})
	if source != FlowUnavailable || flow != 0 {
		t.Fatalf("flow = %v source = %s", flow, source)
	}
}

func TestApplyDerivesLoadAndPerCapita(t *testing.T) {
	cfg := config.Default()
	site := influentSite()
	sample := influentSample()
	point := Apply(cfg, site, sample, measurement("WW-ONE", model.MatrixWastewaterInfluent, "2026-03-14", 10000), passing())
	if point.SignalUnit != UnitCopiesPerDay {
		t.Fatalf("unit = %s", point.SignalUnit)
	}
	// Ten thousand copies per litre carried by five million litres a day.
	if math.Abs(point.LoadCopiesPerDay-5e10) > 1 {
		t.Fatalf("load = %v, want 5e10", point.LoadCopiesPerDay)
	}
	if math.Abs(point.SignalValue-5e10) > 1 {
		t.Fatalf("signal = %v", point.SignalValue)
	}
	if math.Abs(point.LoadPerPersonDay-5e6) > 1 {
		t.Fatalf("per person = %v, want 5e6", point.LoadPerPersonDay)
	}
	if math.Abs(point.SignalLower-2.5e10) > 1 || math.Abs(point.SignalUpper-1e11) > 1 {
		t.Fatalf("band = %v..%v", point.SignalLower, point.SignalUpper)
	}
	if point.IndicatorFactor != 1 {
		t.Fatalf("indicator factor = %v with no indicator measurement", point.IndicatorFactor)
	}
	if !point.Usable {
		t.Fatal("a passing result was marked unusable")
	}
}

func TestApplyKeepsGrabSamplesOnAConcentration(t *testing.T) {
	cfg := config.Default()
	point := Apply(cfg, grabSite(), model.Sample{
		SampleID: "SMP-2", SiteID: "RIV-ONE", Matrix: model.MatrixWaterwayGrab,
		CollectedAt: stamp("2026-03-14T08:00:00Z"), VolumeML: 1000,
	}, measurement("RIV-ONE", model.MatrixWaterwayGrab, "2026-03-14", 2500), passing())
	if point.SignalUnit != UnitCopiesPerLitre {
		t.Fatalf("unit = %s", point.SignalUnit)
	}
	if point.LoadCopiesPerDay != 0 {
		t.Fatalf("a grab sample produced a daily load of %v", point.LoadCopiesPerDay)
	}
	if point.SignalValue != 2500 {
		t.Fatalf("signal = %v", point.SignalValue)
	}
	if point.FlowSource != FlowUnavailable {
		t.Fatalf("flow source = %s", point.FlowSource)
	}
}

func TestApplyAppliesIndicatorCorrectionBeforeFlow(t *testing.T) {
	cfg := config.Default()
	site := influentSite()
	sample := influentSample()
	sample.IndicatorCopiesPerLitre = 1.0e7 // half strength, so the factor is two
	point := Apply(cfg, site, sample, measurement("WW-ONE", model.MatrixWastewaterInfluent, "2026-03-14", 10000), passing())
	if math.Abs(point.CorrectedCopiesPerLitre-20000) > 1 {
		t.Fatalf("corrected concentration = %v, want 20000", point.CorrectedCopiesPerLitre)
	}
	if math.Abs(point.LoadCopiesPerDay-1e11) > 1 {
		t.Fatalf("load = %v, want 1e11", point.LoadCopiesPerDay)
	}
}

func TestApplyRecordsMissingFlowAndPopulation(t *testing.T) {
	cfg := config.Default()
	site := influentSite()
	site.MeanDailyFlowM3 = 0
	point := Apply(cfg, site, influentSample(),
		measurement("WW-ONE", model.MatrixWastewaterInfluent, "2026-03-14", 10000), passing())
	if point.LoadCopiesPerDay != 0 {
		t.Fatalf("a load was derived without a flow: %v", point.LoadCopiesPerDay)
	}
	joined := strings.Join(point.Notes, " | ")
	if !strings.Contains(joined, "no flow figure") {
		t.Fatalf("notes = %q", joined)
	}
	site = influentSite()
	site.ServedPopulation = 0
	point = Apply(cfg, site, influentSample(),
		measurement("WW-ONE", model.MatrixWastewaterInfluent, "2026-03-14", 10000), passing())
	if point.LoadPerPersonDay != 0 {
		t.Fatalf("per capita load without a population: %v", point.LoadPerPersonDay)
	}
	if !strings.Contains(strings.Join(point.Notes, " | "), "served_population is zero") {
		t.Fatalf("notes = %v", point.Notes)
	}
}

func TestApplyAllRejectsDanglingReferences(t *testing.T) {
	cfg := config.Default()
	bundle := model.Bundle{
		Network: model.Network{Sites: []model.Site{influentSite()}},
		Samples: []model.Sample{influentSample()},
	}
	measurements := []quant.Measurement{measurement("WW-ONE", model.MatrixWastewaterInfluent, "2026-03-14", 1000)}
	if _, err := ApplyAll(cfg, bundle, measurements, map[string]qc.Assessment{}); err == nil {
		t.Fatal("a measurement with no assessment was accepted")
	}
	assessments := map[string]qc.Assessment{"RES-2026-03-14": passing()}
	points, err := ApplyAll(cfg, bundle, measurements, assessments)
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %d", len(points))
	}
	orphan := measurements[0]
	orphan.SampleID = "SMP-GHOST"
	if _, err := ApplyAll(cfg, bundle, []quant.Measurement{orphan}, assessments); err == nil {
		t.Fatal("a measurement with no sample was accepted")
	}
}

func point(siteID, day, gene string, value float64, usable, detected bool) Point {
	return Point{
		ResultID: "RES-" + day + "-" + gene, SampleID: "SMP-" + day, SiteID: siteID,
		Matrix: model.MatrixWaterwayGrab, TargetGene: gene, Day: day,
		CollectedAt: stamp(day + "T08:00:00Z"), Detected: detected, Usable: usable,
		Status: quant.StatusQuantified, CorrectedCopiesPerLitre: value,
		SignalValue: value, SignalUnit: UnitCopiesPerLitre,
	}
}

func TestBuildDailySeriesUsesOnlyThePrimaryTarget(t *testing.T) {
	cfg := config.Default()
	points := []Point{
		point("RIV-ONE", "2026-03-14", "influenza_a_matrix_gene", 1000, true, true),
		point("RIV-ONE", "2026-03-14", "h5_clade_marker", 100, true, true),
		point("RIV-ONE", "2026-03-15", "influenza_a_matrix_gene", 2000, true, true),
	}
	series := BuildDailySeries(cfg, "RIV-ONE", points)
	if series.TargetGene != "influenza_a_matrix_gene" {
		t.Fatalf("target gene = %q", series.TargetGene)
	}
	if len(series.Days) != 2 {
		t.Fatalf("days = %d", len(series.Days))
	}
	if series.Days[0].Value != 1000 {
		t.Fatalf("the secondary target leaked into the mean: %v", series.Days[0].Value)
	}
	if series.Days[0].Contributed != 1 {
		t.Fatalf("contributed = %d", series.Days[0].Contributed)
	}
}

func TestBuildDailySeriesFallsBackWhenThePrimaryTargetIsAbsent(t *testing.T) {
	cfg := config.Default()
	points := []Point{point("RIV-ONE", "2026-03-14", "some_other_gene", 500, true, true)}
	series := BuildDailySeries(cfg, "RIV-ONE", points)
	if len(series.Days) != 1 {
		t.Fatalf("the site was dropped entirely: %+v", series)
	}
	if series.TargetGene != "all targets" {
		t.Fatalf("target gene = %q", series.TargetGene)
	}
}

func TestBuildDailySeriesAveragesRepeatAssaysAndSkipsFailures(t *testing.T) {
	cfg := config.Default()
	cfg.Quantification.PrimaryTargetGene = ""
	points := []Point{
		point("RIV-ONE", "2026-03-14", "gene", 1000, true, true),
		point("RIV-ONE", "2026-03-14", "gene", 3000, true, false),
		point("RIV-ONE", "2026-03-15", "gene", 9999, false, true),
	}
	points[1].ResultID = "RES-repeat"
	series := BuildDailySeries(cfg, "RIV-ONE", points)
	if len(series.Days) != 1 {
		t.Fatalf("days = %d, want only the usable day", len(series.Days))
	}
	if series.Days[0].Value != 2000 {
		t.Fatalf("value = %v, want the mean of the two repeats", series.Days[0].Value)
	}
	if series.Days[0].Contributed != 2 {
		t.Fatalf("contributed = %d", series.Days[0].Contributed)
	}
	if !series.Days[0].Detected {
		t.Fatal("one detecting repeat should mark the day as detected")
	}
	if strings.Join(series.Days[0].ResultIDs, ",") != "RES-2026-03-14-gene,RES-repeat" {
		t.Fatalf("result ids = %v", series.Days[0].ResultIDs)
	}
}

func TestBuildAllSeriesSortsBySite(t *testing.T) {
	cfg := config.Default()
	cfg.Quantification.PrimaryTargetGene = ""
	points := []Point{
		point("Z-SITE", "2026-03-14", "gene", 10, true, true),
		point("A-SITE", "2026-03-14", "gene", 20, true, true),
	}
	all := BuildAllSeries(cfg, points)
	if len(all) != 2 || all[0].SiteID != "A-SITE" {
		t.Fatalf("series order = %+v", all)
	}
	grouped := BySite(points)
	if len(grouped) != 2 {
		t.Fatalf("grouped = %d", len(grouped))
	}
	usable := UsableBySite(points)
	if len(usable) != 2 {
		t.Fatalf("usable groups = %d", len(usable))
	}
}

func TestSortPointsIsCanonical(t *testing.T) {
	points := []Point{
		point("B-SITE", "2026-03-15", "gene", 1, true, true),
		point("A-SITE", "2026-03-16", "gene", 1, true, true),
		point("A-SITE", "2026-03-14", "gene", 1, true, true),
	}
	SortPoints(points)
	if points[0].SiteID != "A-SITE" || points[0].Day != "2026-03-14" {
		t.Fatalf("first point = %s %s", points[0].SiteID, points[0].Day)
	}
	if points[2].SiteID != "B-SITE" {
		t.Fatalf("last point = %s", points[2].SiteID)
	}
}
