// Package alert holds the alert lifecycle.
//
// An alert is not a message, it is a state that persists between runs. Each run
// takes the previous alert ledger and the current corroborated grades and
// advances the machine one step per site:
//
//	(nothing) -> open        the grade first reaches the opening threshold
//	open      -> sustained   the grade stays at or above it for enough updates
//	any       -> downgraded  the grade falls below the threshold but not to none
//	any       -> resolved    the grade reaches none for enough consecutive updates
//	downgraded-> open        the grade recovers above the threshold again
//
// Every transition is appended to the alert's history with the instant and the
// reason, so the shape of an episode can be read back without rerunning
// anything. A resolved site is suppressed for a cooldown period, which stops one
// noisy site from producing a new alert every day.
//
// Identifiers are derived, not generated: the digest of the site, the opening
// instant and the configuration fingerprint. The same input therefore produces
// the same alert identifier on every machine and in every run.
package alert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"FluWatershed/internal/config"
	"FluWatershed/internal/corroborate"
	"FluWatershed/internal/numeric"
	"FluWatershed/internal/timeutil"
)

// State is where an alert sits in its lifecycle.
type State string

// The four alert states.
const (
	StateOpen       State = "open"
	StateSustained  State = "sustained"
	StateDowngraded State = "downgraded"
	StateResolved   State = "resolved"
)

// Active reports whether the state is still a live episode.
func (s State) Active() bool { return s != StateResolved }

// Kind labels why a history entry was written.
type Kind string

// The history entry kinds.
const (
	KindStateChange Kind = "state_change"
	KindGradeChange Kind = "grade_change"
	KindSuppressed  Kind = "suppressed"
)

// Transition is one entry in an alert's history.
type Transition struct {
	Kind   Kind              `json:"kind"`
	From   State             `json:"from_state"`
	To     State             `json:"to_state"`
	Grade  corroborate.Grade `json:"grade"`
	Score  float64           `json:"score"`
	At     timeutil.Stamp    `json:"at"`
	Reason string            `json:"reason"`
}

// Alert is one episode at one site.
type Alert struct {
	AlertID         string            `json:"alert_id"`
	SiteID          string            `json:"site_id"`
	State           State             `json:"state"`
	Grade           corroborate.Grade `json:"grade"`
	PeakGrade       corroborate.Grade `json:"peak_grade"`
	Score           float64           `json:"score"`
	PeakScore       float64           `json:"peak_score"`
	OpenedAt        timeutil.Stamp    `json:"opened_at"`
	UpdatedAt       timeutil.Stamp    `json:"updated_at"`
	ResolvedAt      timeutil.Stamp    `json:"resolved_at"`
	Updates         int               `json:"updates"`
	AtOrAboveRun    int               `json:"consecutive_at_or_above"`
	BelowRun        int               `json:"consecutive_below"`
	SuppressedUntil timeutil.Stamp    `json:"suppressed_until"`
	Suppressions    int               `json:"suppressions"`
	History         []Transition      `json:"history"`
	Rationale       string            `json:"rationale"`
}

// Ledger is the persisted set of alerts.
type Ledger struct {
	SchemaVersion string  `json:"schema_version"`
	Fingerprint   string  `json:"config_fingerprint"`
	Alerts        []Alert `json:"alerts"`
}

// LedgerSchemaVersion tags a persisted alert ledger.
const LedgerSchemaVersion = "fluwatershed-alerts/v1"

// NewLedger returns an empty ledger tagged for the given configuration.
func NewLedger(cfg config.Config) Ledger {
	return Ledger{SchemaVersion: LedgerSchemaVersion, Fingerprint: cfg.Fingerprint()}
}

// DeriveID builds the deterministic identifier for an episode.
func DeriveID(cfg config.Config, siteID string, openedAt timeutil.Stamp) string {
	payload := fmt.Sprintf("%s|%s|%s", siteID, openedAt.String(), cfg.Fingerprint())
	sum := sha256.Sum256([]byte(payload))
	width := cfg.Alerting.IdentifierBytes
	if width < 4 {
		width = 4
	}
	if width > len(sum) {
		width = len(sum)
	}
	return "ALR-" + hex.EncodeToString(sum[:width])
}

