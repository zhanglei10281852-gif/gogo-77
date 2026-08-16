package model

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"FluWatershed/internal/timeutil"
)

// idPattern constrains identifiers to characters that are safe in file names,
// ledger keys and printed tables.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,63}$`)

// ValidID reports whether text is an acceptable identifier.
func ValidID(text string) bool { return idPattern.MatchString(text) }

// ValidateNetwork checks the catchment document for structural sanity. Graph
// acyclicity is checked separately by the catchment package, which owns the
// topology; here we only confirm the pieces exist and are individually sound.
func ValidateNetwork(n Network) Problems {
	var problems Problems
	if n.SchemaVersion != SchemaVersion {
		problems.Add("network", n.CatchmentID, "schema_version must be %q, got %q", SchemaVersion, n.SchemaVersion)
	}
	if !ValidID(n.CatchmentID) {
		problems.Add("network", n.CatchmentID, "catchment_id %q is not a valid identifier", n.CatchmentID)
	}
	if len(n.Sites) == 0 {
		problems.Add("network", n.CatchmentID, "at least one site is required")
	}
	seen := make(map[string]bool, len(n.Sites))
	for _, site := range n.Sites {
		problems = append(problems, validateSite(site)...)
		if seen[site.SiteID] {
			problems.Add("site", site.SiteID, "duplicate site_id")
		}
		seen[site.SiteID] = true
	}
	linkSeen := make(map[string]bool, len(n.Links))
	for _, link := range n.Links {
		ref := link.UpstreamSiteID + "->" + link.DownstreamSiteID
		if link.UpstreamSiteID == link.DownstreamSiteID {
			problems.Add("link", ref, "a site cannot be upstream of itself")
		}
		if !seen[link.UpstreamSiteID] {
			problems.Add("link", ref, "upstream_site_id %q is not a declared site", link.UpstreamSiteID)
		}
		if !seen[link.DownstreamSiteID] {
			problems.Add("link", ref, "downstream_site_id %q is not a declared site", link.DownstreamSiteID)
		}
		if link.TravelHours < 0 || link.TravelHours > 24*30 {
			problems.Add("link", ref, "travel_hours %.2f is outside the accepted range 0..720", link.TravelHours)
		}
		if linkSeen[ref] {
			problems.Add("link", ref, "duplicate link")
		}
		linkSeen[ref] = true
	}
	return problems
}

func validateSite(site Site) Problems {
	var problems Problems
	if !ValidID(site.SiteID) {
		problems.Add("site", site.SiteID, "site_id %q is not a valid identifier", site.SiteID)
	}
	if strings.TrimSpace(site.Label) == "" {
		problems.Add("site", site.SiteID, "label is required")
	}
	if !site.Matrix.Valid() {
		problems.Add("site", site.SiteID, "matrix %q is not recognised", site.Matrix)
	}
	if site.Latitude < -90 || site.Latitude > 90 {
		problems.Add("site", site.SiteID, "latitude %.4f is outside -90..90", site.Latitude)
	}
	if site.Longitude < -180 || site.Longitude > 180 {
		problems.Add("site", site.SiteID, "longitude %.4f is outside -180..180", site.Longitude)
	}
	if site.ServedPopulation < 0 {
		problems.Add("site", site.SiteID, "served_population cannot be negative")
	}
	if site.MeanDailyFlowM3 < 0 {
		problems.Add("site", site.SiteID, "mean_daily_flow_m3 cannot be negative")
	}
	if site.Matrix.FlowNormalisable() {
		if site.MeanDailyFlowM3 <= 0 {
			problems.Add("site", site.SiteID, "mean_daily_flow_m3 must be positive for a %s site", site.Matrix)
		}
		if site.ServedPopulation <= 0 {
			problems.Add("site", site.SiteID, "served_population must be positive for a %s site", site.Matrix)
		}
	}
	if site.IndicatorRefCopiesPerLitre < 0 {
		problems.Add("site", site.SiteID, "indicator_reference_copies_per_litre cannot be negative")
	}
	if site.AbsoluteThreshold < 0 {
		problems.Add("site", site.SiteID, "absolute_threshold_signal cannot be negative")
	}
	return problems
}

