package report

import (
	"fmt"
	"strconv"

	"FluWatershed/internal/alert"
	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/corroborate"
	"FluWatershed/internal/pipeline"
	"FluWatershed/internal/store"
)

// Topology renders the catchment structure.
func Topology(graph *catchment.Graph) []string {
	table := NewTable("SITE", "MATRIX", "DIRECT UPSTREAM", "DIRECT DOWNSTREAM", "FLOW L/DAY", "POPULATION").
		RightAlign(4, 5)
	for _, id := range graph.Order() {
		site, ok := graph.Site(id)
		if !ok {
			continue
		}
		table.Add(
			id,
			string(site.Matrix),
			JoinOrDash(graph.DirectUpstream(id)),
			JoinOrDash(graph.DirectDownstream(id)),
			Sci(site.FlowLitresPerDay()),
			strconv.Itoa(site.ServedPopulation),
		)
	}
	return Section("Catchment topology (upstream first)", table.Render())
}

// RollUps renders the propagated loads.
func RollUps(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "DAY", "OWN LOAD", "UPSTREAM LOAD", "TOTAL LOAD", "TOTAL FLOW",
		"IMPLIED C", "UPSTREAM", "FLAGGED UP", "MOST UPSTREAM").RightAlign(2, 3, 4, 5, 6)
	for _, rollUp := range analysis.RollUps {
		table.Add(
			rollUp.SiteID,
			Dash(rollUp.Day),
			Sci(rollUp.OwnLoadCopiesPerDay),
			Sci(rollUp.UpstreamLoad),
			Sci(rollUp.TotalLoad),
			Sci(rollUp.TotalFlow),
			Sci(rollUp.ImpliedConcentration),
			strconv.Itoa(len(rollUp.UpstreamSites)),
			JoinOrDash(rollUp.FlaggedUpstream),
			JoinOrDash(rollUp.MostUpstreamFlagged),
		)
	}
	lines := Section(fmt.Sprintf("Catchment roll-up (%d node(s))", table.Rows()), table.Render())
	contributionTable := NewTable("NODE", "CONTRIBUTOR", "SELF", "LOAD/DAY", "FLOW L/DAY",
		"FLOW SHARE", "LOAD SHARE", "TRAVEL H", "FLAGGED").RightAlign(3, 4, 5, 6, 7)
	for _, rollUp := range analysis.RollUps {
		for _, contribution := range rollUp.Contributions {
			contributionTable.Add(
				rollUp.SiteID,
				contribution.SiteID,
				YesNo(contribution.Self),
				Sci(contribution.LoadCopiesPerDay),
				Sci(contribution.FlowLitresPerDay),
				Fixed(contribution.FlowShare, 4),
				Fixed(contribution.LoadShare, 4),
				Fixed(contribution.TravelHours, 2),
				YesNo(contribution.Flagged),
			)
		}
	}
	lines = append(lines, Section("Contribution shares", contributionTable.Render())...)
	attributionTable := NewTable("SITE", "ATTRIBUTION")
	for _, rollUp := range analysis.RollUps {
		attributionTable.Add(rollUp.SiteID, rollUp.Attribution)
	}
	return append(lines, Section("Upstream attribution", attributionTable.Render())...)
}

// States renders the reduced per-site state used by the roll-up.
func States(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "MATRIX", "DAY", "COPIES/L", "FLOW L/DAY", "LOAD/DAY",
		"OBSERVED", "DETECTED", "FLAGGED", "SUSTAINED").RightAlign(3, 4, 5)
	for _, state := range analysis.States {
		table.Add(
			state.SiteID,
			string(state.Matrix),
			Dash(state.Day),
			Sci(state.Concentration),
			Sci(state.FlowLitresPerDay),
			Sci(state.LoadCopiesPerDay),
			YesNo(state.Observed),
			YesNo(state.Detected),
			YesNo(state.Flagged),
			YesNo(state.SustainedRise),
		)
	}
	return Section(fmt.Sprintf("Site states (%d)", table.Rows()), table.Render())
}

