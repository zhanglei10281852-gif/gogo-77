// Package model defines the FluWatershed domain: monitoring sites, the
// catchment links between them, collected samples with their custody trail,
// and the assay results measured from those samples.
//
// The types here are the wire format as well as the in-memory form. They carry
// JSON tags with explicit names so that a stored ledger stays readable, and
// they hold no derived values: everything computed lives in the analysis
// packages so that a reload of the ledger reproduces the same answers.
package model

import (
	"fmt"
	"sort"
	"strings"

	"FluWatershed/internal/timeutil"
)

// SchemaVersion is the only accepted schema tag on input documents.
const SchemaVersion = "fluwatershed/v1"

// Matrix names the physical material a site samples.
type Matrix string

// The four supported matrices. Wastewater influent and waterway grabs carry
// quantitative signal; sediment is quantitative but slower moving; a carcass
// event is an observation rather than a measured concentration.
const (
	MatrixWastewaterInfluent Matrix = "wastewater_influent"
	MatrixWaterwayGrab       Matrix = "waterway_grab"
	MatrixSediment           Matrix = "sediment"
	MatrixCarcassEvent       Matrix = "carcass_event"
)

// Matrices returns every matrix in a fixed order.
func Matrices() []Matrix {
	return []Matrix{MatrixWastewaterInfluent, MatrixWaterwayGrab, MatrixSediment, MatrixCarcassEvent}
}

// Valid reports whether m is a recognised matrix.
func (m Matrix) Valid() bool {
	for _, candidate := range Matrices() {
		if m == candidate {
			return true
		}
	}
	return false
}

// Quantitative reports whether a concentration can be derived from the matrix.
func (m Matrix) Quantitative() bool {
	return m == MatrixWastewaterInfluent || m == MatrixWaterwayGrab || m == MatrixSediment
}

// FlowNormalisable reports whether a flow-normalised daily load is meaningful.
// Only a piped influent stream has a defensible daily flow behind it.
func (m Matrix) FlowNormalisable() bool { return m == MatrixWastewaterInfluent }

// CustodyAction labels one step in the chain of custody.
type CustodyAction string

// The recognised custody steps, in the order they normally occur.
const (
	CustodyCollected   CustodyAction = "collected"
	CustodyTransferred CustodyAction = "transferred"
	CustodyReceived    CustodyAction = "received"
	CustodyStored      CustodyAction = "stored"
	CustodyExtracted   CustodyAction = "extracted"
)

// CustodyActions returns the recognised custody steps in canonical order.
func CustodyActions() []CustodyAction {
	return []CustodyAction{CustodyCollected, CustodyTransferred, CustodyReceived, CustodyStored, CustodyExtracted}
}

// Valid reports whether a is a recognised custody action.
func (a CustodyAction) Valid() bool {
	for _, candidate := range CustodyActions() {
		if a == candidate {
			return true
		}
	}
	return false
}