// Advance runs one lifecycle step for every site that has a corroborated signal,
// carrying forward any prior alerts that the signals do not mention.
func Advance(cfg config.Config, prior Ledger, signals []corroborate.Signal,
	asOf timeutil.Stamp) (Ledger, error) {
	openGrade, err := corroborate.ParseGrade(cfg.Alerting.OpenGrade)
	if err != nil {
		return Ledger{}, fmt.Errorf("alerting.open_grade: %w", err)
	}
	bySite := make(map[string][]Alert)
	for _, item := range prior.Alerts {
		bySite[item.SiteID] = append(bySite[item.SiteID], item)
	}
	for site := range bySite {
		sortEpisodes(bySite[site])
	}
	touched := make(map[string]bool, len(signals))
	next := NewLedger(cfg)
	for _, signal := range signals {
		touched[signal.SiteID] = true
		episodes := bySite[signal.SiteID]
		next.Alerts = append(next.Alerts, step(cfg, openGrade, episodes, signal, asOf)...)
	}
	untouched := make([]string, 0, len(bySite))
	for site := range bySite {
		if !touched[site] {
			untouched = append(untouched, site)
		}
	}
	sort.Strings(untouched)
	for _, site := range untouched {
		next.Alerts = append(next.Alerts, bySite[site]...)
	}
	SortAlerts(next.Alerts)
	return next, nil
}

// step advances one site. episodes are that site's prior alerts, oldest first.
func step(cfg config.Config, openGrade corroborate.Grade, episodes []Alert,
	signal corroborate.Signal, asOf timeutil.Stamp) []Alert {
	out := make([]Alert, 0, len(episodes)+1)
	out = append(out, episodes...)
	meets := corroborate.Rank(signal.Grade) >= corroborate.Rank(openGrade)
	activeIndex := -1
	for index := range out {
		if out[index].State.Active() {
			activeIndex = index
		}
	}
	if activeIndex < 0 {
		if !meets {
			return out
		}
		if index, until, blocked := cooldownBlock(cfg, out, asOf); blocked {
			out[index].Suppressions++
			out[index].SuppressedUntil = until
			out[index].History = append(out[index].History, Transition{
				Kind:  KindSuppressed,
				From:  StateResolved,
				To:    StateResolved,
				Grade: signal.Grade,
				Score: signal.Score,
				At:    asOf,
				Reason: fmt.Sprintf("grade %s would open a new alert but the %.1f hour cooldown runs until %s",
					signal.Grade, cfg.Alerting.CooldownHours, until),
			})
			return out
		}
		return append(out, open(cfg, signal, asOf))
	}
	out[activeIndex] = update(cfg, openGrade, out[activeIndex], signal, asOf, meets)
	return out
}

func open(cfg config.Config, signal corroborate.Signal, asOf timeutil.Stamp) Alert {
	created := Alert{
		AlertID:      DeriveID(cfg, signal.SiteID, asOf),
		SiteID:       signal.SiteID,
		State:        StateOpen,
		Grade:        signal.Grade,
		PeakGrade:    signal.Grade,
		Score:        numeric.Round4(signal.Score),
		PeakScore:    numeric.Round4(signal.Score),
		OpenedAt:     asOf,
		UpdatedAt:    asOf,
		Updates:      1,
		AtOrAboveRun: 1,
	}
	created.History = append(created.History, Transition{
		Kind:  KindStateChange,
		From:  "",
		To:    StateOpen,
		Grade: signal.Grade,
		Score: created.Score,
		At:    asOf,
		Reason: fmt.Sprintf("grade %s reached the %s opening threshold with %d contributing stream(s)",
			signal.Grade, cfg.Alerting.OpenGrade, signal.PresentStreams),
	})
	created.Rationale = signal.Rationale
	return created
}

