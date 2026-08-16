# FluWatershed

An offline backend command line tool for **environmental H5 surveillance signal processing and
alerting**. It takes assay results measured from wastewater influent, waterway grab samples and
sediment, together with wild-bird carcass observations, and turns them into quantified
concentrations, quality verdicts, normalised loads, rolling baselines, exceedance and trend
findings, catchment roll-ups, corroborated graded signals and a persistent alert lifecycle.

Everything runs locally. There is no network client, no telemetry, no database server and no
dependency outside the Go standard library.

## Independence and scope

FluWatershed is an **independent original project**. It has **no affiliation, endorsement,
sponsorship, or association with any other product, company, agency, university, laboratory,
dashboard, programme or organisation**. No real-world entity, place or initiative is named,
referenced or implied anywhere in the code, the documentation or the sample data.

**All sample data in this repository is fictional.** The catchment, the site names, the
coordinates, the species names and every measurement in `examples/` were invented for this
project to exercise the code paths. They describe no real location, no real monitoring
programme and no real outbreak.

**This is a study tool.** It exists so that the arithmetic of environmental surveillance can be
read, tested and argued with. It does **not** provide public-health, clinical, veterinary,
epidemiological or regulatory advice, it is **not** a diagnostic or decision-support system, and
it **must not be used to make real outbreak decisions** of any kind. Anyone with an actual
surveillance or animal-health question should consult the qualified professionals responsible
for it.

## Motivation, in our own words

Recent public reporting has drawn attention to three related ideas: that avian influenza can
sometimes be picked up in water bodies before it turns up on a farm, that wastewater monitoring
dashboards are being extended to look for influenza A of the H5 subtype, and that large wild-bird
die-offs are being confirmed. Reading about that raised a question worth exploring in code: given
a handful of noisy environmental measurements from different places, matrices and days, what does
it actually take to say something defensible? The answer involves a surprising amount of careful
bookkeeping, and that bookkeeping is what this project implements. No article is quoted or
summarised here, and nothing in the code is drawn from any real programme.

## Building

The module targets `go 1.22.5` and needs no downloads.

```
go build ./...
go test ./... -count=1
go vet ./...
gofmt -l .
```

All four are expected to run with `GOTOOLCHAIN=local` and `GOPROXY=off`.

To produce the binary:

```
go build -o fluwatershed ./cmd/fluwatershed
```

## Commands

```
fluwatershed <command> [flags]
```

| Command     | What it does                                                                                |
| ----------- | ------------------------------------------------------------------------------------------- |
| `validate`  | Checks a catchment definition and its ledgers, including topology, without writing anything |
| `ingest`    | Appends samples, results and carcass events to a store and records the audit entry          |
| `quantify`  | Converts assay results into concentrations with coverage bands                              |
| `qc`        | Reports quality control and which results are barred from trends                            |
| `normalize` | Reports flow, population and dilution corrected values and the daily series                 |
| `baseline`  | Reports the rolling baseline for every site                                                 |
| `detect`    | Reports exceedance, sustained rise and trend direction                                      |
| `catchment` | Reports topology, propagated loads, contribution shares and upstream attribution            |
| `alert`     | Advances the alert lifecycle and, with `--write`, persists the signal snapshot              |
| `verify`    | Recomputes the store audit chain and lists the store contents                               |
| `report`    | Renders the whole analysis in one document                                                  |
| `profile`   | Prints the active policy document and its fingerprint                                       |
| `version`   | Prints the build identifier                                                                 |

Common flags:

| Flag                  | Meaning                                                                 |
| --------------------- | ----------------------------------------------------------------------- |
| `--config PATH`       | JSON policy document; the built-in default profile is used when omitted |
| `--store PATH`        | Local store directory holding ledgers, snapshots and the audit chain    |
| `--network PATH`      | Catchment definition JSON document                                      |
| `--samples PATH`      | Sample JSONL ledger                                                     |
| `--results PATH`      | Assay result JSONL ledger                                               |
| `--events PATH`       | Carcass event JSONL ledger                                              |
| `--as-of STAMP`       | Reporting instant in RFC 3339 UTC                                       |
| `--format text\|json` | Output encoding                                                         |
| `--out PATH`          | Write to a file instead of standard output                              |
| `--site ID`           | Restrict detail output to one site                                      |
| `--result ID`         | Restrict detail output to one assay result                              |
| `--detail`            | Include per-day and per-check detail                                    |
| `--write`             | Persist the computed snapshot into the store (`alert` only)             |

