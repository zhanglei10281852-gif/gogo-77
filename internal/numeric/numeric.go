// Package numeric holds the deterministic arithmetic helpers shared by the
// quantification, baseline, trend and roll-up stages.
//
// Two rules govern everything here. First, no function mutates its input
// slice: order-sensitive statistics copy before sorting so that callers keep
// their own ordering. Second, every value that ever reaches a stored document
// or printed report passes through Round, so two runs on the same input write
// byte-identical output regardless of the order intermediate sums were formed.
package numeric

import (
	"errors"
	"math"
	"sort"
)

// ErrEmpty is returned by the statistics helpers when handed no samples.
var ErrEmpty = errors.New("no values supplied")

// Round returns value rounded half away from zero to digits decimal places.
// digits is clamped to the range [0, 12]; larger values gain nothing because
// float64 cannot represent them reliably.
func Round(value float64, digits int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	if digits < 0 {
		digits = 0
	}
	if digits > 12 {
		digits = 12
	}
	scale := math.Pow(10, float64(digits))
	scaled := value * scale
	if math.IsInf(scaled, 0) {
		return value
	}
	rounded := math.Floor(math.Abs(scaled) + 0.5)
	if value < 0 {
		rounded = -rounded
	}
	return rounded / scale
}

// Round2 rounds to two decimals, the fixed precision used for Ct values,
// temperatures, scores and ratios.
func Round2(value float64) float64 { return Round(value, 2) }

// Round4 rounds to four decimals, used for slopes, weights and shares.
func Round4(value float64) float64 { return Round(value, 4) }

// RoundSignificant rounds value to digits significant figures. Copy numbers
// span many orders of magnitude, so a fixed number of decimals is either
// wasteful or lossy; significant figures behave sensibly across the range.
func RoundSignificant(value float64, digits int) float64 {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	if digits < 1 {
		digits = 1
	}
	if digits > 15 {
		digits = 15
	}
	magnitude := math.Floor(math.Log10(math.Abs(value)))
	shift := float64(digits-1) - magnitude
	factor := math.Pow(10, shift)
	if math.IsInf(factor, 0) || factor == 0 {
		return value
	}
	return Round(value*factor, 0) / factor
}

// Mean is the arithmetic mean of values.
func Mean(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values)), nil
}

// Median is the classic median: the middle element of an odd-length sample and
// the mean of the two middle elements of an even-length sample.
func Median(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	sorted := Sorted(values)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid], nil
	}
	return (sorted[mid-1] + sorted[mid]) / 2, nil
}

// Quantile returns the fraction-th quantile using linear interpolation between
// the two neighbouring order statistics. fraction is clamped to [0, 1].
func Quantile(values []float64, fraction float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	sorted := Sorted(values)
	if len(sorted) == 1 {
		return sorted[0], nil
	}
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower], nil
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight, nil
}

// StdDev is the sample standard deviation, which needs at least two values.
func StdDev(values []float64) (float64, error) {
	if len(values) < 2 {
		return 0, ErrEmpty
	}
	mean, err := Mean(values)
	if err != nil {
		return 0, err
	}
	sumSquares := 0.0
	for _, v := range values {
		delta := v - mean
		sumSquares += delta * delta
	}
	return math.Sqrt(sumSquares / float64(len(values)-1)), nil
}

// MADScale converts a median absolute deviation into an estimate of the
// standard deviation of a normal distribution.
const MADScale = 1.4826

// RobustDispersion returns the median absolute deviation of values scaled by
// MADScale. It is the dispersion estimate used for every baseline because a
// single grossly high sample must not inflate the band it is compared against.
func RobustDispersion(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	centre, err := Median(values)
	if err != nil {
		return 0, err
	}
	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - centre)
	}
	mad, err := Median(deviations)
	if err != nil {
		return 0, err
	}
	return mad * MADScale, nil
}

// Sorted returns an ascending copy of values, leaving the input untouched.
func Sorted(values []float64) []float64 {
	out := make([]float64, len(values))
	copy(out, values)
	sort.Float64s(out)
	return out
}

// Min returns the smallest value.
func Min(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	best := values[0]
	for _, v := range values[1:] {
		if v < best {
			best = v
		}
	}
	return best, nil
}

// Max returns the largest value.
func Max(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, ErrEmpty
	}
	best := values[0]
	for _, v := range values[1:] {
		if v > best {
			best = v
		}
	}
	return best, nil
}

// Spread is Max minus Min, the replicate agreement measure used by quality
// control.
func Spread(values []float64) (float64, error) {
	low, err := Min(values)
	if err != nil {
		return 0, err
	}
	high, err := Max(values)
	if err != nil {
		return 0, err
	}
	return high - low, nil
}

// Clamp confines value to [low, high]. If the bounds arrive reversed they are
// swapped rather than producing nonsense.
func Clamp(value, low, high float64) float64 {
	if low > high {
		low, high = high, low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// SafeDiv divides numerator by denominator, returning fallback when the
// denominator is zero or the result is not finite.
func SafeDiv(numerator, denominator, fallback float64) float64 {
	if denominator == 0 {
		return fallback
	}
	out := numerator / denominator
	if math.IsNaN(out) || math.IsInf(out, 0) {
		return fallback
	}
	return out
}

// Log10Floor returns log10 of value, substituting floor for values at or below
// zero so that a non-detect can still take part in a log-scale regression.
func Log10Floor(value, floor float64) float64 {
	if floor <= 0 {
		floor = 1
	}
	if value < floor {
		value = floor
	}
	return math.Log10(value)
}

// Finite reports whether value is a usable real number.
func Finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// EarthRadiusKm is the mean radius used by GreatCircleKm.
const EarthRadiusKm = 6371.0088

// GreatCircleKm returns the great-circle distance in kilometres between two
// decimal-degree coordinates using the haversine form, which stays accurate at
// the short separations relevant to a carcass search radius.
func GreatCircleKm(lat1, lon1, lat2, lon2 float64) float64 {
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaPhi := (lat2 - lat1) * math.Pi / 180
	deltaLambda := (lon2 - lon1) * math.Pi / 180
	sinPhi := math.Sin(deltaPhi / 2)
	sinLambda := math.Sin(deltaLambda / 2)
	h := sinPhi*sinPhi + math.Cos(phi1)*math.Cos(phi2)*sinLambda*sinLambda
	if h < 0 {
		h = 0
	}
	if h > 1 {
		h = 1
	}
	return 2 * EarthRadiusKm * math.Asin(math.Sqrt(h))
}
