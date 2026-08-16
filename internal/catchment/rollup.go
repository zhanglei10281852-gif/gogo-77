package catchment

import (
	"fmt"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

// SiteState is the current condition of one site, reduced to the few numbers a
// roll-up needs. It is built from the most recent quality-passing day.
type SiteState struct {
	SiteID           string         `json:"site_id"`
	Matrix           model.Matrix   `json:"matrix"`
	Day              string         `json:"day"`
	At               timeutil.Stamp `json:"at"`
	Concentration    float64        `json:"copies_per_litre"`
	FlowLitresPerDay float64        `json:"flow_litres_per_day"`
	LoadCopiesPerDay float64        `json:"load_copies_per_day"`
	HasFlow          bool           `json:"has_flow"`
	Observed         bool           `json:"observed"`
	Detected         bool           `json:"detected"`
	Flagged          bool           `json:"flagged"`
	SustainedRise    bool           `json:"sustained_rise"`
}

// BuildStates reduces normalised points and site trends into one state per site
// in the graph. A site with no usable observation still appears, marked
// unobserved, so that a silent monitoring point is visible in the roll-up.
func BuildStates(cfg config.Config, graph *Graph, points []normalize.Point,
	trends []trend.SiteTrend) []SiteState {
	trendBySite := trend.Index(trends)
	latestDay := make(map[string]string)
	byDay := make(map[string][]normalize.Point)
	for _, point := range points {
		if !point.Usable {
			continue
		}
		if point.Day > latestDay[point.SiteID] {
			latestDay[point.SiteID] = point.Day
		}
		byDay[point.SiteID+"|"+point.Day] = append(byDay[point.SiteID+"|"+point.Day], point)
	}
	digits := cfg.Quantification.ReportSignificantDigits
	states := make([]SiteState, 0, len(graph.SiteIDs()))
	for _, id := range graph.SiteIDs() {
		site, ok := graph.Site(id)
		if !ok {
			continue
		}
		state := SiteState{SiteID: id, Matrix: site.Matrix}
		if siteTrend, has := trendBySite[id]; has {
			state.Flagged = siteTrend.LatestExceeded
			state.SustainedRise = siteTrend.SustainedRise
		}
		day := latestDay[id]
		bucket := byDay[id+"|"+day]
		if day == "" || len(bucket) == 0 {
			states = append(states, state)
			continue
		}
		state.Observed = true
		state.Day = day
		state.At = bucket[0].CollectedAt.StartOfDay()
		concentrations := make([]float64, 0, len(bucket))
		loads := make([]float64, 0, len(bucket))
		flows := make([]float64, 0, len(bucket))
		for _, point := range bucket {
			concentrations = append(concentrations, point.CorrectedCopiesPerLitre)
			if point.Detected {
				state.Detected = true
			}
			if point.LoadCopiesPerDay > 0 {
				loads = append(loads, point.LoadCopiesPerDay)
			}
			if point.FlowLitresPerDay > 0 {
				flows = append(flows, point.FlowLitresPerDay)
			}
		}
		if mean, err := numeric.Mean(concentrations); err == nil {
			state.Concentration = numeric.RoundSignificant(mean, digits)
		}
		if mean, err := numeric.Mean(flows); err == nil {
			state.FlowLitresPerDay = numeric.RoundSignificant(mean, digits)
			state.HasFlow = true
		} else if site.MeanDailyFlowM3 > 0 {
			state.FlowLitresPerDay = numeric.RoundSignificant(site.FlowLitresPerDay(), digits)
			state.HasFlow = true
		}
		switch {
		case len(loads) > 0:
			if mean, err := numeric.Mean(loads); err == nil {
				state.LoadCopiesPerDay = numeric.RoundSignificant(mean, digits)
			}
		case state.HasFlow:
			state.LoadCopiesPerDay = numeric.RoundSignificant(state.Concentration*state.FlowLitresPerDay, digits)
		}
		states = append(states, state)
	}
	sort.SliceStable(states, func(i, j int) bool { return states[i].SiteID < states[j].SiteID })
	return states
}

// Contribution is one site's share of a downstream node's rolled-up load.
type Contribution struct {
	SiteID           string  `json:"site_id"`
	LoadCopiesPerDay float64 `json:"load_copies_per_day"`
	FlowLitresPerDay float64 `json:"flow_litres_per_day"`
	FlowShare        float64 `json:"flow_share"`
	LoadShare        float64 `json:"load_share"`
	TravelHours      float64 `json:"travel_hours"`
	Flagged          bool    `json:"flagged"`
	Detected         bool    `json:"detected"`
	Self             bool    `json:"self"`
}

// RollUp is the propagated picture at one node.
type RollUp struct {
	SiteID               string         `json:"site_id"`
	Matrix               model.Matrix   `json:"matrix"`
	Day                  string         `json:"day"`
	OwnLoadCopiesPerDay  float64        `json:"own_load_copies_per_day"`
	UpstreamLoad         float64        `json:"upstream_load_copies_per_day"`
	TotalLoad            float64        `json:"total_load_copies_per_day"`
	OwnFlow              float64        `json:"own_flow_litres_per_day"`
	TotalFlow            float64        `json:"total_flow_litres_per_day"`
	ImpliedConcentration float64        `json:"implied_concentration_copies_per_litre"`
	Contributions        []Contribution `json:"contributions"`
	UpstreamSites        []string       `json:"upstream_sites"`
	FlaggedUpstream      []string       `json:"flagged_upstream_sites"`
	MostUpstreamFlagged  []string       `json:"most_upstream_flagged_sites"`
	Attribution          string         `json:"attribution"`
	Detected             bool           `json:"detected"`
	Flagged              bool           `json:"flagged"`
}

// Propagate rolls loads downstream through the topology.
//
// Loads are additive, so the total at a node is its own load plus the loads of
// every site transitively upstream. Two shares are reported for each
// contributor: its share of the combined flow, which says how much of the water
// at the node came from there, and its share of the combined load, which says
// how much of the signal did. A contributor with a large load share and a small
// flow share is the interesting case, because that is a concentrated source.
func Propagate(cfg config.Config, graph *Graph, states []SiteState) []RollUp {
	byID := make(map[string]SiteState, len(states))
	for _, state := range states {
		byID[state.SiteID] = state
	}
	digits := cfg.Quantification.ReportSignificantDigits
	out := make([]RollUp, 0, len(states))
	for _, id := range graph.Order() {
		state := byID[id]
		site, _ := graph.Site(id)
		ancestors := graph.Ancestors(id)
		rollUp := RollUp{
			SiteID:              id,
			Matrix:              site.Matrix,
			Day:                 state.Day,
			OwnLoadCopiesPerDay: state.LoadCopiesPerDay,
			OwnFlow:             state.FlowLitresPerDay,
			UpstreamSites:       ancestors,
			Detected:            state.Detected,
			Flagged:             state.Flagged,
		}
		totalLoad := state.LoadCopiesPerDay
		totalFlow := state.FlowLitresPerDay
		upstreamLoad := 0.0
		for _, ancestor := range ancestors {
			upstreamState := byID[ancestor]
			upstreamLoad += upstreamState.LoadCopiesPerDay
			totalLoad += upstreamState.LoadCopiesPerDay
			totalFlow += upstreamState.FlowLitresPerDay
			if upstreamState.Detected {
				rollUp.Detected = true
			}
			if upstreamState.Flagged {
				rollUp.FlaggedUpstream = append(rollUp.FlaggedUpstream, ancestor)
			}
		}
		rollUp.UpstreamLoad = numeric.RoundSignificant(upstreamLoad, digits)
		rollUp.TotalLoad = numeric.RoundSignificant(totalLoad, digits)
		rollUp.TotalFlow = numeric.RoundSignificant(totalFlow, digits)
		rollUp.ImpliedConcentration = numeric.RoundSignificant(
			numeric.SafeDiv(totalLoad, totalFlow, 0), digits)
		participants := append([]string{id}, ancestors...)
		sort.Strings(participants)
		for _, participant := range participants {
			partState := byID[participant]
			travel := 0.0
			if participant != id {
				if hours, ok := graph.TravelHours(participant, id); ok {
					travel = hours
				}
			}
			rollUp.Contributions = append(rollUp.Contributions, Contribution{
				SiteID:           participant,
				LoadCopiesPerDay: partState.LoadCopiesPerDay,
				FlowLitresPerDay: partState.FlowLitresPerDay,
				FlowShare:        numeric.Round4(numeric.SafeDiv(partState.FlowLitresPerDay, totalFlow, 0)),
				LoadShare:        numeric.Round4(numeric.SafeDiv(partState.LoadCopiesPerDay, totalLoad, 0)),
				TravelHours:      travel,
				Flagged:          partState.Flagged,
				Detected:         partState.Detected,
				Self:             participant == id,
			})
		}
		sort.Strings(rollUp.FlaggedUpstream)
		rollUp.MostUpstreamFlagged, rollUp.Attribution = attribute(cfg, graph, byID, id)
		out = append(out, rollUp)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SiteID < out[j].SiteID })
	return out
}