Explicit input files win over a store when both are given, because a caller who names files means
to analyse those exact files.

Exit codes: `0` success, `1` a usage or runtime error, `2` the check ran correctly but its verdict
was negative — input that failed validation, results barred by quality control, a flagged site, an
active alert, or an audit chain that did not verify.

## Worked example

```
fluwatershed validate  --network examples/network.json \
                       --samples examples/samples.jsonl \
                       --results examples/results.jsonl \
                       --events  examples/events.jsonl

fluwatershed ingest    --store ./store --config examples/config.json \
                       --network examples/network.json \
                       --samples examples/samples.jsonl \
                       --results examples/results.jsonl \
                       --events  examples/events.jsonl

fluwatershed qc        --store ./store --config examples/config.json --detail
fluwatershed baseline  --store ./store --config examples/config.json
fluwatershed detect    --store ./store --config examples/config.json --detail
fluwatershed catchment --store ./store --config examples/config.json
fluwatershed alert     --store ./store --config examples/config.json --write
fluwatershed verify    --store ./store
fluwatershed report    --store ./store --config examples/config.json --format json --out report.json
```

## Input formats

### Catchment definition — one strict JSON document

```json
{
  "schema_version": "fluwatershed/v1",
  "catchment_id": "vellamoor-basin",
  "label": "Vellamoor Basin demonstration catchment (fictional)",
  "sites": [
    {
      "site_id": "WW-KELDMOOR",
      "label": "Keldmoor influent works (fictional)",
      "matrix": "wastewater_influent",
      "latitude": 45.21,
      "longitude": -95.44,
      "served_population": 18400,
      "mean_daily_flow_m3": 6200,
      "indicator_reference_copies_per_litre": 24000000,
      "absolute_threshold_signal": 0
    }
  ],
  "links": [
    {
      "upstream_site_id": "WW-KELDMOOR",
      "downstream_site_id": "RIV-KELD-BROOK",
      "travel_hours": 6
    }
  ]
}
```

`matrix` is one of `wastewater_influent`, `waterway_grab`, `sediment`, `carcass_event`. Links point
downstream and must form a directed acyclic graph. `absolute_threshold_signal` is optional and is
expressed in the site's own signal unit; zero means "use the configured default".

### Samples — one JSON object per line

```json
{
  "sample_id": "SMP-KLD-0001",
  "site_id": "WW-KELDMOOR",
  "matrix": "wastewater_influent",
  "collected_at": "2026-03-02T08:00:00Z",
  "volume_ml": 200,
  "transport_temperature_c": 4.2,
  "observed_flow_m3": 6100.5,
  "indicator_copies_per_litre": 23000000,
  "custody": [
    {
      "action": "collected",
      "holder": "keldmoor-crew",
      "at": "2026-03-02T08:00:00Z",
      "temperature_c": 4.2
    }
  ],
  "comment": "fictional"
}
```

Custody actions are `collected`, `transferred`, `received`, `stored`, `extracted`. The first step
must be `collected` and its time must equal `collected_at`.

### Assay results — one JSON object per line

```json
{
  "result_id": "RES-KLD-0001-1",
  "sample_id": "SMP-KLD-0001",
  "target_gene": "influenza_a_matrix_gene",
  "analysed_at": "2026-03-03T10:00:00Z",
  "replicates": [
    { "well": "A1", "ct": 33.5 },
    { "well": "A2", "ct": null }
  ],
  "dilution_factor": 1,
  "extract_volume_ul": 100,
  "template_volume_ul": 5,
  "recovery_fraction": 0.62,
  "standard_curve": {
    "slope": -3.32,
    "intercept": 38.5,
    "r_squared": 0.994,
    "lod_copies_per_reaction": 5,
    "loq_copies_per_reaction": 20
  },
  "controls": {
    "negative_control_ct": null,
    "positive_control_expected_copies": 10000,
    "positive_control_observed_copies": 9100,
    "inhibition_control_ct": 24.5,
    "inhibition_reference_ct": 24.4,
    "extraction_blank_detected": false
  }
}
```

