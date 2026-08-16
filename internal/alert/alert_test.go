package alert

import (
	"strings"
	"testing"

	"FluWatershed/internal/config"
	"FluWatershed/internal/corroborate"
	"FluWatershed/internal/timeutil"
)

func stamp(text string) timeutil.Stamp { return timeutil.MustParse(text) }

func signal(siteID string, grade corroborate.Grade, score float64) corroborate.Signal {
	return corroborate.Signal{
		SiteID:            siteID,
		Grade:             grade,
		RawGrade:          grade,
		Score:             score,
		ApplicableStreams: 3,
		PresentStreams:    2,
		MinStreamsMet:     true,
		Rationale:         "test signal",
	}
}

func advance(t *testing.T, cfg config.Config, prior Ledger, grade corroborate.Grade,
	score float64, at string) Ledger {
	t.Helper()
	next, err := Advance(cfg, prior, []corroborate.Signal{signal("SITE-1", grade, score)}, stamp(at))
	if err != nil {
		t.Fatalf("Advance at %s: %v", at, err)
	}
	return next
}

func only(t *testing.T, ledger Ledger) Alert {
	t.Helper()
	if len(ledger.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1: %+v", len(ledger.Alerts), ledger.Alerts)
	}
	return ledger.Alerts[0]
}

func TestDeriveIDIsDeterministicAndSensitive(t *testing.T) {
	cfg := config.Default()
	at := stamp("2026-03-20T12:00:00Z")
	first := DeriveID(cfg, "SITE-1", at)
	if first != DeriveID(cfg, "SITE-1", at) {
		t.Fatal("two derivations of the same episode differ")
	}
	if !strings.HasPrefix(first, "ALR-") {
		t.Fatalf("identifier = %q", first)
	}
	if len(first) != 4+2*cfg.Alerting.IdentifierBytes {
		t.Fatalf("identifier length = %d", len(first))
	}
	if first == DeriveID(cfg, "SITE-2", at) {
		t.Fatal("two sites share an identifier")
	}
	if first == DeriveID(cfg, "SITE-1", stamp("2026-03-21T12:00:00Z")) {
		t.Fatal("two opening instants share an identifier")
	}
	other := cfg
	other.Detection.BaselineMultiple = 9
	if first == DeriveID(other, "SITE-1", at) {
		t.Fatal("two policies share an identifier")
	}
	narrow := cfg
	narrow.Alerting.IdentifierBytes = 4
	if len(DeriveID(narrow, "SITE-1", at)) != 12 {
		t.Fatalf("narrow identifier = %q", DeriveID(narrow, "SITE-1", at))
	}
}