// ValidateSample checks one collection record on its own terms.
func ValidateSample(sample Sample) Problems {
	var problems Problems
	if !ValidID(sample.SampleID) {
		problems.Add("sample", sample.SampleID, "sample_id %q is not a valid identifier", sample.SampleID)
	}
	if !ValidID(sample.SiteID) {
		problems.Add("sample", sample.SampleID, "site_id %q is not a valid identifier", sample.SiteID)
	}
	if !sample.Matrix.Valid() {
		problems.Add("sample", sample.SampleID, "matrix %q is not recognised", sample.Matrix)
	}
	if !sample.CollectedAt.IsSet() {
		problems.Add("sample", sample.SampleID, "collected_at is required")
	}
	if sample.Matrix.Quantitative() && sample.VolumeML <= 0 {
		problems.Add("sample", sample.SampleID, "volume_ml must be positive for a quantitative matrix")
	}
	if sample.VolumeML < 0 || sample.VolumeML > 100000 {
		problems.Add("sample", sample.SampleID, "volume_ml %.2f is outside the accepted range 0..100000", sample.VolumeML)
	}
	if sample.TransportTempC < -80 || sample.TransportTempC > 60 {
		problems.Add("sample", sample.SampleID, "transport_temperature_c %.2f is outside -80..60", sample.TransportTempC)
	}
	if sample.ObservedFlowM3 < 0 {
		problems.Add("sample", sample.SampleID, "observed_flow_m3 cannot be negative")
	}
	if sample.IndicatorCopiesPerLitre < 0 {
		problems.Add("sample", sample.SampleID, "indicator_copies_per_litre cannot be negative")
	}
	problems = append(problems, validateCustody(sample)...)
	return problems
}

func validateCustody(sample Sample) Problems {
	var problems Problems
	if len(sample.Custody) == 0 {
		problems.Add("sample", sample.SampleID, "custody must hold at least the collection step")
		return problems
	}
	for index, step := range sample.Custody {
		ref := sample.SampleID
		if !step.Action.Valid() {
			problems.Add("custody", ref, "step %d action %q is not recognised", index+1, step.Action)
		}
		if strings.TrimSpace(step.Holder) == "" {
			problems.Add("custody", ref, "step %d holder is required", index+1)
		}
		if !step.At.IsSet() {
			problems.Add("custody", ref, "step %d timestamp is required", index+1)
		}
		if step.TemperatureC != nil {
			temp := *step.TemperatureC
			if temp < -80 || temp > 60 {
				problems.Add("custody", ref, "step %d temperature_c %.2f is outside -80..60", index+1, temp)
			}
		}
	}
	if sample.Custody[0].Action != CustodyCollected {
		problems.Add("custody", sample.SampleID, "the first custody step must be %q", CustodyCollected)
	}
	return problems
}

// ValidateResult checks one assay record on its own terms.
func ValidateResult(result Result) Problems {
	var problems Problems
	if !ValidID(result.ResultID) {
		problems.Add("result", result.ResultID, "result_id %q is not a valid identifier", result.ResultID)
	}
	if !ValidID(result.SampleID) {
		problems.Add("result", result.ResultID, "sample_id %q is not a valid identifier", result.SampleID)
	}
	if strings.TrimSpace(result.TargetGene) == "" {
		problems.Add("result", result.ResultID, "target_gene is required")
	}
	if !result.AnalysedAt.IsSet() {
		problems.Add("result", result.ResultID, "analysed_at is required")
	}
	if len(result.Replicates) == 0 {
		problems.Add("result", result.ResultID, "at least one replicate is required")
	}
	wells := make(map[string]bool, len(result.Replicates))
	for index, rep := range result.Replicates {
		if strings.TrimSpace(rep.Well) == "" {
			problems.Add("result", result.ResultID, "replicate %d well label is required", index+1)
		}
		if wells[rep.Well] {
			problems.Add("result", result.ResultID, "replicate well %q appears twice", rep.Well)
		}
		wells[rep.Well] = true
		if rep.Ct != nil {
			ct := *rep.Ct
			if !finite(ct) || ct <= 0 || ct > 60 {
				problems.Add("result", result.ResultID, "replicate %s ct %.3f is outside 0..60", rep.Well, ct)
			}
		}
	}
	if result.DilutionFactor < 1 {
		problems.Add("result", result.ResultID, "dilution_factor %.4f must be at least 1", result.DilutionFactor)
	}
	if result.ExtractVolumeUL <= 0 {
		problems.Add("result", result.ResultID, "extract_volume_ul must be positive")
	}
	if result.TemplateVolumeUL <= 0 {
		problems.Add("result", result.ResultID, "template_volume_ul must be positive")
	}
	if result.TemplateVolumeUL > result.ExtractVolumeUL {
		problems.Add("result", result.ResultID, "template_volume_ul %.2f exceeds extract_volume_ul %.2f",
			result.TemplateVolumeUL, result.ExtractVolumeUL)
	}
	if result.RecoveryFraction <= 0 || result.RecoveryFraction > 1 {
		problems.Add("result", result.ResultID, "recovery_fraction %.4f must fall in (0,1]", result.RecoveryFraction)
	}
	problems = append(problems, validateCurve(result)...)
	problems = append(problems, validateControls(result)...)
	return problems
}

