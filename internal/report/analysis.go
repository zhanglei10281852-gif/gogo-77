package report

import (
	"fmt"
	"strconv"

	"FluWatershed/internal/config"
	"FluWatershed/internal/normalize"
	"FluWatershed/internal/pipeline"
	"FluWatershed/internal/qc"
	"FluWatershed/internal/quant"
	"FluWatershed/internal/trend"
)

// Validation renders a validation report.
func Validation(report pipeline.ValidationReport) []string {
	lines := Section("Input validation", KeyValues([][2]string{
		{"catchment", Dash(report.CatchmentID)},
		{"sites", strconv.Itoa(report.Sites)},
		{"links", strconv.Itoa(report.Links)},
		{"samples", strconv.Itoa(report.Samples)},
		{"results", strconv.Itoa(report.Results)},
		{"carcass events", strconv.Itoa(report.Events)},
		{"topology", Dash(report.TopologyNote)},
		{"headwaters", JoinOrDash(report.Headwaters)},
		{"outlets", JoinOrDash(report.Outlets)},
		{"verdict", verdictWord(report.Valid)},
	}))
	if len(report.Problems) == 0 {
		return lines
	}
	table := NewTable("SCOPE", "REFERENCE", "PROBLEM")
	for _, problem := range report.Problems {
		table.Add(problem.Scope, Dash(problem.Ref), problem.Message)
	}
	return append(lines, Section(fmt.Sprintf("Problems (%d)", len(report.Problems)), table.Render())...)
}

func verdictWord(valid bool) string {
	if valid {
		return "every check passed"
	}
	return "one or more checks failed"
}

// Header renders the common analysis heading.
func Header(analysis pipeline.Analysis, cfg config.Config) []string {
	return Section("FluWatershed analysis", KeyValues([][2]string{
		{"catchment", Dash(analysis.CatchmentID)},
		{"reporting instant", analysis.AsOf.String()},
		{"profile", cfg.ProfileID},
		{"sites / links", fmt.Sprintf("%d / %d", analysis.Counts.Sites, analysis.Counts.Links)},
		{"samples / results", fmt.Sprintf("%d / %d", analysis.Counts.Samples, analysis.Counts.Results)},
		{"carcass events", strconv.Itoa(analysis.Counts.Events)},
		{"measurements", strconv.Itoa(analysis.Counts.Measurements)},
		{"usable points", strconv.Itoa(analysis.Counts.UsablePoints)},
		{"series days", strconv.Itoa(analysis.Counts.SeriesDays)},
		{"topological order", JoinOrDash(analysis.Topology)},
	}))
}

// Quantification renders the quantified measurements.
func Quantification(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "DAY", "RESULT", "GENE", "STATUS", "QUANT/DET/TOT",
		"COPIES/L", "LOWER", "UPPER", "LOQ/L", "CT SPREAD").RightAlign(6, 7, 8, 9, 10)
	for _, measurement := range analysis.Measurements {
		table.Add(
			measurement.SiteID,
			measurement.Day,
			measurement.ResultID,
			measurement.TargetGene,
			shortStatus(measurement.Status),
			fmt.Sprintf("%d/%d/%d", measurement.QuantifiableReplicates,
				measurement.DetectedReplicates, measurement.TotalReplicates),
			Sci(measurement.CopiesPerLitre),
			Sci(measurement.LowerCopiesPerLitre),
			Sci(measurement.UpperCopiesPerLitre),
			Sci(measurement.LOQCopiesPerLitre),
			Fixed(measurement.CtSpread, 2),
		)
	}
	lines := Section(fmt.Sprintf("Quantification (%d measurement(s))", table.Rows()), table.Render())
	if len(analysis.Skipped) > 0 {
		skipTable := NewTable("SKIPPED")
		for _, item := range analysis.Skipped {
			skipTable.Add(item)
		}
		lines = append(lines, Section("Skipped results", skipTable.Render())...)
	}
	return lines
}

func shortStatus(status quant.Status) string {
	switch status {
	case quant.StatusQuantified:
		return "quantified"
	case quant.StatusDetectedNotQuantified:
		return "below-loq"
	default:
		return "non-detect"
	}
}

