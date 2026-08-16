package cli

import (
	"fmt"
	"strings"

	"FluWatershed/internal/alert"
	"FluWatershed/internal/catchment"
	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/pipeline"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/report"
	"FluWatershed/internal/store"
	"FluWatershed/internal/timeutil"
	"FluWatershed/internal/trend"
)

// runContext is everything a read-only analysis subcommand needs.
type runContext struct {
	opts     *options
	cfg      config.Config
	bundle   model.Bundle
	asOf     timeutil.Stamp
	format   Format
	source   string
	graph    *catchment.Graph
	analysis pipeline.Analysis
}

// prepare parses the analysis flags, loads and validates the input, and runs the
// pipeline. Validation is not optional: an analysis over invalid input would
// produce numbers nobody should read.
func prepare(name string, env Environment, args []string) (*runContext, error) {
	opts := &options{}
	set := newFlagSet(name, env.Stderr)
	opts.commonFlags(set)
	opts.storeFlag(set)
	opts.sourceFlags(set)
	opts.asOfFlag(set)
	set.StringVar(&opts.siteFilter, "site", "", "restrict detail output to this site")
	set.StringVar(&opts.resultID, "result", "", "restrict detail output to this assay result")
	set.BoolVar(&opts.detail, "detail", false, "include per-day and per-check detail")
	set.BoolVar(&opts.writeStore, "write", false, "persist the computed snapshot into the store")
	if err := set.Parse(args); err != nil {
		return nil, err
	}
	if err := requireNoArgs(set); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, helpFor(name, set))
	}
	format, err := opts.resolveFormat()
	if err != nil {
		return nil, err
	}
	cfg, err := opts.resolveConfig()
	if err != nil {
		return nil, err
	}
	bundle, source, err := opts.loadBundle()
	if err != nil {
		return nil, err
	}
	validation := pipeline.Validate(bundle)
	if err := validation.Err(); err != nil {
		return nil, fmt.Errorf("input from %s did not validate: %w", source, err)
	}
	override, err := opts.resolveAsOf()
	if err != nil {
		return nil, err
	}
	asOf, err := pipeline.ResolveAsOf(bundle, override)
	if err != nil {
		return nil, err
	}
	graph, err := catchment.Build(bundle.Network)
	if err != nil {
		return nil, err
	}
	analysis, err := pipeline.Run(cfg, bundle, asOf)
	if err != nil {
		return nil, err
	}
	return &runContext{
		opts: opts, cfg: cfg, bundle: bundle, asOf: asOf,
		format: format, source: source, graph: graph, analysis: analysis,
	}, nil
}

// deliver writes the chosen representation to the chosen destination.
func (c *runContext) deliver(env Environment, document any, lines []string) error {
	out, closer, err := c.opts.writer(env.Stdout)
	if err != nil {
		return err
	}
	defer func() { _ = closer() }()
	return c.opts.emit(out, c.format, document, lines)
}

func runValidate(env Environment, args []string) (int, error) {
	opts := &options{}
	set := newFlagSet("validate", env.Stderr)
	opts.commonFlags(set)
	opts.storeFlag(set)
	opts.sourceFlags(set)
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	if err := requireNoArgs(set); err != nil {
		return ExitError, fmt.Errorf("%w\n%s", err, helpFor("validate", set))
	}
	format, err := opts.resolveFormat()
	if err != nil {
		return ExitError, err
	}
	if _, err := opts.resolveConfig(); err != nil {
		return ExitError, err
	}
	bundle, source, err := opts.loadBundle()
	if err != nil {
		return ExitError, err
	}
	validation := pipeline.Validate(bundle)
	out, closer, err := opts.writer(env.Stdout)
	if err != nil {
		return ExitError, err
	}
	defer func() { _ = closer() }()
	lines := append([]string{}, report.Validation(validation)...)
	lines = append(lines, fmt.Sprintf("input read from %s", source))
	if err := opts.emit(out, format, validation, lines); err != nil {
		return ExitError, err
	}
	if !validation.Valid {
		return ExitVerdict, nil
	}
	return ExitOK, nil
}