// Corroboration renders the graded signals and the evidence behind them.
func Corroboration(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "GRADE", "RAW GRADE", "SCORE", "APPLICABLE", "PRESENT",
		"MIN MET", "CAPPED").RightAlign(3, 4, 5)
	for _, signal := range analysis.Signals {
		table.Add(
			signal.SiteID,
			string(signal.Grade),
			string(signal.RawGrade),
			Fixed(signal.Score, 4),
			strconv.Itoa(signal.ApplicableStreams),
			strconv.Itoa(signal.PresentStreams),
			YesNo(signal.MinStreamsMet),
			YesNo(signal.Capped),
		)
	}
	lines := Section(fmt.Sprintf("Corroborated signals (%d site(s))", table.Rows()), table.Render())
	evidenceTable := NewTable("SITE", "STREAM", "APPLICABLE", "PRESENT", "STRENGTH", "WEIGHT",
		"CONTRIBUTION", "SOURCES", "DETAIL").RightAlign(4, 5, 6)
	for _, signal := range analysis.Signals {
		for _, evidence := range signal.Evidence {
			sources := evidence.SourceSites
			if len(evidence.SourceEvents) > 0 {
				sources = append(append([]string{}, sources...), evidence.SourceEvents...)
			}
			evidenceTable.Add(
				signal.SiteID,
				string(evidence.Stream),
				YesNo(evidence.Applicable),
				YesNo(evidence.Present),
				Fixed(evidence.Strength, 2),
				Fixed(evidence.Weight, 2),
				Fixed(evidence.Contribution, 4),
				JoinOrDash(sources),
				evidence.Detail,
			)
		}
	}
	lines = append(lines, Section("Evidence streams", evidenceTable.Render())...)
	rationaleTable := NewTable("SITE", "RATIONALE")
	for _, signal := range analysis.Signals {
		rationaleTable.Add(signal.SiteID, signal.Rationale)
	}
	return append(lines, Section("Grade rationale", rationaleTable.Render())...)
}

// Alerts renders the alert ledger and every transition in it.
func Alerts(ledger alert.Ledger) []string {
	counts := ledger.Count()
	lines := Section("Alert ledger", KeyValues([][2]string{
		{"total", strconv.Itoa(counts.Total)},
		{"open", strconv.Itoa(counts.Open)},
		{"sustained", strconv.Itoa(counts.Sustained)},
		{"downgraded", strconv.Itoa(counts.Downgraded)},
		{"resolved", strconv.Itoa(counts.Resolved)},
		{"fingerprint", ledger.Fingerprint},
	}))
	table := NewTable("ALERT", "SITE", "STATE", "GRADE", "PEAK", "SCORE", "PEAK SCORE",
		"UPDATES", "RUN", "BELOW", "OPENED", "UPDATED", "RESOLVED", "SUPPRESSED").
		RightAlign(5, 6, 7, 8, 9, 13)
	for _, item := range ledger.Alerts {
		table.Add(
			item.AlertID,
			item.SiteID,
			string(item.State),
			string(item.Grade),
			string(item.PeakGrade),
			Fixed(item.Score, 4),
			Fixed(item.PeakScore, 4),
			strconv.Itoa(item.Updates),
			strconv.Itoa(item.AtOrAboveRun),
			strconv.Itoa(item.BelowRun),
			item.OpenedAt.String(),
			item.UpdatedAt.String(),
			Dash(item.ResolvedAt.String()),
			strconv.Itoa(item.Suppressions),
		)
	}
	lines = append(lines, Section("Alerts", table.Render())...)
	historyTable := NewTable("ALERT", "SEQ", "KIND", "FROM", "TO", "GRADE", "SCORE", "AT", "REASON").
		RightAlign(1, 6)
	for _, item := range ledger.Alerts {
		for index, transition := range item.History {
			historyTable.Add(
				item.AlertID,
				strconv.Itoa(index+1),
				string(transition.Kind),
				Dash(string(transition.From)),
				string(transition.To),
				string(transition.Grade),
				Fixed(transition.Score, 4),
				transition.At.String(),
				transition.Reason,
			)
		}
	}
	return append(lines, Section("Transition history", historyTable.Render())...)
}