func validateCurve(result Result) Problems {
	var problems Problems
	curve := result.Curve
	if curve.Slope >= 0 {
		problems.Add("curve", result.ResultID, "slope %.4f must be negative", curve.Slope)
	}
	if curve.Slope < -6 {
		problems.Add("curve", result.ResultID, "slope %.4f is implausibly steep", curve.Slope)
	}
	if curve.Intercept <= 0 || curve.Intercept > 60 {
		problems.Add("curve", result.ResultID, "intercept %.4f is outside 0..60", curve.Intercept)
	}
	if curve.RSquared < 0 || curve.RSquared > 1 {
		problems.Add("curve", result.ResultID, "r_squared %.4f is outside 0..1", curve.RSquared)
	}
	if curve.LODCopiesPerReaction <= 0 {
		problems.Add("curve", result.ResultID, "lod_copies_per_reaction must be positive")
	}
	if curve.LOQCopiesPerReaction <= 0 {
		problems.Add("curve", result.ResultID, "loq_copies_per_reaction must be positive")
	}
	if curve.LOQCopiesPerReaction < curve.LODCopiesPerReaction {
		problems.Add("curve", result.ResultID, "loq %.2f cannot be below lod %.2f",
			curve.LOQCopiesPerReaction, curve.LODCopiesPerReaction)
	}
	return problems
}

func validateControls(result Result) Problems {
	var problems Problems
	controls := result.Controls
	if controls.NegativeControlCt != nil {
		ct := *controls.NegativeControlCt
		if !finite(ct) || ct <= 0 || ct > 60 {
			problems.Add("controls", result.ResultID, "negative_control_ct %.3f is outside 0..60", ct)
		}
	}
	if controls.InhibitionControlCt != nil {
		ct := *controls.InhibitionControlCt
		if !finite(ct) || ct <= 0 || ct > 60 {
			problems.Add("controls", result.ResultID, "inhibition_control_ct %.3f is outside 0..60", ct)
		}
	}
	if controls.InhibitionReferenceCt <= 0 || controls.InhibitionReferenceCt > 60 {
		problems.Add("controls", result.ResultID, "inhibition_reference_ct %.3f is outside 0..60",
			controls.InhibitionReferenceCt)
	}
	if controls.PositiveControlExpected <= 0 {
		problems.Add("controls", result.ResultID, "positive_control_expected_copies must be positive")
	}
	if controls.PositiveControlObserved < 0 {
		problems.Add("controls", result.ResultID, "positive_control_observed_copies cannot be negative")
	}
	return problems
}

