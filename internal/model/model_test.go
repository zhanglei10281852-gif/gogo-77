package model

import (
	"strings"
	"testing"

	"FluWatershed/internal/timeutil"
)

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func ptr(v float64) *float64 { return &v }

func goodSite() Site {
	return Site{
		SiteID: "WW-ONE", Label: "one", Matrix: MatrixWastewaterInfluent,
		Latitude: 45.2, Longitude: -95.4, ServedPopulation: 12000, MeanDailyFlowM3: 5000,
		IndicatorRefCopiesPerLitre: 2.4e7,
	}
}

func goodNetwork() Network {
	return Network{
		SchemaVersion: SchemaVersion,
		CatchmentID:   "test-basin",
		Label:         "test",
		Sites: []Site{
			goodSite(),
			{SiteID: "RIV-ONE", Label: "river", Matrix: MatrixWaterwayGrab, Latitude: 45.1, Longitude: -95.3},
		},
		Links: []Link{{UpstreamSiteID: "WW-ONE", DownstreamSiteID: "RIV-ONE", TravelHours: 6}},
	}
}

func goodSample() Sample {
	cold := 4.0
	return Sample{
		SampleID: "SMP-1", SiteID: "WW-ONE", Matrix: MatrixWastewaterInfluent,
		CollectedAt: stamp("2026-03-14T08:00:00Z"), VolumeML: 200, TransportTempC: 4,
		Custody: []CustodyStep{
			{Action: CustodyCollected, Holder: "crew", At: stamp("2026-03-14T08:00:00Z"), TemperatureC: &cold},
			{Action: CustodyReceived, Holder: "bench", At: stamp("2026-03-14T15:00:00Z"), TemperatureC: &cold},
		},
	}
}

func goodResult() Result {
	return Result{
		ResultID: "RES-1", SampleID: "SMP-1", TargetGene: "influenza_a_matrix_gene",
		AnalysedAt:     stamp("2026-03-15T10:00:00Z"),
		Replicates:     []Replicate{{Well: "A1", Ct: ptr(31.5)}, {Well: "A2", Ct: nil}},
		DilutionFactor: 1, ExtractVolumeUL: 100, TemplateVolumeUL: 5, RecoveryFraction: 0.6,
		Curve: StandardCurve{Slope: -3.32, Intercept: 38.5, RSquared: 0.995,
			LODCopiesPerReaction: 5, LOQCopiesPerReaction: 20},
		Controls: Controls{PositiveControlExpected: 10000, PositiveControlObserved: 9500,
			InhibitionControlCt: ptr(24.5), InhibitionReferenceCt: 24.4},
	}
}

func goodEvent() CarcassEvent {
	return CarcassEvent{
		EventID: "EVT-1", ObservedAt: stamp("2026-03-14T09:00:00Z"),
		Latitude: 45.22, Longitude: -95.46, Species: "invented shearwater", CarcassCount: 6,
	}
}

func goodBundle() Bundle {
	return Bundle{
		Network: goodNetwork(),
		Samples: []Sample{goodSample()},
		Results: []Result{goodResult()},
		Events:  []CarcassEvent{goodEvent()},
	}
}

func TestValidIDRules(t *testing.T) {
	valid := []string{"WW-ONE", "a1", "site.one_2", "A-b.c_d"}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("%q should be a valid identifier", id)
		}
	}
	invalid := []string{"", "a", "-lead", "has space", "has/slash", strings.Repeat("x", 65)}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("%q should not be a valid identifier", id)
		}
	}
}

func TestMatrixBehaviour(t *testing.T) {
	if len(Matrices()) != 4 {
		t.Fatalf("matrices = %v", Matrices())
	}
	if Matrix("mystery").Valid() {
		t.Error("an unknown matrix was accepted")
	}
	if !MatrixWastewaterInfluent.FlowNormalisable() {
		t.Error("influent should be flow normalisable")
	}
	if MatrixWaterwayGrab.FlowNormalisable() {
		t.Error("a grab sample should not be flow normalisable")
	}
	if MatrixCarcassEvent.Quantitative() {
		t.Error("a carcass event carries no concentration")
	}
	for _, matrix := range Matrices() {
		if !matrix.Valid() {
			t.Errorf("%s failed its own validity check", matrix)
		}
	}
	for _, action := range CustodyActions() {
		if !action.Valid() {
			t.Errorf("%s failed its own validity check", action)
		}
	}
	if CustodyAction("teleported").Valid() {
		t.Error("an unknown custody action was accepted")
	}
}