// Audit renders the audit chain and its verification outcome.
func Audit(entries []store.AuditEntry, report store.AuditReport, path string) []string {
	lines := Section("Audit chain", KeyValues([][2]string{
		{"file", path},
		{"entries", strconv.Itoa(report.Entries)},
		{"verified", YesNo(report.Verified)},
		{"chronological", YesNo(report.Chronological)},
		{"head hash", Dash(report.HeadHash)},
		{"first bad sequence", strconv.Itoa(report.FirstBadAt)},
	}))
	table := NewTable("SEQ", "AT", "ACTION", "RECORDS", "PAYLOAD", "PREVIOUS", "HASH", "DETAIL").
		RightAlign(0, 3)
	for _, entry := range entries {
		table.Add(
			strconv.Itoa(entry.Sequence),
			entry.At.String(),
			entry.Action,
			strconv.Itoa(entry.Records),
			shortHash(entry.PayloadHash),
			shortHash(entry.PreviousHash),
			shortHash(entry.Hash),
			entry.Detail,
		)
	}
	lines = append(lines, Section("Entries", table.Render())...)
	if len(report.Problems) > 0 {
		problemTable := NewTable("PROBLEM")
		for _, problem := range report.Problems {
			problemTable.Add(problem)
		}
		lines = append(lines, Section("Chain problems", problemTable.Render())...)
	}
	if len(report.Notes) > 0 {
		noteTable := NewTable("NOTE")
		for _, note := range report.Notes {
			noteTable.Add(note)
		}
		lines = append(lines, Section("Chain notes", noteTable.Render())...)
	}
	return lines
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return Dash(hash)
	}
	return hash[:12]
}

// Inventory renders the store's file listing.
func Inventory(inventory store.Inventory) []string {
	table := NewTable("FILE", "PRESENT", "BYTES").RightAlign(2)
	for _, file := range inventory.Files {
		table.Add(file.Name, YesNo(file.Present), strconv.FormatInt(file.Bytes, 10))
	}
	return Section("Store inventory: "+inventory.Root, table.Render())
}

// Combined renders the full report: every section in a fixed order.
func Combined(cfg config.Config, analysis pipeline.Analysis, ledger alert.Ledger,
	graph *catchment.Graph, entries []store.AuditEntry, auditReport store.AuditReport,
	auditPath string) []string {
	lines := Header(analysis, cfg)
	lines = append(lines, Section("Active profile", cfg.Describe())...)
	lines = append(lines, Topology(graph)...)
	lines = append(lines, Quantification(analysis)...)
	lines = append(lines, Quality(analysis)...)
	lines = append(lines, Normalisation(analysis)...)
	lines = append(lines, Series(analysis)...)
	lines = append(lines, Baselines(analysis)...)
	lines = append(lines, Detection(analysis)...)
	lines = append(lines, Exceedances(analysis)...)
	lines = append(lines, States(analysis)...)
	lines = append(lines, RollUps(analysis)...)
	lines = append(lines, Corroboration(analysis)...)
	lines = append(lines, Alerts(ledger)...)
	if auditPath != "" {
		lines = append(lines, Audit(entries, auditReport, auditPath)...)
	}
	lines = append(lines, Section("Standing caution", []string{
		"FluWatershed is a study tool for exploring environmental surveillance arithmetic.",
		"It provides no public-health, clinical, veterinary or regulatory advice, and its",
		"output must not be used to make real outbreak decisions. All bundled data is fictional.",
	})...)
	return lines
}

// Grades renders a compact grade tally, weakest first.
func Grades(signals []corroborate.Signal) []string {
	counts := make(map[corroborate.Grade]int, len(corroborate.Grades()))
	for _, signal := range signals {
		counts[signal.Grade]++
	}
	table := NewTable("GRADE", "SITES").RightAlign(1)
	for _, grade := range corroborate.Grades() {
		table.Add(string(grade), strconv.Itoa(counts[grade]))
	}
	return Section("Grade distribution", table.Render())
}