// ValidateEvent checks one carcass observation.
func ValidateEvent(event CarcassEvent) Problems {
	var problems Problems
	if !ValidID(event.EventID) {
		problems.Add("event", event.EventID, "event_id %q is not a valid identifier", event.EventID)
	}
	if !event.ObservedAt.IsSet() {
		problems.Add("event", event.EventID, "observed_at is required")
	}
	if event.Latitude < -90 || event.Latitude > 90 {
		problems.Add("event", event.EventID, "latitude %.4f is outside -90..90", event.Latitude)
	}
	if event.Longitude < -180 || event.Longitude > 180 {
		problems.Add("event", event.EventID, "longitude %.4f is outside -180..180", event.Longitude)
	}
	if strings.TrimSpace(event.Species) == "" {
		problems.Add("event", event.EventID, "species is required")
	}
	if event.CarcassCount <= 0 {
		problems.Add("event", event.EventID, "carcass_count must be positive")
	}
	return problems
}

// ValidateBundle runs every per-object check and then the cross-object checks
// that only make sense once the whole input set is present.
func ValidateBundle(bundle Bundle) Problems {
	problems := ValidateNetwork(bundle.Network)
	siteIndex := make(map[string]Site, len(bundle.Network.Sites))
	for _, site := range bundle.Network.Sites {
		siteIndex[site.SiteID] = site
	}
	sampleIDs := make(map[string]bool, len(bundle.Samples))
	for _, sample := range bundle.Samples {
		problems = append(problems, ValidateSample(sample)...)
		if sampleIDs[sample.SampleID] {
			problems.Add("sample", sample.SampleID, "duplicate sample_id")
		}
		sampleIDs[sample.SampleID] = true
		site, ok := siteIndex[sample.SiteID]
		if !ok {
			problems.Add("sample", sample.SampleID, "site_id %q is not a declared site", sample.SiteID)
			continue
		}
		if site.Matrix != sample.Matrix {
			problems.Add("sample", sample.SampleID, "matrix %q disagrees with site matrix %q",
				sample.Matrix, site.Matrix)
		}
	}
	resultIDs := make(map[string]bool, len(bundle.Results))
	sampleByID := bundle.SampleByID()
	for _, result := range bundle.Results {
		problems = append(problems, ValidateResult(result)...)
		if resultIDs[result.ResultID] {
			problems.Add("result", result.ResultID, "duplicate result_id")
		}
		resultIDs[result.ResultID] = true
		sample, ok := sampleByID[result.SampleID]
		if !ok {
			problems.Add("result", result.ResultID, "sample_id %q has no matching sample", result.SampleID)
			continue
		}
		if result.AnalysedAt.IsSet() && sample.CollectedAt.IsSet() &&
			result.AnalysedAt.Before(sample.CollectedAt) {
			problems.Add("result", result.ResultID, "analysed_at %s precedes collection %s",
				result.AnalysedAt, sample.CollectedAt)
		}
	}
	eventIDs := make(map[string]bool, len(bundle.Events))
	for _, event := range bundle.Events {
		problems = append(problems, ValidateEvent(event)...)
		if eventIDs[event.EventID] {
			problems.Add("event", event.EventID, "duplicate event_id")
		}
		eventIDs[event.EventID] = true
	}
	problems = append(problems, orphanChecks(bundle, sampleIDs)...)
	return problems
}

// orphanChecks reports samples that no assay ever measured. That is not fatal,
// but a quantitative sample with no result usually means a partial export.
func orphanChecks(bundle Bundle, sampleIDs map[string]bool) Problems {
	var problems Problems
	measured := make(map[string]bool, len(bundle.Results))
	for _, result := range bundle.Results {
		measured[result.SampleID] = true
	}
	ids := make([]string, 0, len(sampleIDs))
	for id := range sampleIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	byID := bundle.SampleByID()
	for _, id := range ids {
		sample, ok := byID[id]
		if !ok || !sample.Matrix.Quantitative() {
			continue
		}
		if !measured[id] {
			problems.Add("sample", id, "quantitative sample has no assay result")
		}
	}
	return problems
}

// CustodyOrdered reports whether the custody steps are in non-decreasing time
// order and, if not, the index of the first step that goes backwards.
func CustodyOrdered(steps []CustodyStep) (bool, int) {
	var previous timeutil.Stamp
	for index, step := range steps {
		if previous.IsSet() && step.At.Before(previous) {
			return false, index
		}
		previous = step.At
	}
	return true, -1
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