A `null` Ct means the well never crossed threshold, which is a genuine non-detect rather than
missing data. A `null` negative control Ct means the no-template control did not amplify, which is
the desired outcome.

### Carcass events — one JSON object per line

```json
{
  "event_id": "EVT-0002",
  "observed_at": "2026-03-19T10:30:00Z",
  "latitude": 45.2255,
  "longitude": -95.4555,
  "species": "grey-throated shearwater (fictional)",
  "carcass_count": 22,
  "h5_confirmed": true,
  "locality_note": "fictional locality"
}
```

### Policy document

`examples/config.json` is the built-in default profile written out. `fluwatershed profile --default
--format json` reproduces it. Every member is required and range checked; an unknown member is an
error rather than a warning, so a renamed knob cannot silently fall back to a default.

## What the pipeline computes

1. **Quantification.** A replicate Ct becomes copies per reaction by inverting the standard curve,
   `log10(copies) = (Ct - intercept) / slope`. That becomes copies per litre of original sample by
   multiplying the dilution factor and the extract-to-template ratio and dividing by the
   concentrated volume in litres and the recovery fraction. Replicates are classified against the
   limits of detection and quantification, and the reported value is the arithmetic mean of the
   quantifiable replicates when enough of them exist. A linear mean is used rather than a geometric
   one because loads add downstream. A result that amplified but stayed below the limit of
   quantification, or did not amplify at all, is substituted under a stated configurable rule and
   the substitution is recorded in the notes. A coverage band comes from the log10 standard error of
   the replicates, or from a configured default when only one well is quantifiable.
2. **Quality control.** Eight named rules: replicate agreement, standard curve fit, negative
   control contamination, positive control recovery, inhibition, holding time against a per-matrix
   limit, transport temperature, and custody continuity. Each rule records what it observed and what
   limit it was held to. A failing result is marked unusable and **never** reaches a baseline, an
   exceedance test, a daily series or a catchment load.
3. **Normalisation.** Optional dilution correction against a faecal-strength indicator, then a
   flow-normalised daily load in copies per day, then a population-normalised load in copies per
   person per day. Only a piped influent stream has a defensible daily flow, so a grab or sediment
   sample stays on a concentration basis; each site therefore keeps one internally consistent unit.
4. **Daily series.** One value per calendar day per site, built from the configured primary target
   gene so that different targets are never averaged together.
5. **Baselines.** A median and a scaled median absolute deviation over a trailing window, computed
   as of a stated day and by default excluding the day under test, so that the day's own value
   cannot inflate the reference it is compared against.
6. **Detection.** Three independent exceedance tests — a baseline multiple, a robust z score and an
   absolute threshold — plus a sustained-rise rule over consecutive intervals, plus a Theil-Sen
   slope on log10 values.
7. **Catchment roll-up.** Loads are propagated downstream and added, with each contributor's share
   of the combined flow and of the combined load reported separately. A large load share with a
   small flow share is the interesting case. Given a flagged node, the most-upstream flagged site
   whose travel time is consistent with the delay is identified.
8. **Corroboration.** Four evidence streams — wastewater trend, waterway grab, sediment and carcass
   events within a radius and time window — combine into a weighted score over the streams that
   apply to the site, graded none, low, moderate or high. A configurable minimum number of
   contributing streams caps the grade when only one has anything to say.
9. **Alert lifecycle.** `open`, `sustained`, `downgraded`, `resolved`, with every transition
   appended to the alert's history with its instant and reason, and a cooldown that suppresses
   reopening straight after a resolution.

## The store

A store is a plain directory:

| File            | Shape              | Purpose                          |
| --------------- | ------------------ | -------------------------------- |
| `meta.json`     | JSON document      | Store bookkeeping and counts     |
| `network.json`  | JSON document      | The catchment definition         |
| `samples.jsonl` | JSONL, append only | Sample ledger                    |
| `results.jsonl` | JSONL, append only | Assay result ledger              |
| `events.jsonl`  | JSONL, append only | Carcass event ledger             |
| `signals.json`  | JSON document      | Computed signal snapshot         |
| `alerts.json`   | JSON document      | Alert ledger                     |
| `audit.log`     | JSONL, append only | SHA-256 hash-chained audit trail |

Observations live in append-only ledgers because an observation is a historical fact. Re-ingesting
the same records is a no-op: existing identifiers are skipped and counted, not duplicated. Computed
documents are replaced atomically by writing a temporary file in the same directory and renaming it
over the target, so a reader sees either the whole previous file or the whole new one.

Every mutation appends an audit entry carrying the SHA-256 of the bytes written, the previous
entry's hash, and its own hash over all of its fields. `verify` recomputes the chain and links each
entry to the hash its predecessor's fields produce, so editing any entry invalidates every entry
after it. `verify` reports integrity separately from chronology: replaying history at earlier
instants leaves the chain intact and is reported as a note rather than a failure.

## Determinism

Two runs over the same input with the same policy produce byte-identical output.

- **No wall clock.** Nothing in the program reads the system time. The reporting instant comes from
  `--as-of` or from the newest instant present in the input.
- **No randomness.** There is no random number generator anywhere. Alert identifiers are derived
  from the SHA-256 of the site, the opening instant and the policy fingerprint.
- **Sorted iteration.** Every map is drained into a slice and sorted before use. Sites, links,
  samples, results, events, measurements, assessments, points, series, baselines, trends, roll-ups,
  signals and alerts all have a stated canonical order.
- **Order-independent input.** Shuffling the order of records in the input files does not change the
  analysis document.
- **Fixed rounding.** Copy numbers are rounded to a configured number of significant figures;
  scores, ratios and slopes to a fixed number of decimals. Rounding is half away from zero.
- **Order-independent statistics.** The trend slope is the median of pairwise slopes taken from an
  explicitly sorted slice, so it does not depend on the order the pairs were visited.
- **Fixed formatting.** Tables compute their widths from their own cells and never consult terminal
  width or locale. JSON is encoded with two-space indentation, no HTML escaping and one trailing
  newline.

## Docker

The repository root holds a multi-stage `Dockerfile`. The builder stage is `golang:1.22` with
`GOTOOLCHAIN=local`, `CGO_ENABLED=0` and `GOPROXY=off`, so it compiles a fully static binary without
touching the network. The final stage is `scratch` and contains only that binary, with the binary as
the entry point.

```
docker build -t fluwatershed:local .

docker run --rm --network none fluwatershed:local version

docker run --rm --network none \
  -v "$PWD/examples:/data:ro" \
  fluwatershed:local validate \
    --network /data/network.json \
    --samples /data/samples.jsonl \
    --results /data/results.jsonl \
    --events  /data/events.jsonl
```

`--network none` is the point: the container has no network access and the tool never needs any.

## Repository layout

```
cmd/fluwatershed/     entry point
internal/strictjson/  the only JSON entry points; strict decode, deterministic encode
internal/numeric/     rounding, robust statistics, great-circle distance
internal/timeutil/    the single UTC timestamp type and windows
internal/model/       domain types and validation
internal/config/      policy document, defaults, range checks, fingerprint
internal/quant/       Ct to copies per litre, replicate aggregation, coverage band
internal/qc/          the eight quality rules
internal/normalize/   dilution, flow and population correction, daily series
internal/baseline/    rolling median and robust dispersion
internal/trend/       Theil-Sen slope, exceedance, sustained rise
internal/catchment/   topology, roll-up, upstream attribution
internal/corroborate/ evidence streams and graded signals
internal/alert/       the alert state machine
internal/store/       atomic writes, ledgers, hash-chained audit
internal/pipeline/    stage wiring and the analysis document
internal/report/      deterministic text rendering
internal/cli/         command surface
examples/             fictional catchment, samples, results, events and policy
```

## Licence

No licence is granted or implied by this repository.
