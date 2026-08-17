// Package trend detects exceedance, sustained rise and direction of travel on a
// site's daily series.
//
// The slope estimator is Theil-Sen: the median of the slopes of every pair of
// distinct points. It is used instead of least squares for two reasons. It
// tolerates the occasional very high day that a surveillance series always
// contains, and it is computed from a sorted median rather than an accumulated
// sum, so the answer does not depend on the order the pairs were visited. The
// regression runs on log10 of the signal, because growth in shed material is
// multiplicative, and a slope of 0.05 then reads directly as "an eightfold rise
// in about eighteen days".
package trend

import (
	"fmt"
	"math"
	"sort"

	"FluWatershed/internal/numeric"
)

// Direction is the qualitative reading of a slope.
type Direction string

// The possible directions.
const (
	DirectionRising       Direction = "rising"
	DirectionFalling      Direction = "falling"
	DirectionFlat         Direction = "flat"
	DirectionInsufficient Direction = "insufficient_data"
)

// Slope is a Theil-Sen fit over log10 values.
type Slope struct {
	Slope        float64   `json:"slope_log10_per_day"`
	Intercept    float64   `json:"intercept_log10"`
	Pairs        int       `json:"pairs"`
	Points       int       `json:"points"`
	Sufficient   bool      `json:"sufficient"`
	Direction    Direction `json:"direction"`
	DoublingDays float64   `json:"doubling_days"`
	SpanDays     int       `json:"span_days"`
	Reason       string    `json:"reason"`
}

// TheilSen returns the median pairwise slope and the matching intercept.
//
// The slope is median over all i<j with xi != xj of (yj-yi)/(xj-xi). The
// intercept is median over all i of (yi - slope*xi), which is the standard
// companion estimator and keeps the fitted line inside the data. Both medians
// are taken from an explicitly sorted slice, so the result is exactly
// reproducible.
func TheilSen(xs, ys []float64) (slope, intercept float64, pairs int, ok bool) {
	if len(xs) != len(ys) || len(xs) < 2 {
		return 0, 0, 0, false
	}
	slopes := make([]float64, 0, len(xs)*(len(xs)-1)/2)
	for i := 0; i < len(xs); i++ {
		for j := i + 1; j < len(xs); j++ {
			dx := xs[j] - xs[i]
			if dx == 0 {
				continue
			}
			candidate := (ys[j] - ys[i]) / dx
			if !numeric.Finite(candidate) {
				continue
			}
			slopes = append(slopes, candidate)
		}
	}
	if len(slopes) == 0 {
		return 0, 0, 0, false
	}
	sort.Float64s(slopes)
	medianSlope, err := numeric.Median(slopes)
	if err != nil {
		return 0, 0, 0, false
	}
	// Build residuals in a fresh slice so the caller's ys is left untouched;
	// sorting it in place would otherwise rearrange the input observations.
	residuals := make([]float64, len(ys))
	for index := range xs {
		residuals[index] = ys[index] - medianSlope*xs[index]
	}
	sort.Float64s(residuals)
	medianIntercept, err := numeric.Median(residuals)
	if err != nil {
		return 0, 0, 0, false
	}
	return medianSlope, medianIntercept, len(slopes), true
}

// FitLog10 runs TheilSen on log10 of the values, using floor for any value at
// or below zero so that a non-detecting day still anchors the line instead of
// dropping out of it.
func FitLog10(dayIndices []int, values []float64, floor float64, minPoints int,
	risingCut, fallingCut float64) Slope {
	fit := Slope{Points: len(values), Direction: DirectionInsufficient}
	if len(dayIndices) != len(values) {
		fit.Reason = "day and value counts disagree"
		return fit
	}
	if len(values) < minPoints {
		fit.Reason = fmt.Sprintf("%d point(s) is below the %d point minimum", len(values), minPoints)
		return fit
	}
	xs := make([]float64, len(dayIndices))
	ys := make([]float64, len(values))
	for index := range values {
		xs[index] = float64(dayIndices[index])
		ys[index] = numeric.Log10Floor(values[index], floor)
	}
	slope, intercept, pairs, ok := TheilSen(xs, ys)
	if !ok {
		fit.Reason = "no usable pair of distinct days"
		return fit
	}
	fit.Slope = numeric.Round4(slope)
	fit.Intercept = numeric.Round4(intercept)
	fit.Pairs = pairs
	fit.Sufficient = true
	if len(xs) > 1 {
		fit.SpanDays = int(xs[len(xs)-1] - xs[0])
	}
	switch {
	case slope >= risingCut:
		fit.Direction = DirectionRising
	case slope <= fallingCut:
		fit.Direction = DirectionFalling
	default:
		fit.Direction = DirectionFlat
	}
	if slope > 0 {
		fit.DoublingDays = numeric.Round2(math.Log10(2) / slope)
	}
	fit.Reason = fmt.Sprintf("Theil-Sen over %d point(s) and %d pair(s) spanning %d day(s)",
		fit.Points, fit.Pairs, fit.SpanDays)
	return fit
}

// FoldChangeOverSpan is the multiplicative change the slope implies across days
// days: 10 raised to slope times days.
func (s Slope) FoldChangeOverSpan(days int) float64 {
	if !s.Sufficient || days <= 0 {
		return 1
	}
	return numeric.Round4(math.Pow(10, s.Slope*float64(days)))
}