// Quality renders the quality-control assessments and the reasons results were
// barred from trends.
func Quality(analysis pipeline.Analysis) []string {
	tally := analysis.QualityTally
	lines := Section("Quality control", KeyValues([][2]string{
		{"assessed", strconv.Itoa(tally.Total)},
		{"passed", strconv.Itoa(tally.Passed)},
		{"warned", strconv.Itoa(tally.Warned)},
		{"failed", strconv.Itoa(tally.Failed)},
		{"barred from trends", JoinOrDash(analysis.FailedResults())},
	}))
	table := NewTable("SITE", "DAY", "RESULT", "VERDICT", "USABLE", "FAILED", "WARNED")
	for _, assessment := range analysis.Assessments {
		table.Add(
			assessment.SiteID,
			assessment.Day,
			assessment.ResultID,
			string(assessment.Verdict),
			YesNo(assessment.UsableForTrend),
			JoinOrDash(assessment.Failed),
			JoinOrDash(assessment.Warned),
		)
	}
	lines = append(lines, Section("Assessments", table.Render())...)
	failures := qc.FailureReasons(analysis.Assessments)
	if len(failures) > 0 {
		reasonTable := NewTable("CHECK", "FAILURES").RightAlign(1)
		for _, failure := range failures {
			reasonTable.Add(failure.Name, Fixed(failure.Observed, 0))
		}
		lines = append(lines, Section("Failure counts", reasonTable.Render())...)
	}
	return lines
}

// QualityDetail renders every individual check for one result, used when the
// caller asks for a single result rather than the whole run.
func QualityDetail(assessment qc.Assessment) []string {
	table := NewTable("CHECK", "SEVERITY", "OBSERVED", "LIMIT", "DETAIL").RightAlign(2, 3)
	for _, check := range assessment.Checks {
		table.Add(check.Name, string(check.Severity), Fixed(check.Observed, 2), Fixed(check.Limit, 2), check.Detail)
	}
	return Section(fmt.Sprintf("Checks for %s (%s)", assessment.ResultID, assessment.Verdict), table.Render())
}

// Normalisation renders the normalised points.
func Normalisation(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "DAY", "RESULT", "COPIES/L", "IND.FACTOR", "CORRECTED",
		"FLOW L/DAY", "SOURCE", "LOAD/DAY", "PER PERSON", "UNIT", "USABLE").
		RightAlign(3, 4, 5, 6, 8, 9)
	for _, point := range analysis.Points {
		table.Add(
			point.SiteID,
			point.Day,
			point.ResultID,
			Sci(point.CopiesPerLitre),
			Fixed(point.IndicatorFactor, 4),
			Sci(point.CorrectedCopiesPerLitre),
			Sci(point.FlowLitresPerDay),
			string(point.FlowSource),
			Sci(point.LoadCopiesPerDay),
			Sci(point.LoadPerPersonDay),
			shortUnit(point.SignalUnit),
			YesNo(point.Usable),
		)
	}
	return Section(fmt.Sprintf("Normalisation (%d point(s))", table.Rows()), table.Render())
}

func shortUnit(unit normalize.Unit) string {
	if unit == normalize.UnitCopiesPerDay {
		return "copies/day"
	}
	return "copies/L"
}

// Baselines renders the per-site rolling baselines.
func Baselines(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "UNIT", "AS OF", "WINDOW", "POINTS", "MEDIAN", "DISPERSION",
		"P90", "MIN", "MAX", "STATUS").RightAlign(4, 5, 6, 7, 8, 9)
	for _, item := range analysis.Baselines {
		status := "usable"
		if !item.Sufficient {
			status = "insufficient"
		}
		window := "-"
		if item.Window.From.IsSet() {
			window = item.Window.From.Day() + ".." + item.Window.To.Day()
		}
		table.Add(
			item.SiteID,
			shortUnit(item.Unit),
			item.AsOf.Day(),
			window,
			strconv.Itoa(item.Points),
			Sci(item.Median),
			Sci(item.Dispersion),
			Sci(item.P90),
			Sci(item.Minimum),
			Sci(item.Maximum),
			status,
		)
	}
	return Section(fmt.Sprintf("Baselines (%d site(s))", table.Rows()), table.Render())
}