// attribute finds the most-upstream flagged site that could plausibly explain a
// flag at target.
//
// A flagged ancestor is consistent when the delay between the two flagged days
// is compatible with the nominal travel time along the shortest path, within the
// configured tolerance. Among the consistent candidates, the most upstream are
// those with no consistent candidate of their own upstream; those are the sites
// worth visiting first.
func attribute(cfg config.Config, graph *Graph, states map[string]SiteState, target string) ([]string, string) {
	targetState := states[target]
	if !targetState.Flagged {
		return nil, "site is not flagged, so nothing to attribute"
	}
	tolerance := cfg.Detection.UpstreamTravelToleranceHr
	consistent := make(map[string]bool)
	for _, ancestor := range graph.Ancestors(target) {
		state := states[ancestor]
		if !state.Flagged || !state.Observed || !targetState.Observed {
			continue
		}
		travel, ok := graph.TravelHours(ancestor, target)
		if !ok {
			continue
		}
		delay := targetState.At.HoursSince(state.At)
		if delay < -tolerance {
			continue
		}
		if delay > travel+tolerance+24 {
			continue
		}
		consistent[ancestor] = true
	}
	if len(consistent) == 0 {
		if targetState.Flagged {
			return []string{target}, fmt.Sprintf(
				"no flagged upstream site is consistent within %.1f hour(s) of travel time, so %s is the most upstream flag",
				tolerance, target)
		}
		return nil, "no consistent upstream flag"
	}
	mostUpstream := make([]string, 0, len(consistent))
	for candidate := range consistent {
		deeper := false
		for _, ancestor := range graph.Ancestors(candidate) {
			if consistent[ancestor] {
				deeper = true
				break
			}
		}
		if !deeper {
			mostUpstream = append(mostUpstream, candidate)
		}
	}
	sort.Strings(mostUpstream)
	return mostUpstream, fmt.Sprintf(
		"%d consistent flagged upstream site(s); most upstream: %s",
		len(consistent), joinIDs(mostUpstream))
}

func joinIDs(ids []string) string {
	out := ""
	for index, id := range ids {
		if index > 0 {
			out += ", "
		}
		out += id
	}
	if out == "" {
		return "none"
	}
	return out
}

// IndexRollUps maps site identifiers to roll-ups.
func IndexRollUps(rollUps []RollUp) map[string]RollUp {
	out := make(map[string]RollUp, len(rollUps))
	for _, item := range rollUps {
		out[item.SiteID] = item
	}
	return out
}

// IndexStates maps site identifiers to states.
func IndexStates(states []SiteState) map[string]SiteState {
	out := make(map[string]SiteState, len(states))
	for _, item := range states {
		out[item.SiteID] = item
	}
	return out
}