// ingestSummary is the JSON document the ingest subcommand emits.
type ingestSummary struct {
	Store          string             `json:"store"`
	AsOf           timeutil.Stamp     `json:"as_of"`
	CatchmentID    string             `json:"catchment_id"`
	SamplesAdded   int                `json:"samples_added"`
	SamplesSkipped []string           `json:"samples_skipped"`
	ResultsAdded   int                `json:"results_added"`
	ResultsSkipped []string           `json:"results_skipped"`
	EventsAdded    int                `json:"events_added"`
	EventsSkipped  []string           `json:"events_skipped"`
	NetworkWritten bool               `json:"network_written"`
	AuditEntries   []store.AuditEntry `json:"audit_entries"`
	Meta           store.Meta         `json:"meta"`
}

func runIngest(env Environment, args []string) (int, error) {
	opts := &options{}
	set := newFlagSet("ingest", env.Stderr)
	opts.commonFlags(set)
	opts.storeFlag(set)
	opts.sourceFlags(set)
	opts.asOfFlag(set)
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	if err := requireNoArgs(set); err != nil {
		return ExitError, fmt.Errorf("%w\n%s", err, helpFor("ingest", set))
	}
	format, err := opts.resolveFormat()
	if err != nil {
		return ExitError, err
	}
	cfg, err := opts.resolveConfig()
	if err != nil {
		return ExitError, err
	}
	if strings.TrimSpace(opts.networkPath) == "" {
		return ExitError, fmt.Errorf("--network is required: an ingest always states the catchment it belongs to")
	}
	handle, err := opts.resolveStore()
	if err != nil {
		return ExitError, err
	}
	incoming, err := pipeline.Load(pipeline.Sources{
		NetworkPath: opts.networkPath,
		SamplesPath: opts.samplesPath,
		ResultsPath: opts.resultsPath,
		EventsPath:  opts.eventsPath,
	})
	if err != nil {
		return ExitError, err
	}
	if problems := model.ValidateNetwork(incoming.Network); len(problems) > 0 {
		return ExitError, fmt.Errorf("catchment definition did not validate: %w", problems.Err())
	}
	for _, sample := range incoming.Samples {
		if problems := model.ValidateSample(sample); len(problems) > 0 {
			return ExitError, fmt.Errorf("sample %s did not validate: %w", sample.SampleID, problems.Err())
		}
	}
	for _, result := range incoming.Results {
		if problems := model.ValidateResult(result); len(problems) > 0 {
			return ExitError, fmt.Errorf("result %s did not validate: %w", result.ResultID, problems.Err())
		}
	}
	for _, event := range incoming.Events {
		if problems := model.ValidateEvent(event); len(problems) > 0 {
			return ExitError, fmt.Errorf("event %s did not validate: %w", event.EventID, problems.Err())
		}
	}
	// The catchment identity is checked before anything else about the incoming
	// data, because mixing two catchments into one store would corrupt every
	// roll-up that store ever produces.
	existing, found, err := handle.LoadNetwork()
	if err != nil {
		return ExitError, err
	}
	if found && existing.CatchmentID != incoming.Network.CatchmentID {
		return ExitError, fmt.Errorf("store holds catchment %q but the input describes %q",
			existing.CatchmentID, incoming.Network.CatchmentID)
	}
	override, err := opts.resolveAsOf()
	if err != nil {
		return ExitError, err
	}
	asOf, err := pipeline.ResolveAsOf(incoming, override)
	if err != nil {
		return ExitError, fmt.Errorf("%w (an ingest is stamped with --as-of when the input carries no times)", err)
	}
	summary := ingestSummary{Store: handle.Root(), AsOf: asOf, CatchmentID: incoming.Network.CatchmentID}
	networkPayload, err := handle.WriteNetwork(incoming.Network)
	if err != nil {
		return ExitError, err
	}
	summary.NetworkWritten = true
	entry, err := handle.Record(asOf, "write-network",
		fmt.Sprintf("catchment %s with %d site(s) and %d link(s)",
			incoming.Network.CatchmentID, len(incoming.Network.Sites), len(incoming.Network.Links)),
		len(incoming.Network.Sites), networkPayload)
	if err != nil {
		return ExitError, err
	}
	summary.AuditEntries = append(summary.AuditEntries, entry)

	added, skipped, payload, err := handle.AppendSamples(incoming.Samples)
	if err != nil {
		return ExitError, err
	}
	summary.SamplesAdded, summary.SamplesSkipped = added, skipped
	if added > 0 {
		entry, err := handle.Record(asOf, "append-samples",
			fmt.Sprintf("%d sample(s) appended, %d already present", added, len(skipped)), added, payload)
		if err != nil {
			return ExitError, err
		}
		summary.AuditEntries = append(summary.AuditEntries, entry)
	}

	added, skipped, payload, err = handle.AppendResults(incoming.Results)
	if err != nil {
		return ExitError, err
	}
	summary.ResultsAdded, summary.ResultsSkipped = added, skipped
	if added > 0 {
		entry, err := handle.Record(asOf, "append-results",
			fmt.Sprintf("%d result(s) appended, %d already present", added, len(skipped)), added, payload)
		if err != nil {
			return ExitError, err
		}
		summary.AuditEntries = append(summary.AuditEntries, entry)
	}

	added, skipped, payload, err = handle.AppendEvents(incoming.Events)
	if err != nil {
		return ExitError, err
	}
	summary.EventsAdded, summary.EventsSkipped = added, skipped
	if added > 0 {
		entry, err := handle.Record(asOf, "append-events",
			fmt.Sprintf("%d carcass event(s) appended, %d already present", added, len(skipped)), added, payload)
		if err != nil {
			return ExitError, err
		}
		summary.AuditEntries = append(summary.AuditEntries, entry)
	}

	meta, err := handle.Meta()
	if err != nil {
		return ExitError, err
	}
	if !meta.CreatedAt.IsSet() {
		meta.CreatedAt = asOf
		meta.StoreID = incoming.Network.CatchmentID
	}
	meta.UpdatedAt = asOf
	meta.ConfigFingerprint = cfg.Fingerprint()
	meta.CatchmentID = incoming.Network.CatchmentID
	meta.Sites = len(incoming.Network.Sites)
	meta.Ingests++
	stored, err := handle.LoadBundle()
	if err != nil {
		return ExitError, err
	}
	meta.Samples = len(stored.Samples)
	meta.Results = len(stored.Results)
	meta.Events = len(stored.Events)
	if err := handle.WriteMeta(meta); err != nil {
		return ExitError, err
	}
	summary.Meta = meta

	out, closer, err := opts.writer(env.Stdout)
	if err != nil {
		return ExitError, err
	}
	defer func() { _ = closer() }()
	if err := opts.emit(out, format, summary, ingestLines(summary)); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

func ingestLines(summary ingestSummary) []string {
	lines := report.Section("Ingest", report.KeyValues([][2]string{
		{"store", summary.Store},
		{"catchment", summary.CatchmentID},
		{"reporting instant", summary.AsOf.String()},
		{"network written", report.YesNo(summary.NetworkWritten)},
		{"samples added", fmt.Sprintf("%d (skipped %d)", summary.SamplesAdded, len(summary.SamplesSkipped))},
		{"results added", fmt.Sprintf("%d (skipped %d)", summary.ResultsAdded, len(summary.ResultsSkipped))},
		{"events added", fmt.Sprintf("%d (skipped %d)", summary.EventsAdded, len(summary.EventsSkipped))},
		{"store totals", fmt.Sprintf("%d sample(s), %d result(s), %d event(s)",
			summary.Meta.Samples, summary.Meta.Results, summary.Meta.Events)},
		{"ingests recorded", fmt.Sprintf("%d", summary.Meta.Ingests)},
	}))
	table := report.NewTable("SEQ", "ACTION", "RECORDS", "HASH", "DETAIL").RightAlign(0, 2)
	for _, entry := range summary.AuditEntries {
		table.Add(fmt.Sprintf("%d", entry.Sequence), entry.Action,
			fmt.Sprintf("%d", entry.Records), truncateHash(entry.Hash), entry.Detail)
	}
	return append(lines, report.Section("Audit entries written", table.Render())...)
}

func truncateHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func runQuantify(env Environment, args []string) (int, error) {
	ctx, err := prepare("quantify", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Quantification(ctx.analysis)...)
	document := struct {
		AsOf         timeutil.Stamp  `json:"as_of"`
		Counts       pipeline.Counts `json:"counts"`
		Measurements any             `json:"measurements"`
		Skipped      []string        `json:"skipped"`
	}{ctx.asOf, ctx.analysis.Counts, ctx.analysis.Measurements, ctx.analysis.Skipped}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

func runQC(env Environment, args []string) (int, error) {
	ctx, err := prepare("qc", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Quality(ctx.analysis)...)
	if ctx.opts.resultID != "" {
		found := false
		for _, assessment := range ctx.analysis.Assessments {
			if assessment.ResultID != ctx.opts.resultID {
				continue
			}
			found = true
			lines = append(lines, report.QualityDetail(assessment)...)
		}
		if !found {
			return ExitError, fmt.Errorf("no assessment for result %q", ctx.opts.resultID)
		}
	} else if ctx.opts.detail {
		for _, assessment := range ctx.analysis.Assessments {
			lines = append(lines, report.QualityDetail(assessment)...)
		}
	}
	document := struct {
		AsOf        timeutil.Stamp  `json:"as_of"`
		Tally       qc.Tally        `json:"tally"`
		Assessments []qc.Assessment `json:"assessments"`
		Barred      []string        `json:"barred_from_trends"`
	}{ctx.asOf, ctx.analysis.QualityTally, ctx.analysis.Assessments, ctx.analysis.FailedResults()}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	if ctx.analysis.QualityTally.Failed > 0 {
		return ExitVerdict, nil
	}
	return ExitOK, nil
}

func runNormalize(env Environment, args []string) (int, error) {
	ctx, err := prepare("normalize", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Normalisation(ctx.analysis)...)
	lines = append(lines, report.Series(ctx.analysis)...)
	document := struct {
		AsOf   timeutil.Stamp `json:"as_of"`
		Points any            `json:"points"`
		Series any            `json:"series"`
	}{ctx.asOf, ctx.analysis.Points, ctx.analysis.Series}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

func runBaseline(env Environment, args []string) (int, error) {
	ctx, err := prepare("baseline", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Baselines(ctx.analysis)...)
	if ctx.opts.detail {
		lines = append(lines, report.Series(ctx.analysis)...)
	}
	document := struct {
		AsOf      timeutil.Stamp `json:"as_of"`
		Baselines any            `json:"baselines"`
	}{ctx.asOf, ctx.analysis.Baselines}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

func runDetect(env Environment, args []string) (int, error) {
	ctx, err := prepare("detect", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Detection(ctx.analysis)...)
	lines = append(lines, report.Exceedances(ctx.analysis)...)
	if ctx.opts.siteFilter != "" {
		item, ok := ctx.analysis.TrendIndex()[ctx.opts.siteFilter]
		if !ok {
			return ExitError, fmt.Errorf("no trend for site %q", ctx.opts.siteFilter)
		}
		lines = append(lines, report.TrendDetail(item)...)
	} else if ctx.opts.detail {
		for _, item := range ctx.analysis.Trends {
			lines = append(lines, report.TrendDetail(item)...)
		}
	}
	document := struct {
		AsOf    timeutil.Stamp    `json:"as_of"`
		Trends  []trend.SiteTrend `json:"trends"`
		Flagged []string          `json:"flagged_sites"`
	}{ctx.asOf, ctx.analysis.Trends, trend.Flagged(ctx.analysis.Trends)}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	if len(document.Flagged) > 0 {
		return ExitVerdict, nil
	}
	return ExitOK, nil
}

func runCatchment(env Environment, args []string) (int, error) {
	ctx, err := prepare("catchment", env, args)
	if err != nil {
		return ExitError, err
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Topology(ctx.graph)...)
	lines = append(lines, report.States(ctx.analysis)...)
	lines = append(lines, report.RollUps(ctx.analysis)...)
	document := struct {
		AsOf       timeutil.Stamp `json:"as_of"`
		Topology   []string       `json:"topological_order"`
		Headwaters []string       `json:"headwaters"`
		Outlets    []string       `json:"outlets"`
		States     any            `json:"site_states"`
		RollUps    any            `json:"roll_ups"`
	}{ctx.asOf, ctx.analysis.Topology, ctx.analysis.Headwaters,
		ctx.analysis.Outlets, ctx.analysis.States, ctx.analysis.RollUps}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

// alertSummary is the JSON document the alert subcommand emits.
type alertSummary struct {
	AsOf    timeutil.Stamp     `json:"as_of"`
	Store   string             `json:"store"`
	Written bool               `json:"snapshot_written"`
	Signals any                `json:"signals"`
	Ledger  alert.Ledger       `json:"ledger"`
	Counts  alert.Counts       `json:"counts"`
	Audit   []store.AuditEntry `json:"audit_entries"`
}

func runAlert(env Environment, args []string) (int, error) {
	ctx, err := prepare("alert", env, args)
	if err != nil {
		return ExitError, err
	}
	summary := alertSummary{AsOf: ctx.asOf, Signals: ctx.analysis.Signals}
	prior := alert.NewLedger(ctx.cfg)
	var handle *store.Store
	if strings.TrimSpace(ctx.opts.storePath) != "" {
		handle, err = ctx.opts.resolveStore()
		if err != nil {
			return ExitError, err
		}
		summary.Store = handle.Root()
		var loaded alert.Ledger
		found, err := handle.ReadDocument(store.AlertsFile, &loaded)
		if err != nil {
			return ExitError, err
		}
		if found {
			if loaded.SchemaVersion != alert.LedgerSchemaVersion {
				return ExitError, fmt.Errorf("%s: schema_version must be %q, got %q",
					handle.Path(store.AlertsFile), alert.LedgerSchemaVersion, loaded.SchemaVersion)
			}
			prior = loaded
		}
	}
	ledger, err := alert.Advance(ctx.cfg, prior, ctx.analysis.Signals, ctx.asOf)
	if err != nil {
		return ExitError, err
	}
	summary.Ledger = ledger
	summary.Counts = ledger.Count()
	if handle != nil && ctx.opts.writeStore {
		snapshotPayload, err := handle.WriteDocument(store.SnapshotFile, ctx.analysis)
		if err != nil {
			return ExitError, err
		}
		entry, err := handle.Record(ctx.asOf, "write-snapshot",
			fmt.Sprintf("signal snapshot for %d site(s) as of %s",
				ctx.analysis.Counts.Sites, ctx.asOf), ctx.analysis.Counts.Measurements, snapshotPayload)
		if err != nil {
			return ExitError, err
		}
		summary.Audit = append(summary.Audit, entry)
		alertPayload, err := handle.WriteDocument(store.AlertsFile, ledger)
		if err != nil {
			return ExitError, err
		}
		counts := ledger.Count()
		entry, err = handle.Record(ctx.asOf, "write-alerts",
			fmt.Sprintf("%d alert(s): %d open, %d sustained, %d downgraded, %d resolved",
				counts.Total, counts.Open, counts.Sustained, counts.Downgraded, counts.Resolved),
			counts.Total, alertPayload)
		if err != nil {
			return ExitError, err
		}
		summary.Audit = append(summary.Audit, entry)
		summary.Written = true
	}
	lines := report.Header(ctx.analysis, ctx.cfg)
	lines = append(lines, report.Corroboration(ctx.analysis)...)
	lines = append(lines, report.Grades(ctx.analysis.Signals)...)
	lines = append(lines, report.Alerts(ledger)...)
	if handle != nil {
		lines = append(lines, report.Section("Persistence", report.KeyValues([][2]string{
			{"store", handle.Root()},
			{"snapshot written", report.YesNo(summary.Written)},
			{"audit entries written", fmt.Sprintf("%d", len(summary.Audit))},
		}))...)
	}
	if err := ctx.deliver(env, summary, lines); err != nil {
		return ExitError, err
	}
	if len(ledger.Active()) > 0 {
		return ExitVerdict, nil
	}
	return ExitOK, nil
}

// verifySummary is the JSON document the verify subcommand emits.
type verifySummary struct {
	Store     string             `json:"store"`
	Report    store.AuditReport  `json:"audit"`
	Entries   []store.AuditEntry `json:"entries"`
	Inventory store.Inventory    `json:"inventory"`
	Meta      store.Meta         `json:"meta"`
}

func runVerify(env Environment, args []string) (int, error) {
	opts := &options{}
	set := newFlagSet("verify", env.Stderr)
	opts.commonFlags(set)
	opts.storeFlag(set)
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	if err := requireNoArgs(set); err != nil {
		return ExitError, fmt.Errorf("%w\n%s", err, helpFor("verify", set))
	}
	format, err := opts.resolveFormat()
	if err != nil {
		return ExitError, err
	}
	handle, err := opts.resolveStore()
	if err != nil {
		return ExitError, err
	}
	auditReport, err := handle.VerifyAudit()
	if err != nil {
		return ExitError, err
	}
	entries, err := handle.AuditEntries()
	if err != nil {
		return ExitError, err
	}
	inventory, err := handle.List()
	if err != nil {
		return ExitError, err
	}
	meta, err := handle.Meta()
	if err != nil {
		return ExitError, err
	}
	summary := verifySummary{
		Store: handle.Root(), Report: auditReport,
		Entries: entries, Inventory: inventory, Meta: meta,
	}
	lines := report.Audit(entries, auditReport, handle.AuditPath())
	lines = append(lines, report.Inventory(inventory)...)
	out, closer, err := opts.writer(env.Stdout)
	if err != nil {
		return ExitError, err
	}
	defer func() { _ = closer() }()
	if err := opts.emit(out, format, summary, lines); err != nil {
		return ExitError, err
	}
	if !auditReport.Verified {
		return ExitVerdict, nil
	}
	return ExitOK, nil
}

func runReport(env Environment, args []string) (int, error) {
	ctx, err := prepare("report", env, args)
	if err != nil {
		return ExitError, err
	}
	ledger := alert.NewLedger(ctx.cfg)
	var entries []store.AuditEntry
	var auditReport store.AuditReport
	auditPath := ""
	if strings.TrimSpace(ctx.opts.storePath) != "" {
		handle, err := ctx.opts.resolveStore()
		if err != nil {
			return ExitError, err
		}
		var loaded alert.Ledger
		found, err := handle.ReadDocument(store.AlertsFile, &loaded)
		if err != nil {
			return ExitError, err
		}
		if found {
			ledger = loaded
		}
		if entries, err = handle.AuditEntries(); err != nil {
			return ExitError, err
		}
		if auditReport, err = handle.VerifyAudit(); err != nil {
			return ExitError, err
		}
		auditPath = handle.AuditPath()
	}
	lines := report.Combined(ctx.cfg, ctx.analysis, ledger, ctx.graph, entries, auditReport, auditPath)
	document := struct {
		Analysis pipeline.Analysis `json:"analysis"`
		Ledger   alert.Ledger      `json:"alerts"`
		Audit    store.AuditReport `json:"audit"`
		Notice   string            `json:"notice"`
	}{ctx.analysis, ledger, auditReport, standingNotice}
	if err := ctx.deliver(env, document, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

// standingNotice accompanies every machine-readable report so the caveat travels
// with the numbers.
const standingNotice = "FluWatershed is a study tool. It provides no public-health, clinical, " +
	"veterinary or regulatory advice and must not be used for real outbreak decisions. " +
	"All bundled sample data is fictional."

func runProfile(env Environment, args []string) (int, error) {
	opts := &options{}
	set := newFlagSet("profile", env.Stderr)
	opts.commonFlags(set)
	set.BoolVar(&opts.emitConfig, "default", false, "print the built-in default profile rather than a loaded one")
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	if err := requireNoArgs(set); err != nil {
		return ExitError, fmt.Errorf("%w\n%s", err, helpFor("profile", set))
	}
	format, err := opts.resolveFormat()
	if err != nil {
		return ExitError, err
	}
	cfg := config.Default()
	if !opts.emitConfig {
		cfg, err = opts.resolveConfig()
		if err != nil {
			return ExitError, err
		}
	}
	lines := report.Section("Active profile", cfg.Describe())
	lines = append(lines, report.Section("Fingerprint", []string{cfg.Fingerprint()})...)
	out, closer, err := opts.writer(env.Stdout)
	if err != nil {
		return ExitError, err
	}
	defer func() { _ = closer() }()
	if err := opts.emit(out, format, cfg, lines); err != nil {
		return ExitError, err
	}
	return ExitOK, nil
}

func runVersion(env Environment, args []string) (int, error) {
	set := newFlagSet("version", env.Stderr)
	if err := set.Parse(args); err != nil {
		return ExitError, err
	}
	if err := requireNoArgs(set); err != nil {
		return ExitError, err
	}
	fmt.Fprintf(env.Stdout, "fluwatershed %s\n", Version)
	fmt.Fprintf(env.Stdout, "%s\n", standingNotice)
	return ExitOK, nil
}