// Detection renders the trend and exceedance picture.
func Detection(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "MATRIX", "LATEST DAY", "LATEST VALUE", "UNIT", "RATIO", "Z",
		"FLAGGED", "RUN", "SUSTAINED", "SLOPE", "DIRECTION", "FOLD", "DOUBLING").
		RightAlign(3, 5, 6, 7, 8, 10, 12, 13)
	for _, item := range analysis.Trends {
		ratio, robustZ := 0.0, 0.0
		if len(item.Exceedances) > 0 {
			last := item.Exceedances[len(item.Exceedances)-1]
			ratio, robustZ = last.Ratio, last.RobustZ
		}
		doubling := "-"
		if item.Slope.DoublingDays > 0 {
			doubling = Fixed(item.Slope.DoublingDays, 2)
		}
		table.Add(
			item.SiteID,
			string(item.Matrix),
			Dash(item.LatestDay),
			Sci(item.LatestValue),
			shortUnit(item.Unit),
			Fixed(ratio, 2),
			Fixed(robustZ, 2),
			strconv.Itoa(item.FlaggedDays),
			strconv.Itoa(item.ConsecutiveFlagged),
			YesNo(item.SustainedRise),
			Fixed(item.Slope.Slope, 4),
			string(item.Slope.Direction),
			Fixed(item.FoldChangeOverWindow, 2),
			doubling,
		)
	}
	lines := Section(fmt.Sprintf("Trend and exceedance (%d site(s))", table.Rows()), table.Render())
	summaryTable := NewTable("SITE", "SUMMARY")
	for _, item := range analysis.Trends {
		summaryTable.Add(item.SiteID, item.Summary)
	}
	return append(lines, Section("Site summaries", summaryTable.Render())...)
}

// Exceedances renders every flagged day across all sites.
func Exceedances(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "DAY", "VALUE", "BASELINE", "RATIO", "Z", "THRESHOLD",
		"TRIGGERS", "EXPLANATION").RightAlign(2, 3, 4, 5, 6)
	for _, item := range analysis.Trends {
		for _, exceedance := range item.Exceedances {
			if !exceedance.Exceeded {
				continue
			}
			triggers := make([]string, 0, len(exceedance.Triggers))
			for _, trigger := range exceedance.Triggers {
				triggers = append(triggers, string(trigger))
			}
			table.Add(
				exceedance.SiteID,
				exceedance.Day,
				Sci(exceedance.Value),
				Sci(exceedance.BaselineMedian),
				Fixed(exceedance.Ratio, 2),
				Fixed(exceedance.RobustZ, 2),
				Sci(exceedance.AbsoluteThreshold),
				JoinOrDash(triggers),
				exceedance.Explanation,
			)
		}
	}
	return Section(fmt.Sprintf("Flagged days (%d)", table.Rows()), table.Render())
}

// Series renders each site's daily series.
func Series(analysis pipeline.Analysis) []string {
	table := NewTable("SITE", "TARGET", "DAY", "VALUE", "UNIT", "DETECTED", "QUANTIFIED", "RESULTS").
		RightAlign(3)
	for _, series := range analysis.Series {
		for _, bin := range series.Days {
			table.Add(
				series.SiteID,
				series.TargetGene,
				bin.Day,
				Sci(bin.Value),
				shortUnit(series.Unit),
				YesNo(bin.Detected),
				YesNo(bin.Quantified),
				JoinOrDash(bin.ResultIDs),
			)
		}
	}
	return Section(fmt.Sprintf("Daily series (%d day-site row(s))", table.Rows()), table.Render())
}

// TrendDetail renders the full exceedance history of one site.
func TrendDetail(item trend.SiteTrend) []string {
	table := NewTable("DAY", "VALUE", "BASELINE", "DISPERSION", "RATIO", "Z", "FLAGGED", "EXPLANATION").
		RightAlign(1, 2, 3, 4, 5)
	for _, exceedance := range item.Exceedances {
		table.Add(
			exceedance.Day,
			Sci(exceedance.Value),
			Sci(exceedance.BaselineMedian),
			Sci(exceedance.BaselineDispersion),
			Fixed(exceedance.Ratio, 2),
			Fixed(exceedance.RobustZ, 2),
			YesNo(exceedance.Exceeded),
			exceedance.Explanation,
		)
	}
	title := fmt.Sprintf("Trend detail for %s (%s)", item.SiteID, item.Slope.Reason)
	return Section(title, table.Render())
}
