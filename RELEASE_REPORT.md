# FluWatershed release report

FluWatershed is an independent original project with no affiliation, endorsement, sponsorship or
association with any other product, company, agency, university or organisation. All sample data is
fictional. It is a study tool and gives no public-health, clinical, veterinary or regulatory advice.

- Module: `FluWatershed`
- Go directive: `go 1.22.5`
- External dependencies: none beyond the Go standard library
- Binary: `cmd/fluwatershed`

## Effective production LOC

Effective LOC counts lines in non-test `.go` files, excluding blank lines and comment-only lines.
`*_test.go` files and everything under the ignored `.cache/` directory are excluded. No generated
code is present in the repository.

| File                                  | Effective LOC |
| ------------------------------------- | ------------- |
| `cmd/fluwatershed/main.go`            | 8             |
| `internal/alert/alert.go`             | 315           |
| `internal/baseline/baseline.go`       | 135           |
| `internal/catchment/graph.go`         | 250           |
| `internal/catchment/rollup.go`        | 275           |
| `internal/cli/cli.go`                 | 118           |
| `internal/cli/commands.go`            | 712           |
| `internal/cli/flags.go`               | 167           |
| `internal/config/config.go`           | 268           |
| `internal/config/validate.go`         | 297           |
| `internal/corroborate/corroborate.go` | 298           |
| `internal/model/types.go`             | 270           |
| `internal/model/validate.go`          | 369           |
| `internal/normalize/normalize.go`     | 282           |
| `internal/numeric/numeric.go`         | 218           |
| `internal/pipeline/load.go`           | 109           |
| `internal/pipeline/pipeline.go`       | 141           |
| `internal/qc/qc.go`                   | 424           |
| `internal/quant/quant.go`             | 283           |
| `internal/report/analysis.go`         | 289           |
| `internal/report/signals.go`          | 277           |
| `internal/report/table.go`            | 161           |
| `internal/store/atomic.go`            | 114           |
| `internal/store/audit.go`             | 170           |
| `internal/store/store.go`             | 292           |
| `internal/strictjson/strictjson.go`   | 143           |
| `internal/timeutil/timeutil.go`       | 159           |
| `internal/trend/detect.go`            | 263           |
| `internal/trend/slope.go`             | 112           |
| **Total**                             | **6919**      |

Requirement was at least 2600 effective production LOC.

## Packages

| Package                             | Responsibility                                                         |
| ----------------------------------- | ---------------------------------------------------------------------- |
| `FluWatershed/cmd/fluwatershed`     | Process entry point                                                    |
| `FluWatershed/internal/alert`       | Alert state machine, transition history, cooldown, derived identifiers |
| `FluWatershed/internal/baseline`    | Rolling median and scaled median absolute deviation per site           |
| `FluWatershed/internal/catchment`   | Directed acyclic topology, load propagation, upstream attribution      |
| `FluWatershed/internal/cli`         | Command dispatch, flags, exit codes, output delivery                   |
| `FluWatershed/internal/config`      | Policy document, defaults, range validation, fingerprint               |
| `FluWatershed/internal/corroborate` | Evidence streams, weighted scoring, graded signals                     |
| `FluWatershed/internal/model`       | Domain types and per-object and cross-object validation                |
| `FluWatershed/internal/normalize`   | Dilution, flow and population correction, daily series                 |
| `FluWatershed/internal/numeric`     | Deterministic rounding, robust statistics, great-circle distance       |
| `FluWatershed/internal/pipeline`    | Input loading, validation report, stage wiring, analysis document      |
| `FluWatershed/internal/qc`          | Eight named quality rules and their verdicts                           |
| `FluWatershed/internal/quant`       | Standard-curve inversion, replicate aggregation, coverage band         |
| `FluWatershed/internal/report`      | Deterministic fixed-width text rendering                               |
| `FluWatershed/internal/store`       | Atomic writes, append-only ledgers, hash-chained audit trail           |
| `FluWatershed/internal/strictjson`  | The only JSON entry points; strict decode, deterministic encode        |
| `FluWatershed/internal/timeutil`    | The single validated UTC timestamp type and windows                    |
| `FluWatershed/internal/trend`       | Theil-Sen slope, exceedance tests, sustained rise                      |

18 packages, 17 of which carry tests. `cmd/fluwatershed` is an eight-line shell whose behaviour is
covered end to end by the `internal/cli` suite.

## Validation results

Every command below was run from the repository root with `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOFLAGS=-mod=mod`, and `GOCACHE` and `TMP` redirected under the ignored `.cache/` directory.

| Command                  | Result          |
| ------------------------ | --------------- |
| `gofmt -l .`             | no output       |
| `go build ./...`         | no output       |
| `go vet ./...`           | no output       |
| `go test ./... -count=1` | all packages ok |

Test run:

```
?       FluWatershed/cmd/fluwatershed   [no test files]
ok      FluWatershed/internal/alert
ok      FluWatershed/internal/baseline
ok      FluWatershed/internal/catchment
ok      FluWatershed/internal/cli
ok      FluWatershed/internal/config
ok      FluWatershed/internal/corroborate
ok      FluWatershed/internal/model
ok      FluWatershed/internal/normalize
ok      FluWatershed/internal/numeric
ok      FluWatershed/internal/pipeline
ok      FluWatershed/internal/qc
ok      FluWatershed/internal/quant
ok      FluWatershed/internal/report
ok      FluWatershed/internal/store
ok      FluWatershed/internal/strictjson
ok      FluWatershed/internal/timeutil
ok      FluWatershed/internal/trend
```

