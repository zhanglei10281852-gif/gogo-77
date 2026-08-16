// Package cli is the command line surface of FluWatershed.
//
// Every subcommand follows the same shape: parse flags, load and validate
// inputs, run the pipeline, then emit either deterministic text or a strict JSON
// document. Nothing writes to the store unless the subcommand's purpose is to
// write to it, so the read-only commands can be run freely against a live store.
//
// Exit codes are meaningful: 0 for success, 1 for a usage or runtime error, and
// 2 when the requested check ran correctly but its verdict was negative, such as
// input that failed validation or an audit chain that did not verify.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Version is the build identifier reported by the version subcommand.
const Version = "1.0.0"

// Exit codes returned by Run.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitVerdict = 2
)

// Environment carries the streams a run reads and writes, so the whole CLI can
// be exercised in a test without touching the real standard streams.
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer
}

// commandFunc implements one subcommand.
type commandFunc func(env Environment, args []string) (int, error)

// command is one entry in the dispatch table.
type command struct {
	name    string
	summary string
	run     commandFunc
}

// commands returns the dispatch table in the order the help text lists it.
func commands() []command {
	return []command{
		{"validate", "check a catchment definition and its ledgers without writing anything", runValidate},
		{"ingest", "append samples, results and events to a store and record the audit entry", runIngest},
		{"quantify", "convert assay results into concentrations", runQuantify},
		{"qc", "report quality control and which results are barred from trends", runQC},
		{"normalize", "report flow, population and dilution corrected values", runNormalize},
		{"baseline", "report the rolling baseline for every site", runBaseline},
		{"detect", "report exceedance, sustained rise and trend direction", runDetect},
		{"catchment", "report topology, propagated loads and upstream attribution", runCatchment},
		{"alert", "advance the alert lifecycle and persist the signal snapshot", runAlert},
		{"verify", "recompute the store audit chain and list the store contents", runVerify},
		{"report", "render the whole analysis in one document", runReport},
		{"profile", "print the active policy document", runProfile},
		{"version", "print the build identifier", runVersion},
	}
}

// Run dispatches one command line and returns the process exit code.
func Run(env Environment, args []string) int {
	if env.Stdout == nil || env.Stderr == nil {
		return ExitError
	}
	if len(args) == 0 {
		writeUsage(env.Stderr)
		return ExitError
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		writeUsage(env.Stdout)
		return ExitOK
	}
	for _, candidate := range commands() {
		if candidate.name != name {
			continue
		}
		code, err := candidate.run(env, args[1:])
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return ExitOK
			}
			fmt.Fprintf(env.Stderr, "fluwatershed %s: %v\n", name, err)
			if code == ExitOK {
				return ExitError
			}
			return code
		}
		return code
	}
	fmt.Fprintf(env.Stderr, "fluwatershed: unknown command %q\n\n", name)
	writeUsage(env.Stderr)
	return ExitError
}

func writeUsage(out io.Writer) {
	fmt.Fprintf(out, "FluWatershed %s - offline environmental H5 surveillance signal processing\n\n", Version)
	fmt.Fprint(out, "Usage:\n  fluwatershed <command> [flags]\n\nCommands:\n")
	width := 0
	for _, candidate := range commands() {
		if len(candidate.name) > width {
			width = len(candidate.name)
		}
	}
	for _, candidate := range commands() {
		fmt.Fprintf(out, "  %-*s  %s\n", width, candidate.name, candidate.summary)
	}
	fmt.Fprint(out, "\nCommon flags:\n")
	for _, line := range commonFlagHelp() {
		fmt.Fprintf(out, "  %s\n", line)
	}
	fmt.Fprint(out, "\nEvery timestamp is RFC 3339 in UTC. Nothing reads the system clock: the\n")
	fmt.Fprint(out, "reporting instant comes from --as-of or from the newest instant in the input.\n")
	fmt.Fprint(out, "\nThis is a study tool. It gives no public-health, clinical, veterinary or\n")
	fmt.Fprint(out, "regulatory advice and must not be used for real outbreak decisions.\n")
}

func commonFlagHelp() []string {
	return []string{
		"--config PATH   JSON policy document; the built-in default profile is used when omitted",
		"--store PATH    local store directory holding ledgers, snapshots and the audit chain",
		"--network PATH  catchment definition JSON document",
		"--samples PATH  sample JSONL ledger",
		"--results PATH  assay result JSONL ledger",
		"--events PATH   carcass event JSONL ledger",
		"--as-of STAMP   reporting instant in RFC 3339 UTC",
		"--format FORM   text or json",
		"--out PATH      write to a file instead of standard output",
	}
}

// helpFor renders the flag help of one subcommand, used when parsing fails.
func helpFor(name string, set *flag.FlagSet) string {
	var lines []string
	set.VisitAll(func(f *flag.Flag) {
		lines = append(lines, fmt.Sprintf("  --%-9s %s", f.Name, f.Usage))
	})
	sort.Strings(lines)
	return fmt.Sprintf("usage: fluwatershed %s [flags]\n%s", name, strings.Join(lines, "\n"))
}