func TestValidateBundleAcceptsCleanInput(t *testing.T) {
	problems := ValidateBundle(goodBundle())
	if len(problems) != 0 {
		t.Fatalf("clean input produced problems: %v", problems)
	}
	if err := problems.Err(); err != nil {
		t.Fatalf("Err on an empty list = %v", err)
	}
}

func TestValidateNetworkCatchesStructuralFaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Network)
		want   string
	}{
		{"schema", func(n *Network) { n.SchemaVersion = "other" }, "schema_version"},
		{"catchment id", func(n *Network) { n.CatchmentID = "!" }, "catchment_id"},
		{"no sites", func(n *Network) { n.Sites = nil }, "at least one site"},
		{"duplicate site", func(n *Network) { n.Sites = append(n.Sites, n.Sites[0]) }, "duplicate site_id"},
		{"self link", func(n *Network) {
			n.Links = []Link{{UpstreamSiteID: "WW-ONE", DownstreamSiteID: "WW-ONE"}}
		}, "upstream of itself"},
		{"unknown upstream", func(n *Network) {
			n.Links = []Link{{UpstreamSiteID: "GHOST", DownstreamSiteID: "RIV-ONE"}}
		}, "not a declared site"},
		{"duplicate link", func(n *Network) { n.Links = append(n.Links, n.Links[0]) }, "duplicate link"},
		{"travel hours", func(n *Network) { n.Links[0].TravelHours = -1 }, "travel_hours"},
		{"missing label", func(n *Network) { n.Sites[0].Label = "  " }, "label is required"},
		{"latitude", func(n *Network) { n.Sites[0].Latitude = 200 }, "latitude"},
		{"longitude", func(n *Network) { n.Sites[0].Longitude = -400 }, "longitude"},
		{"matrix", func(n *Network) { n.Sites[0].Matrix = "mystery" }, "not recognised"},
		{"flow", func(n *Network) { n.Sites[0].MeanDailyFlowM3 = 0 }, "mean_daily_flow_m3"},
		{"population", func(n *Network) { n.Sites[0].ServedPopulation = 0 }, "served_population"},
		{"threshold", func(n *Network) { n.Sites[0].AbsoluteThreshold = -5 }, "absolute_threshold_signal"},
	}
	for _, item := range cases {
		network := goodNetwork()
		item.mutate(&network)
		problems := ValidateNetwork(network)
		if len(problems) == 0 {
			t.Errorf("%s: no problem reported", item.name)
			continue
		}
		if !strings.Contains(problems.Err().Error(), item.want) {
			t.Errorf("%s: error %v does not mention %q", item.name, problems.Err(), item.want)
		}
	}
}

func TestValidateSampleCatchesFaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Sample)
		want   string
	}{
		{"id", func(s *Sample) { s.SampleID = "" }, "sample_id"},
		{"site", func(s *Sample) { s.SiteID = "bad id" }, "site_id"},
		{"matrix", func(s *Sample) { s.Matrix = "mystery" }, "not recognised"},
		{"collected", func(s *Sample) { s.CollectedAt = timeutil.Stamp{} }, "collected_at"},
		{"volume", func(s *Sample) { s.VolumeML = 0 }, "volume_ml"},
		{"volume range", func(s *Sample) { s.VolumeML = 1e9 }, "volume_ml"},
		{"temperature", func(s *Sample) { s.TransportTempC = 900 }, "transport_temperature_c"},
		{"flow", func(s *Sample) { s.ObservedFlowM3 = -1 }, "observed_flow_m3"},
		{"indicator", func(s *Sample) { s.IndicatorCopiesPerLitre = -1 }, "indicator_copies_per_litre"},
		{"no custody", func(s *Sample) { s.Custody = nil }, "custody must hold"},
		{"first step", func(s *Sample) { s.Custody[0].Action = CustodyReceived }, "first custody step"},
		{"holder", func(s *Sample) { s.Custody[0].Holder = "" }, "holder is required"},
		{"step action", func(s *Sample) { s.Custody[1].Action = "teleported" }, "not recognised"},
		{"step time", func(s *Sample) { s.Custody[1].At = timeutil.Stamp{} }, "timestamp is required"},
		{"step temperature", func(s *Sample) { s.Custody[1].TemperatureC = ptr(500) }, "temperature_c"},
	}
	for _, item := range cases {
		sample := goodSample()
		item.mutate(&sample)
		problems := ValidateSample(sample)
		if len(problems) == 0 {
			t.Errorf("%s: no problem reported", item.name)
			continue
		}
		if !strings.Contains(problems.Err().Error(), item.want) {
			t.Errorf("%s: error %v does not mention %q", item.name, problems.Err(), item.want)
		}
	}
}

func TestValidateResultCatchesFaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Result)
		want   string
	}{
		{"id", func(r *Result) { r.ResultID = "" }, "result_id"},
		{"gene", func(r *Result) { r.TargetGene = " " }, "target_gene"},
		{"analysed", func(r *Result) { r.AnalysedAt = timeutil.Stamp{} }, "analysed_at"},
		{"replicates", func(r *Result) { r.Replicates = nil }, "at least one replicate"},
		{"well label", func(r *Result) { r.Replicates[0].Well = "" }, "well label"},
		{"duplicate well", func(r *Result) { r.Replicates[1].Well = "A1" }, "appears twice"},
		{"ct range", func(r *Result) { r.Replicates[0].Ct = ptr(99) }, "ct"},
		{"dilution", func(r *Result) { r.DilutionFactor = 0.5 }, "dilution_factor"},
		{"extract", func(r *Result) { r.ExtractVolumeUL = 0 }, "extract_volume_ul"},
		{"template", func(r *Result) { r.TemplateVolumeUL = 0 }, "template_volume_ul"},
		{"template over extract", func(r *Result) { r.TemplateVolumeUL = 200 }, "exceeds extract_volume_ul"},
		{"recovery", func(r *Result) { r.RecoveryFraction = 1.5 }, "recovery_fraction"},
		{"slope sign", func(r *Result) { r.Curve.Slope = 3 }, "must be negative"},
		{"slope steep", func(r *Result) { r.Curve.Slope = -9 }, "implausibly steep"},
		{"intercept", func(r *Result) { r.Curve.Intercept = 0 }, "intercept"},
		{"r squared", func(r *Result) { r.Curve.RSquared = 2 }, "r_squared"},
		{"lod", func(r *Result) { r.Curve.LODCopiesPerReaction = 0 }, "lod_copies_per_reaction"},
		{"loq below lod", func(r *Result) { r.Curve.LOQCopiesPerReaction = 1 }, "cannot be below lod"},
		{"negative control", func(r *Result) { r.Controls.NegativeControlCt = ptr(-2) }, "negative_control_ct"},
		{"inhibition ct", func(r *Result) { r.Controls.InhibitionControlCt = ptr(90) }, "inhibition_control_ct"},
		{"inhibition reference", func(r *Result) { r.Controls.InhibitionReferenceCt = 0 }, "inhibition_reference_ct"},
		{"positive expected", func(r *Result) { r.Controls.PositiveControlExpected = 0 }, "positive_control_expected"},
		{"positive observed", func(r *Result) { r.Controls.PositiveControlObserved = -1 }, "positive_control_observed"},
	}
	for _, item := range cases {
		result := goodResult()
		item.mutate(&result)
		problems := ValidateResult(result)
		if len(problems) == 0 {
			t.Errorf("%s: no problem reported", item.name)
			continue
		}
		if !strings.Contains(problems.Err().Error(), item.want) {
			t.Errorf("%s: error %v does not mention %q", item.name, problems.Err(), item.want)
		}
	}
}

func TestValidateEventCatchesFaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CarcassEvent)
		want   string
	}{
		{"id", func(e *CarcassEvent) { e.EventID = "" }, "event_id"},
		{"observed", func(e *CarcassEvent) { e.ObservedAt = timeutil.Stamp{} }, "observed_at"},
		{"latitude", func(e *CarcassEvent) { e.Latitude = 100 }, "latitude"},
		{"longitude", func(e *CarcassEvent) { e.Longitude = 200 }, "longitude"},
		{"species", func(e *CarcassEvent) { e.Species = "" }, "species"},
		{"count", func(e *CarcassEvent) { e.CarcassCount = 0 }, "carcass_count"},
	}
	for _, item := range cases {
		event := goodEvent()
		item.mutate(&event)
		problems := ValidateEvent(event)
		if len(problems) == 0 {
			t.Errorf("%s: no problem reported", item.name)
			continue
		}
		if !strings.Contains(problems.Err().Error(), item.want) {
			t.Errorf("%s: error %v does not mention %q", item.name, problems.Err(), item.want)
		}
	}
}