func TestLifecycleOpenSustainDowngradeResolve(t *testing.T) {
	cfg := config.Default()
	cfg.Alerting.SustainAfterUpdates = 2
	cfg.Alerting.ResolveAfterUpdates = 2
	ledger := NewLedger(cfg)

	// Nothing happens below the opening grade.
	ledger = advance(t, cfg, ledger, corroborate.GradeLow, 0.3, "2026-03-10T12:00:00Z")
	if len(ledger.Alerts) != 0 {
		t.Fatalf("a low grade opened an alert: %+v", ledger.Alerts)
	}

	ledger = advance(t, cfg, ledger, corroborate.GradeModerate, 0.5, "2026-03-12T12:00:00Z")
	current := only(t, ledger)
	if current.State != StateOpen {
		t.Fatalf("state = %s, want open", current.State)
	}
	if current.OpenedAt.String() != "2026-03-12T12:00:00Z" {
		t.Fatalf("opened at %s", current.OpenedAt)
	}
	if current.Updates != 1 || current.AtOrAboveRun != 1 {
		t.Fatalf("counters = %d/%d", current.Updates, current.AtOrAboveRun)
	}
	if len(current.History) != 1 || current.History[0].To != StateOpen {
		t.Fatalf("history = %+v", current.History)
	}
	openedID := current.AlertID

	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.8, "2026-03-13T12:00:00Z")
	current = only(t, ledger)
	if current.State != StateSustained {
		t.Fatalf("state = %s, want sustained", current.State)
	}
	if current.AlertID != openedID {
		t.Fatal("the identifier changed mid episode")
	}
	if current.PeakGrade != corroborate.GradeHigh || current.PeakScore != 0.8 {
		t.Fatalf("peak = %s/%v", current.PeakGrade, current.PeakScore)
	}

	// A quiet update with some evidence left downgrades rather than resolves.
	ledger = advance(t, cfg, ledger, corroborate.GradeLow, 0.25, "2026-03-14T12:00:00Z")
	current = only(t, ledger)
	if current.State != StateDowngraded {
		t.Fatalf("state = %s, want downgraded", current.State)
	}
	if current.BelowRun != 1 || current.AtOrAboveRun != 0 {
		t.Fatalf("counters = %d/%d", current.BelowRun, current.AtOrAboveRun)
	}

	// Recovery reopens the same episode.
	ledger = advance(t, cfg, ledger, corroborate.GradeModerate, 0.5, "2026-03-15T12:00:00Z")
	current = only(t, ledger)
	if current.State != StateOpen {
		t.Fatalf("state = %s, want open again", current.State)
	}
	if current.AlertID != openedID {
		t.Fatal("recovery created a new episode")
	}

	// Two updates with no evidence at all resolve it.
	ledger = advance(t, cfg, ledger, corroborate.GradeNone, 0, "2026-03-16T12:00:00Z")
	if only(t, ledger).State != StateDowngraded {
		t.Fatalf("state = %s after one quiet update", only(t, ledger).State)
	}
	ledger = advance(t, cfg, ledger, corroborate.GradeNone, 0, "2026-03-17T12:00:00Z")
	current = only(t, ledger)
	if current.State != StateResolved {
		t.Fatalf("state = %s, want resolved", current.State)
	}
	if current.ResolvedAt.String() != "2026-03-17T12:00:00Z" {
		t.Fatalf("resolved at %s", current.ResolvedAt)
	}
	if current.State.Active() {
		t.Fatal("a resolved alert reports itself as active")
	}
	states := make([]string, 0, len(current.History))
	for _, transition := range current.History {
		states = append(states, string(transition.To))
	}
	got := strings.Join(states, ">")
	want := "open>sustained>downgraded>open>downgraded>resolved"
	if got != want {
		t.Fatalf("history = %s, want %s", got, want)
	}
	counts := ledger.Count()
	if counts.Total != 1 || counts.Resolved != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	if len(ledger.Active()) != 0 {
		t.Fatal("a resolved ledger still reports an active alert")
	}
}

func TestCooldownSuppressesReopening(t *testing.T) {
	cfg := config.Default()
	cfg.Alerting.ResolveAfterUpdates = 1
	cfg.Alerting.CooldownHours = 72
	ledger := NewLedger(cfg)
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.9, "2026-03-12T12:00:00Z")
	ledger = advance(t, cfg, ledger, corroborate.GradeNone, 0, "2026-03-13T12:00:00Z")
	if only(t, ledger).State != StateResolved {
		t.Fatalf("state = %s, want resolved", only(t, ledger).State)
	}

	// Inside the cooldown a fresh high grade is suppressed, not reopened.
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.9, "2026-03-14T12:00:00Z")
	current := only(t, ledger)
	if current.State != StateResolved {
		t.Fatalf("a new alert opened inside the cooldown: %s", current.State)
	}
	if current.Suppressions != 1 {
		t.Fatalf("suppressions = %d", current.Suppressions)
	}
	if current.SuppressedUntil.String() != "2026-03-16T12:00:00Z" {
		t.Fatalf("suppressed until %s", current.SuppressedUntil)
	}
	last := current.History[len(current.History)-1]
	if last.Kind != KindSuppressed || !strings.Contains(last.Reason, "cooldown") {
		t.Fatalf("history entry = %+v", last)
	}

	// After the cooldown lapses a second episode opens.
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.9, "2026-03-17T12:00:00Z")
	if len(ledger.Alerts) != 2 {
		t.Fatalf("alerts = %d, want a second episode", len(ledger.Alerts))
	}
	SortAlerts(ledger.Alerts)
	second := ledger.Alerts[1]
	if second.State != StateOpen || second.AlertID == ledger.Alerts[0].AlertID {
		t.Fatalf("second episode = %+v", second)
	}
}

func TestCooldownCanBeDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Alerting.ResolveAfterUpdates = 1
	cfg.Alerting.CooldownHours = 0
	ledger := NewLedger(cfg)
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.9, "2026-03-12T12:00:00Z")
	ledger = advance(t, cfg, ledger, corroborate.GradeNone, 0, "2026-03-13T12:00:00Z")
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.9, "2026-03-13T18:00:00Z")
	if len(ledger.Alerts) != 2 {
		t.Fatalf("alerts = %d, want an immediate second episode", len(ledger.Alerts))
	}
}

func TestGradeChangeWithinAStateIsRecorded(t *testing.T) {
	cfg := config.Default()
	cfg.Alerting.SustainAfterUpdates = 9 // never reach sustained
	ledger := NewLedger(cfg)
	ledger = advance(t, cfg, ledger, corroborate.GradeModerate, 0.5, "2026-03-12T12:00:00Z")
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.8, "2026-03-13T12:00:00Z")
	current := only(t, ledger)
	if current.State != StateOpen {
		t.Fatalf("state = %s", current.State)
	}
	if len(current.History) != 2 {
		t.Fatalf("history = %+v", current.History)
	}
	if current.History[1].Kind != KindGradeChange {
		t.Fatalf("second entry kind = %s", current.History[1].Kind)
	}
	// A repeated identical grade adds nothing.
	ledger = advance(t, cfg, ledger, corroborate.GradeHigh, 0.8, "2026-03-14T12:00:00Z")
	if got := len(only(t, ledger).History); got != 2 {
		t.Fatalf("history grew to %d entries on an unchanged update", got)
	}
	if got := only(t, ledger).Updates; got != 3 {
		t.Fatalf("updates = %d", got)
	}
}

func TestUntouchedSitesAreCarriedForward(t *testing.T) {
	cfg := config.Default()
	ledger := NewLedger(cfg)
	first, err := Advance(cfg, ledger, []corroborate.Signal{
		signal("SITE-1", corroborate.GradeHigh, 0.9),
		signal("SITE-2", corroborate.GradeHigh, 0.9),
	}, stamp("2026-03-12T12:00:00Z"))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(first.Alerts) != 2 {
		t.Fatalf("alerts = %d", len(first.Alerts))
	}
	// The second run only mentions SITE-1; SITE-2 must survive unchanged.
	second, err := Advance(cfg, first, []corroborate.Signal{
		signal("SITE-1", corroborate.GradeNone, 0),
	}, stamp("2026-03-13T12:00:00Z"))
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(second.Alerts) != 2 {
		t.Fatalf("alerts = %d, an untouched site was lost", len(second.Alerts))
	}
	for _, item := range second.Alerts {
		if item.SiteID == "SITE-2" && item.Updates != 1 {
			t.Fatalf("SITE-2 was updated without a signal: %+v", item)
		}
	}
	if second.Alerts[0].SiteID != "SITE-1" {
		t.Fatalf("alerts are not sorted by site: %+v", second.Alerts)
	}
}

func TestAdvanceRejectsAnUnknownOpeningGrade(t *testing.T) {
	cfg := config.Default()
	cfg.Alerting.OpenGrade = "extreme"
	if _, err := Advance(cfg, NewLedger(cfg), nil, stamp("2026-03-12T12:00:00Z")); err == nil {
		t.Fatal("an unknown opening grade was accepted")
	}
}

func TestLedgerMetadataAndDescription(t *testing.T) {
	cfg := config.Default()
	ledger := advance(t, cfg, NewLedger(cfg), corroborate.GradeHigh, 0.91, "2026-03-12T12:00:00Z")
	if ledger.SchemaVersion != LedgerSchemaVersion {
		t.Fatalf("schema version = %q", ledger.SchemaVersion)
	}
	if ledger.Fingerprint != cfg.Fingerprint() {
		t.Fatal("the ledger does not carry the policy fingerprint")
	}
	line := only(t, ledger).Describe()
	for _, needle := range []string{"SITE-1", "open", "high"} {
		if !strings.Contains(line, needle) {
			t.Errorf("description %q does not mention %q", line, needle)
		}
	}
	if !StateOpen.Active() || !StateSustained.Active() || !StateDowngraded.Active() {
		t.Error("an unresolved state reported itself inactive")
	}
}