### Numerical tests

- `internal/quant`: the standard curve is inverted exactly. With slope `-3.32` and intercept `38.5`,
  Ct `38.5` gives 1 copy per reaction, `35.18` gives 10, `31.86` gives 100 and `28.54` gives 1000.
  The conversion factor for 200 mL concentrated into 100 µL with 5 µL of template at 0.62 recovery
  is asserted term by term, including that doubling the dilution and halving the recovery each
  double the reported concentration. Aggregation asserts that two quantifiable replicates at 100 and
  200 copies per reaction average to 30000 copies per litre while a third well below the limit of
  quantification does not drag the mean down. All four below-limit substitution rules and all three
  non-detect substitution rules are checked against exact expected values. Well order is shown not
  to change the answer.
- `internal/trend`: Theil-Sen recovers slope 2 and intercept 1 exactly from a clean line, and
  recovers slope 2 and the same intercept from the same line with one point replaced by a gross
  outlier. Reordering the points does not change the fit. `FitLog10` over 10, 100, 1000, 10000 on
  consecutive days gives exactly 1 decade per day, a doubling time of `log10(2)` days and a two-day
  fold change of 100. Degenerate inputs are refused.
- `internal/numeric`: rounding is half away from zero in both signs; the scaled median absolute
  deviation is shown to be unmoved by an outlier that inflates the standard deviation by orders of
  magnitude; the great-circle distance for one degree of latitude is checked against 111.19 km and
  is shown to be symmetric.
- `internal/baseline`: the median, the scaled dispersion and the interpolated ninetieth percentile
  are asserted on hand-computed data, and the day under test is shown not to leak into its own
  baseline.
- `internal/catchment`: contribution load shares are shown to sum to one, and the implied
  concentration at an outlet is checked against the hand-computed total load divided by total flow.

### Offline CLI smoke workflow

Ran end to end against a temporary store built from `examples/`, with no network access:

```
version, help, profile,
validate (files), ingest, ingest again, validate (store),
quantify, qc, normalize, baseline, detect, catchment,
alert --as-of 2026-03-20 --write,
alert --as-of 2026-03-24 --write,
alert (derived instant) --write,
verify, report (text), report (json)
```

Outcomes:

- Exit codes as designed: `0` for informational commands, `2` for `qc` (the example deliberately
  includes failing results), `detect` (flagged sites) and `alert` (active alerts).
- The second `ingest` added zero records and skipped all 61 samples, 71 results and 4 events.
- Three successive `alert` runs advanced the same episodes rather than opening new ones, reaching
  the sustained state.
- `verify` reported the chain intact across eleven audit entries.
- Determinism: two consecutive `report --format json` runs produced byte-identical files
  (SHA-256 `dcd1515069d793f7…`).
- Tamper detection: editing one action string inside `audit.log` made `verify` exit `2` and name the
  first broken sequence number.

### Docker

```
docker build -t fluwatershed:local .
```

Built successfully. Builder stage `golang:1.22` with `GOTOOLCHAIN=local`, `CGO_ENABLED=0`,
`GOPROXY=off`; it runs `go vet ./...` before `go build -trimpath`. Final stage `FROM scratch` holds
only the static binary and sets it as the entry point. Resulting image: 1,200,989 bytes,
`linux/amd64`.

Run with no network:

```
docker run --rm --network none fluwatershed:local version
  -> fluwatershed 1.0.0 plus the standing caution, exit 0

docker run --rm --network none -v "<repo>/examples:/data:ro" fluwatershed:local \
  validate --network /data/network.json --samples /data/samples.jsonl \
           --results /data/results.jsonl --events /data/events.jsonl
  -> 6 sites, 5 links, 61 samples, 71 results, 4 carcass events,
     acyclic with 3 headwaters and 1 outlet, every check passed, exit 0

docker run --rm --network none -v "<repo>/examples:/data:ro" fluwatershed:local \
  detect --network /data/network.json --samples /data/samples.jsonl \
         --results /data/results.jsonl --events /data/events.jsonl --config /data/config.json
  -> full trend and exceedance report, exit 2 (flagged sites)
```

## Example data

`examples/` holds an invented catchment of six sites: three wastewater influent works, two waterway
grab points and one sediment station, linked into a directed acyclic graph with three headwaters and
one outlet. It carries 61 samples and 71 assay results across 26 fictional days in March 2026, plus
four fictional wild-bird carcass observations. The data is shaped to exercise every code path: a
clear sustained rise at one influent works, a single-day exceedance at another, a quiet site that
never flags, late-season detections in the waterways, and five results that deliberately fail
different quality rules (replicate spread, holding time, transport temperature, negative control
contamination and inhibition) so that exclusion from trends can be observed.

Every name, coordinate, species and measurement in that directory is invented. Nothing in it
describes a real place, programme, organism or event.