func TestValidateBundleCrossChecks(t *testing.T) {
	bundle := goodBundle()
	bundle.Samples[0].SiteID = "GHOST"
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "not a declared site") {
		t.Fatalf("a sample at an unknown site was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Samples[0].Matrix = MatrixSediment
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "disagrees with site matrix") {
		t.Fatalf("a matrix mismatch was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Results[0].SampleID = "SMP-GHOST"
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "no matching sample") {
		t.Fatalf("an orphan result was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Results[0].AnalysedAt = stamp("2026-03-13T10:00:00Z")
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "precedes collection") {
		t.Fatalf("analysis before collection was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Results = nil
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "no assay result") {
		t.Fatalf("an unmeasured quantitative sample was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Samples = append(bundle.Samples, bundle.Samples[0])
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "duplicate sample_id") {
		t.Fatalf("a duplicate sample was accepted: %v", err)
	}

	bundle = goodBundle()
	bundle.Events = append(bundle.Events, bundle.Events[0])
	if err := ValidateBundle(bundle).Err(); err == nil || !strings.Contains(err.Error(), "duplicate event_id") {
		t.Fatalf("a duplicate event was accepted: %v", err)
	}
}

func TestCustodyOrdered(t *testing.T) {
	steps := goodSample().Custody
	if ok, index := CustodyOrdered(steps); !ok || index != -1 {
		t.Fatalf("clean custody reported out of order at %d", index)
	}
	steps[1].At = stamp("2026-03-14T07:00:00Z")
	ok, index := CustodyOrdered(steps)
	if ok || index != 1 {
		t.Fatalf("out of order custody = ok %v index %d", ok, index)
	}
}

func TestSortAllIsCanonical(t *testing.T) {
	bundle := goodBundle()
	second := goodSample()
	second.SampleID = "SMP-0"
	second.CollectedAt = stamp("2026-03-12T08:00:00Z")
	bundle.Samples = append(bundle.Samples, second)
	extraResult := goodResult()
	extraResult.ResultID = "RES-0"
	extraResult.SampleID = "SMP-0"
	bundle.Results = append(bundle.Results, extraResult)
	extraSite := Site{SiteID: "AA-SITE", Label: "a", Matrix: MatrixSediment}
	bundle.Network.Sites = append(bundle.Network.Sites, extraSite)
	bundle.SortAll()
	if bundle.Samples[0].SampleID != "SMP-0" {
		t.Fatalf("samples were not sorted by collection time: %v", bundle.Samples[0].SampleID)
	}
	if bundle.Results[0].SampleID != "SMP-0" {
		t.Fatalf("results were not sorted by sample: %v", bundle.Results[0].SampleID)
	}
	if bundle.Network.Sites[0].SiteID != "AA-SITE" {
		t.Fatalf("sites were not sorted: %v", bundle.Network.Sites[0].SiteID)
	}
	if got := strings.Join(bundle.Network.SiteIDs(), ","); got != "AA-SITE,RIV-ONE,WW-ONE" {
		t.Fatalf("site ids = %s", got)
	}
	if _, ok := bundle.Network.SiteByID("GHOST"); ok {
		t.Error("an unknown site was found")
	}
	if index := bundle.SampleByID(); index["SMP-0"].SampleID != "SMP-0" {
		t.Error("sample index lookup failed")
	}
}

func TestResultHelpers(t *testing.T) {
	result := goodResult()
	if result.DetectedReplicates() != 1 {
		t.Fatalf("detected replicates = %d", result.DetectedReplicates())
	}
	if got := result.DetectedCts(); len(got) != 1 || got[0] != 31.5 {
		t.Fatalf("detected Ct values = %v", got)
	}
	if !result.Replicates[0].Detected() || result.Replicates[1].Detected() {
		t.Fatal("replicate detection flags are wrong")
	}
	sample := goodSample()
	last, ok := sample.LastCustodyAt()
	if !ok || last.String() != "2026-03-14T15:00:00Z" {
		t.Fatalf("last custody = %s ok=%v", last, ok)
	}
	if got := goodSite().FlowLitresPerDay(); got != 5e6 {
		t.Fatalf("flow in litres = %v", got)
	}
}

func TestProblemsFormatting(t *testing.T) {
	var problems Problems
	for index := 0; index < 7; index++ {
		problems.Add("scope", "ref", "problem %d", index)
	}
	err := problems.Err()
	if err == nil {
		t.Fatal("no error from a non-empty list")
	}
	if !strings.Contains(err.Error(), "7 validation problem(s)") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "and 2 more") {
		t.Fatalf("the tail should be counted: %v", err)
	}
	bare := Problem{Scope: "scope", Message: "message"}
	if bare.String() != "scope: message" {
		t.Fatalf("bare problem = %q", bare.String())
	}
	withRef := Problem{Scope: "scope", Ref: "ref", Message: "message"}
	if withRef.String() != "scope ref: message" {
		t.Fatalf("referenced problem = %q", withRef.String())
	}
}