// Site is one monitoring location.
type Site struct {
	SiteID           string  `json:"site_id"`
	Label            string  `json:"label"`
	Matrix           Matrix  `json:"matrix"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	ServedPopulation int     `json:"served_population"`
	MeanDailyFlowM3  float64 `json:"mean_daily_flow_m3"`
	// IndicatorRefCopiesPerLitre is the site's typical faecal-strength indicator
	// concentration, used to rescale a diluted sample back towards normal strength.
	IndicatorRefCopiesPerLitre float64 `json:"indicator_reference_copies_per_litre"`
	// AbsoluteThreshold is an optional site-specific alarm level expressed in the
	// site's own signal unit: copies per day wherever a daily load is derived and
	// copies per litre elsewhere. Zero means "use the configured default".
	AbsoluteThreshold float64 `json:"absolute_threshold_signal"`
}

// FlowLitresPerDay converts the site's mean daily flow to litres.
func (s Site) FlowLitresPerDay() float64 { return s.MeanDailyFlowM3 * 1000 }

// Link is a directed catchment edge: material at Upstream eventually reaches
// Downstream. TravelHours is the nominal transit time and is used when judging
// whether an upstream detection could explain a downstream one.
type Link struct {
	UpstreamSiteID   string  `json:"upstream_site_id"`
	DownstreamSiteID string  `json:"downstream_site_id"`
	TravelHours      float64 `json:"travel_hours"`
}

// Network is the catchment definition document.
type Network struct {
	SchemaVersion string `json:"schema_version"`
	CatchmentID   string `json:"catchment_id"`
	Label         string `json:"label"`
	Sites         []Site `json:"sites"`
	Links         []Link `json:"links"`
}

// SiteByID returns the named site.
func (n Network) SiteByID(id string) (Site, bool) {
	for _, site := range n.Sites {
		if site.SiteID == id {
			return site, true
		}
	}
	return Site{}, false
}

// SiteIDs returns every site identifier in ascending order.
func (n Network) SiteIDs() []string {
	ids := make([]string, 0, len(n.Sites))
	for _, site := range n.Sites {
		ids = append(ids, site.SiteID)
	}
	sort.Strings(ids)
	return ids
}

// CustodyStep records one handoff of physical material.
type CustodyStep struct {
	Action       CustodyAction  `json:"action"`
	Holder       string         `json:"holder"`
	At           timeutil.Stamp `json:"at"`
	TemperatureC *float64       `json:"temperature_c"`
}

// Sample is one physical collection event.
type Sample struct {
	SampleID                string         `json:"sample_id"`
	SiteID                  string         `json:"site_id"`
	Matrix                  Matrix         `json:"matrix"`
	CollectedAt             timeutil.Stamp `json:"collected_at"`
	VolumeML                float64        `json:"volume_ml"`
	TransportTempC          float64        `json:"transport_temperature_c"`
	ObservedFlowM3          float64        `json:"observed_flow_m3"`
	IndicatorCopiesPerLitre float64        `json:"indicator_copies_per_litre"`
	Custody                 []CustodyStep  `json:"custody"`
	Comment                 string         `json:"comment"`
}

// LastCustodyAt returns the newest custody timestamp on the sample.
func (s Sample) LastCustodyAt() (timeutil.Stamp, bool) {
	stamps := make([]timeutil.Stamp, 0, len(s.Custody))
	for _, step := range s.Custody {
		stamps = append(stamps, step.At)
	}
	return timeutil.Latest(stamps)
}

// Replicate is one PCR well. A nil Ct means the well never crossed threshold,
// which is a genuine non-detect rather than missing data.
type Replicate struct {
	Well string   `json:"well"`
	Ct   *float64 `json:"ct"`
}

// Detected reports whether the well amplified at all.
func (r Replicate) Detected() bool { return r.Ct != nil }

// StandardCurve is the calibration used to turn a Ct into copies. The relation
// is Ct = Slope*log10(copies) + Intercept, so Slope is negative.
type StandardCurve struct {
	Slope                float64 `json:"slope"`
	Intercept            float64 `json:"intercept"`
	RSquared             float64 `json:"r_squared"`
	LODCopiesPerReaction float64 `json:"lod_copies_per_reaction"`
	LOQCopiesPerReaction float64 `json:"loq_copies_per_reaction"`
}

// Controls holds the run controls that decide whether a result is trustworthy.
// A nil Ct on either control means no amplification was seen, which is the
// desired outcome for the negative control and a failure for the others.
type Controls struct {
	NegativeControlCt       *float64 `json:"negative_control_ct"`
	PositiveControlExpected float64  `json:"positive_control_expected_copies"`
	PositiveControlObserved float64  `json:"positive_control_observed_copies"`
	InhibitionControlCt     *float64 `json:"inhibition_control_ct"`
	InhibitionReferenceCt   float64  `json:"inhibition_reference_ct"`
	ExtractionBlankDetected bool     `json:"extraction_blank_detected"`
}

// Result is one assay run against one sample and one gene target.
type Result struct {
	ResultID         string         `json:"result_id"`
	SampleID         string         `json:"sample_id"`
	TargetGene       string         `json:"target_gene"`
	AnalysedAt       timeutil.Stamp `json:"analysed_at"`
	Replicates       []Replicate    `json:"replicates"`
	DilutionFactor   float64        `json:"dilution_factor"`
	ExtractVolumeUL  float64        `json:"extract_volume_ul"`
	TemplateVolumeUL float64        `json:"template_volume_ul"`
	RecoveryFraction float64        `json:"recovery_fraction"`
	Curve            StandardCurve  `json:"standard_curve"`
	Controls         Controls       `json:"controls"`
}

// DetectedReplicates counts wells that amplified.
func (r Result) DetectedReplicates() int {
	count := 0
	for _, rep := range r.Replicates {
		if rep.Detected() {
			count++
		}
	}
	return count
}

// DetectedCts returns the Ct values of the wells that amplified, in the order
// the wells were reported.
func (r Result) DetectedCts() []float64 {
	out := make([]float64, 0, len(r.Replicates))
	for _, rep := range r.Replicates {
		if rep.Detected() {
			out = append(out, *rep.Ct)
		}
	}
	return out
}

// CarcassEvent is a wild-bird mortality observation. It carries no
// concentration; it is corroborating evidence positioned in space and time.
type CarcassEvent struct {
	EventID      string         `json:"event_id"`
	ObservedAt   timeutil.Stamp `json:"observed_at"`
	Latitude     float64        `json:"latitude"`
	Longitude    float64        `json:"longitude"`
	Species      string         `json:"species"`
	CarcassCount int            `json:"carcass_count"`
	H5Confirmed  bool           `json:"h5_confirmed"`
	LocalityNote string         `json:"locality_note"`
}

// Bundle is a complete input set: the catchment plus everything observed in it.
type Bundle struct {
	Network Network
	Samples []Sample
	Results []Result
	Events  []CarcassEvent
}

// SampleByID indexes the bundle's samples.
func (b Bundle) SampleByID() map[string]Sample {
	index := make(map[string]Sample, len(b.Samples))
	for _, sample := range b.Samples {
		index[sample.SampleID] = sample
	}
	return index
}

// SortAll puts the bundle into canonical order: sites, links, samples, results
// and events all ascend by their identifying key so that every downstream
// stage sees the same sequence no matter how the input files were ordered.
func (b *Bundle) SortAll() {
	sort.SliceStable(b.Network.Sites, func(i, j int) bool {
		return b.Network.Sites[i].SiteID < b.Network.Sites[j].SiteID
	})
	sort.SliceStable(b.Network.Links, func(i, j int) bool {
		left, right := b.Network.Links[i], b.Network.Links[j]
		if left.UpstreamSiteID != right.UpstreamSiteID {
			return left.UpstreamSiteID < right.UpstreamSiteID
		}
		return left.DownstreamSiteID < right.DownstreamSiteID
	})
	sort.SliceStable(b.Samples, func(i, j int) bool {
		left, right := b.Samples[i], b.Samples[j]
		if !left.CollectedAt.Equal(right.CollectedAt) {
			return left.CollectedAt.Before(right.CollectedAt)
		}
		if left.SiteID != right.SiteID {
			return left.SiteID < right.SiteID
		}
		return left.SampleID < right.SampleID
	})
	sort.SliceStable(b.Results, func(i, j int) bool {
		left, right := b.Results[i], b.Results[j]
		if left.SampleID != right.SampleID {
			return left.SampleID < right.SampleID
		}
		if left.TargetGene != right.TargetGene {
			return left.TargetGene < right.TargetGene
		}
		return left.ResultID < right.ResultID
	})
	sort.SliceStable(b.Events, func(i, j int) bool {
		left, right := b.Events[i], b.Events[j]
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.Before(right.ObservedAt)
		}
		return left.EventID < right.EventID
	})
}

// Problem is one validation complaint. Scope names the kind of object, Ref
// identifies which one, and Message says what is wrong.
type Problem struct {
	Scope   string `json:"scope"`
	Ref     string `json:"ref"`
	Message string `json:"message"`
}

// String renders the problem as "scope ref: message".
func (p Problem) String() string {
	if p.Ref == "" {
		return fmt.Sprintf("%s: %s", p.Scope, p.Message)
	}
	return fmt.Sprintf("%s %s: %s", p.Scope, p.Ref, p.Message)
}

// Problems is an ordered collection of validation complaints.
type Problems []Problem

// Add appends a formatted problem.
func (p *Problems) Add(scope, ref, format string, args ...any) {
	*p = append(*p, Problem{Scope: scope, Ref: ref, Message: fmt.Sprintf(format, args...)})
}

// Sort orders the problems by scope, then reference, then message so that a
// validation report is reproducible.
func (p Problems) Sort() {
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].Scope != p[j].Scope {
			return p[i].Scope < p[j].Scope
		}
		if p[i].Ref != p[j].Ref {
			return p[i].Ref < p[j].Ref
		}
		return p[i].Message < p[j].Message
	})
}

// Err collapses the collection into a single error, or nil when empty. At most
// five complaints appear in the message; the rest are counted.
func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	p.Sort()
	shown := p
	suffix := ""
	if len(shown) > 5 {
		suffix = fmt.Sprintf(" (and %d more)", len(shown)-5)
		shown = shown[:5]
	}
	parts := make([]string, 0, len(shown))
	for _, problem := range shown {
		parts = append(parts, problem.String())
	}
	return fmt.Errorf("%d validation problem(s): %s%s", len(p), strings.Join(parts, "; "), suffix)
}
