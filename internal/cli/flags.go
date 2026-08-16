package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"FluWatershed/internal/config"
	"FluWatershed/internal/model"
	"FluWatershed/internal/pipeline"
	"FluWatershed/internal/store"
	"FluWatershed/internal/strictjson"
	"FluWatershed/internal/timeutil"
)

// Format is an output encoding.
type Format string

// The two output encodings.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// options is the assembled command line for one subcommand. Not every
// subcommand registers every field; the registration helpers decide which flags
// exist so that an unsupported flag is an error rather than a silent no-op.
type options struct {
	storePath   string
	configPath  string
	networkPath string
	samplesPath string
	resultsPath string
	eventsPath  string
	asOfText    string
	siteFilter  string
	resultID    string
	format      string
	outPath     string
	writeStore  bool
	detail      bool
	emitConfig  bool
}

// commonFlags registers the flags shared by every analysis subcommand.
func (o *options) commonFlags(set *flag.FlagSet) {
	set.StringVar(&o.configPath, "config", "", "path to a JSON policy document (default profile when omitted)")
	set.StringVar(&o.format, "format", string(FormatText), "output encoding: text or json")
	set.StringVar(&o.outPath, "out", "", "write output to this file instead of standard output")
}

// storeFlag registers the store directory flag.
func (o *options) storeFlag(set *flag.FlagSet) {
	set.StringVar(&o.storePath, "store", "", "path to the local store directory")
}

// sourceFlags registers the input file flags.
func (o *options) sourceFlags(set *flag.FlagSet) {
	set.StringVar(&o.networkPath, "network", "", "path to the catchment definition JSON document")
	set.StringVar(&o.samplesPath, "samples", "", "path to the sample JSONL ledger")
	set.StringVar(&o.resultsPath, "results", "", "path to the assay result JSONL ledger")
	set.StringVar(&o.eventsPath, "events", "", "path to the carcass event JSONL ledger")
}

// asOfFlag registers the reporting instant flag.
func (o *options) asOfFlag(set *flag.FlagSet) {
	set.StringVar(&o.asOfText, "as-of", "",
		"reporting instant in RFC 3339 UTC; defaults to the newest instant in the input")
}

// resolveFormat validates the requested encoding.
func (o *options) resolveFormat() (Format, error) {
	switch Format(o.format) {
	case FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unknown format %q; use text or json", o.format)
	}
}

// resolveAsOf parses the reporting instant, returning an unset stamp when the
// caller did not supply one.
func (o *options) resolveAsOf() (timeutil.Stamp, error) {
	if strings.TrimSpace(o.asOfText) == "" {
		return timeutil.Stamp{}, nil
	}
	return timeutil.Parse(o.asOfText)
}

// resolveConfig loads the policy document, or returns the built-in default when
// no path was given.
func (o *options) resolveConfig() (config.Config, error) {
	if strings.TrimSpace(o.configPath) == "" {
		cfg := config.Default()
		if err := cfg.Validate(); err != nil {
			return config.Config{}, fmt.Errorf("built-in default profile is invalid: %w", err)
		}
		return cfg, nil
	}
	return config.Load(o.configPath)
}

// resolveStore opens the store directory named on the command line.
func (o *options) resolveStore() (*store.Store, error) {
	if strings.TrimSpace(o.storePath) == "" {
		return nil, fmt.Errorf("--store is required for this command")
	}
	return store.Open(o.storePath)
}

// loadBundle assembles the input set from whichever source the flags describe.
//
// Explicit input files win, because a caller who names them means to analyse
// those exact files. Otherwise the store's ledgers are used. Naming neither is an
// error rather than an empty analysis.
func (o *options) loadBundle() (model.Bundle, string, error) {
	hasFiles := strings.TrimSpace(o.networkPath) != ""
	hasStore := strings.TrimSpace(o.storePath) != ""
	switch {
	case hasFiles:
		bundle, err := pipeline.Load(pipeline.Sources{
			NetworkPath: o.networkPath,
			SamplesPath: o.samplesPath,
			ResultsPath: o.resultsPath,
			EventsPath:  o.eventsPath,
		})
		if err != nil {
			return model.Bundle{}, "", err
		}
		return bundle, "input files", nil
	case hasStore:
		handle, err := o.resolveStore()
		if err != nil {
			return model.Bundle{}, "", err
		}
		bundle, err := handle.LoadBundle()
		if err != nil {
			return model.Bundle{}, "", err
		}
		return bundle, "store " + handle.Root(), nil
	default:
		return model.Bundle{}, "", fmt.Errorf("provide either --network with its ledgers or --store")
	}
}

// writer returns the destination for output and a closer for it.
func (o *options) writer(fallback io.Writer) (io.Writer, func() error, error) {
	if strings.TrimSpace(o.outPath) == "" {
		return fallback, func() error { return nil }, nil
	}
	dir := filepath.Dir(o.outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create directory %s: %w", dir, err)
	}
	handle, err := os.Create(o.outPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", o.outPath, err)
	}
	return handle, handle.Close, nil
}

// emit writes either the JSON document or the text lines, whichever the caller
// asked for, and always terminates with exactly one newline.
func (o *options) emit(out io.Writer, format Format, document any, lines []string) error {
	if format == FormatJSON {
		payload, err := strictjson.Indented(document)
		if err != nil {
			return err
		}
		_, err = out.Write(payload)
		return err
	}
	_, err := io.WriteString(out, joinLines(lines))
	return err
}

func joinLines(lines []string) string {
	trimmed := make([]string, 0, len(lines))
	trimmed = append(trimmed, lines...)
	for len(trimmed) > 0 && strings.TrimSpace(trimmed[len(trimmed)-1]) == "" {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return ""
	}
	return strings.Join(trimmed, "\n") + "\n"
}

// newFlagSet builds a flag set that reports errors instead of exiting, so the
// caller controls the exit code and the message.
func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(out)
	return set
}

// requireNoArgs rejects positional arguments, which this CLI never takes.
func requireNoArgs(set *flag.FlagSet) error {
	if set.NArg() == 0 {
		return nil
	}
	extras := make([]string, set.NArg())
	copy(extras, set.Args())
	sort.Strings(extras)
	return fmt.Errorf("unexpected argument(s): %s", strings.Join(extras, " "))
}