func update(cfg config.Config, openGrade corroborate.Grade, current Alert,
	signal corroborate.Signal, asOf timeutil.Stamp, meets bool) Alert {
	previousState := current.State
	previousGrade := current.Grade
	current.Updates++
	current.UpdatedAt = asOf
	current.Grade = signal.Grade
	current.Score = numeric.Round4(signal.Score)
	current.Rationale = signal.Rationale
	if corroborate.Rank(signal.Grade) > corroborate.Rank(current.PeakGrade) {
		current.PeakGrade = signal.Grade
	}
	if current.Score > current.PeakScore {
		current.PeakScore = current.Score
	}
	reason := ""
	if meets {
		current.AtOrAboveRun++
		current.BelowRun = 0
		switch {
		case previousState == StateDowngraded:
			current.State = StateOpen
			reason = fmt.Sprintf("grade recovered to %s, at or above the %s threshold",
				signal.Grade, cfg.Alerting.OpenGrade)
		case current.AtOrAboveRun >= cfg.Alerting.SustainAfterUpdates && previousState != StateSustained:
			current.State = StateSustained
			reason = fmt.Sprintf("grade held at or above %s for %d consecutive update(s), meeting the %d update rule",
				cfg.Alerting.OpenGrade, current.AtOrAboveRun, cfg.Alerting.SustainAfterUpdates)
		}
	} else {
		current.BelowRun++
		current.AtOrAboveRun = 0
		switch {
		case signal.Grade == corroborate.GradeNone && current.BelowRun >= cfg.Alerting.ResolveAfterUpdates:
			current.State = StateResolved
			current.ResolvedAt = asOf
			reason = fmt.Sprintf("no evidence for %d consecutive update(s), meeting the %d update resolution rule",
				current.BelowRun, cfg.Alerting.ResolveAfterUpdates)
		case previousState != StateDowngraded:
			current.State = StateDowngraded
			reason = fmt.Sprintf("grade fell to %s, below the %s threshold, on update %d",
				signal.Grade, cfg.Alerting.OpenGrade, current.BelowRun)
		}
	}
	if current.State != previousState {
		current.History = append(current.History, Transition{
			Kind:   KindStateChange,
			From:   previousState,
			To:     current.State,
			Grade:  signal.Grade,
			Score:  current.Score,
			At:     asOf,
			Reason: reason,
		})
	} else if previousGrade != signal.Grade {
		current.History = append(current.History, Transition{
			Kind:  KindGradeChange,
			From:  previousState,
			To:    current.State,
			Grade: signal.Grade,
			Score: current.Score,
			At:    asOf,
			Reason: fmt.Sprintf("grade moved from %s to %s while remaining %s",
				previousGrade, signal.Grade, current.State),
		})
	}
	return current
}

// cooldownBlock reports whether a freshly resolved episode still suppresses new
// alerts, returning the index of that episode and when the suppression lifts.
func cooldownBlock(cfg config.Config, episodes []Alert, asOf timeutil.Stamp) (int, timeutil.Stamp, bool) {
	if cfg.Alerting.CooldownHours <= 0 {
		return -1, timeutil.Stamp{}, false
	}
	best := -1
	for index := range episodes {
		if episodes[index].State != StateResolved || !episodes[index].ResolvedAt.IsSet() {
			continue
		}
		if best < 0 || episodes[index].ResolvedAt.After(episodes[best].ResolvedAt) {
			best = index
		}
	}
	if best < 0 {
		return -1, timeutil.Stamp{}, false
	}
	until := episodes[best].ResolvedAt.AddHours(cfg.Alerting.CooldownHours)
	if asOf.Before(until) {
		return best, until, true
	}
	return -1, timeutil.Stamp{}, false
}

func sortEpisodes(episodes []Alert) {
	sort.SliceStable(episodes, func(i, j int) bool {
		if !episodes[i].OpenedAt.Equal(episodes[j].OpenedAt) {
			return episodes[i].OpenedAt.Before(episodes[j].OpenedAt)
		}
		return episodes[i].AlertID < episodes[j].AlertID
	})
}

// SortAlerts puts alerts into canonical order: site, then opening instant, then
// identifier.
func SortAlerts(alerts []Alert) {
	sort.SliceStable(alerts, func(i, j int) bool {
		left, right := alerts[i], alerts[j]
		if left.SiteID != right.SiteID {
			return left.SiteID < right.SiteID
		}
		if !left.OpenedAt.Equal(right.OpenedAt) {
			return left.OpenedAt.Before(right.OpenedAt)
		}
		return left.AlertID < right.AlertID
	})
}

// Active returns the alerts that are not resolved.
func (l Ledger) Active() []Alert {
	out := make([]Alert, 0, len(l.Alerts))
	for _, item := range l.Alerts {
		if item.State.Active() {
			out = append(out, item)
		}
	}
	return out
}

// Counts summarises a ledger by state.
type Counts struct {
	Total      int `json:"total"`
	Open       int `json:"open"`
	Sustained  int `json:"sustained"`
	Downgraded int `json:"downgraded"`
	Resolved   int `json:"resolved"`
}

// Count tallies the ledger by state.
func (l Ledger) Count() Counts {
	counts := Counts{Total: len(l.Alerts)}
	for _, item := range l.Alerts {
		switch item.State {
		case StateOpen:
			counts.Open++
		case StateSustained:
			counts.Sustained++
		case StateDowngraded:
			counts.Downgraded++
		case StateResolved:
			counts.Resolved++
		}
	}
	return counts
}

// Describe renders one alert as a single text line.
func (a Alert) Describe() string {
	return fmt.Sprintf("%-20s %-14s %-11s grade %-9s peak %-9s score %6.4f updates %2d opened %s",
		a.AlertID, a.SiteID, a.State, a.Grade, a.PeakGrade, a.Score, a.Updates, a.OpenedAt)
}
