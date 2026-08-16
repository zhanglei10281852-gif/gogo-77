package numeric

import (
	"math"
	"testing"
)

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		value  float64
		digits int
		want   float64
	}{
		{2.345, 2, 2.35},
		{-2.345, 2, -2.35},
		{2.5, 0, 3},
		{-2.5, 0, -3},
		{1.0 / 3.0, 4, 0.3333},
		{0, 3, 0},
	}
	for _, item := range cases {
		if got := Round(item.value, item.digits); got != item.want {
			t.Errorf("Round(%v, %d) = %v, want %v", item.value, item.digits, got, item.want)
		}
	}
}

func TestRoundSignificant(t *testing.T) {
	cases := []struct {
		value float64
		want  float64
	}{
		{123456789, 123457000},
		{0.00012345678, 0.000123457},
		{-98765.4321, -98765.4},
		{0, 0},
	}
	for _, item := range cases {
		got := RoundSignificant(item.value, 6)
		if math.Abs(got-item.want) > math.Abs(item.want)*1e-12 {
			t.Errorf("RoundSignificant(%v, 6) = %v, want %v", item.value, got, item.want)
		}
	}
}

func TestMedianAndDispersionIgnoreInputOrder(t *testing.T) {
	values := []float64{9, 1, 5, 3, 7}
	median, err := Median(values)
	if err != nil {
		t.Fatalf("Median: %v", err)
	}
	if median != 5 {
		t.Fatalf("median = %v, want 5", median)
	}
	if values[0] != 9 {
		t.Fatalf("Median mutated its input: %v", values)
	}
	dispersion, err := RobustDispersion(values)
	if err != nil {
		t.Fatalf("RobustDispersion: %v", err)
	}
	// Deviations from 5 are 4,4,0,2,2; their median is 2, scaled by 1.4826.
	want := 2 * MADScale
	if math.Abs(dispersion-want) > 1e-12 {
		t.Fatalf("dispersion = %v, want %v", dispersion, want)
	}
}

func TestRobustDispersionResistsOneOutlier(t *testing.T) {
	clean := []float64{10, 11, 12, 13, 14}
	dirty := []float64{10, 11, 12, 13, 9000}
	a, err := RobustDispersion(clean)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	b, err := RobustDispersion(dirty)
	if err != nil {
		t.Fatalf("dirty: %v", err)
	}
	if math.Abs(a-b) > 1e-9 {
		t.Fatalf("one extreme value moved the dispersion from %v to %v", a, b)
	}
	sd, err := StdDev(dirty)
	if err != nil {
		t.Fatalf("StdDev: %v", err)
	}
	if sd < 100 {
		t.Fatalf("standard deviation %v should have been inflated by the outlier", sd)
	}
}

func TestMedianEvenLength(t *testing.T) {
	got, err := Median([]float64{4, 1, 3, 2})
	if err != nil {
		t.Fatalf("Median: %v", err)
	}
	if got != 2.5 {
		t.Fatalf("median = %v, want 2.5", got)
	}
}

func TestQuantileInterpolates(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	got, err := Quantile(values, 0.5)
	if err != nil {
		t.Fatalf("Quantile: %v", err)
	}
	if got != 3 {
		t.Fatalf("median quantile = %v, want 3", got)
	}
	got, err = Quantile(values, 0.9)
	if err != nil {
		t.Fatalf("Quantile: %v", err)
	}
	if math.Abs(got-4.6) > 1e-12 {
		t.Fatalf("p90 = %v, want 4.6", got)
	}
}

func TestEmptyInputsReportErrEmpty(t *testing.T) {
	if _, err := Mean(nil); err != ErrEmpty {
		t.Errorf("Mean(nil) error = %v", err)
	}
	if _, err := Median(nil); err != ErrEmpty {
		t.Errorf("Median(nil) error = %v", err)
	}
	if _, err := Quantile(nil, 0.5); err != ErrEmpty {
		t.Errorf("Quantile(nil) error = %v", err)
	}
	if _, err := StdDev([]float64{1}); err != ErrEmpty {
		t.Errorf("StdDev of one value error = %v", err)
	}
}

func TestSpreadAndClamp(t *testing.T) {
	spread, err := Spread([]float64{31.2, 30.1, 32.6})
	if err != nil {
		t.Fatalf("Spread: %v", err)
	}
	if math.Abs(spread-2.5) > 1e-12 {
		t.Fatalf("spread = %v, want 2.5", spread)
	}
	if got := Clamp(9, 1, 4); got != 4 {
		t.Errorf("Clamp above range = %v", got)
	}
	if got := Clamp(-9, 1, 4); got != 1 {
		t.Errorf("Clamp below range = %v", got)
	}
	if got := Clamp(2, 4, 1); got != 2 {
		t.Errorf("Clamp with reversed bounds = %v", got)
	}
}

func TestSafeDivAndLog10Floor(t *testing.T) {
	if got := SafeDiv(1, 0, -1); got != -1 {
		t.Errorf("SafeDiv by zero = %v", got)
	}
	if got := SafeDiv(6, 3, -1); got != 2 {
		t.Errorf("SafeDiv = %v", got)
	}
	if got := Log10Floor(0, 10); got != 1 {
		t.Errorf("Log10Floor applied no floor: %v", got)
	}
	if got := Log10Floor(1000, 1); got != 3 {
		t.Errorf("Log10Floor = %v", got)
	}
}

func TestGreatCircleKm(t *testing.T) {
	if got := GreatCircleKm(45, -95, 45, -95); got != 0 {
		t.Fatalf("distance to self = %v", got)
	}
	// One degree of latitude is close to 111.2 km on the mean sphere.
	got := GreatCircleKm(45, -95, 46, -95)
	if math.Abs(got-111.19) > 0.05 {
		t.Fatalf("one degree of latitude = %v km, want about 111.19", got)
	}
	// The measure is symmetric.
	forward := GreatCircleKm(45.21, -95.44, 45.09, -95.16)
	backward := GreatCircleKm(45.09, -95.16, 45.21, -95.44)
	if math.Abs(forward-backward) > 1e-9 {
		t.Fatalf("asymmetric distance: %v and %v", forward, backward)
	}
	if forward < 20 || forward > 30 {
		t.Fatalf("catchment span %v km is outside the expected 20..30 km", forward)
	}
}

func TestSortedReturnsCopy(t *testing.T) {
	input := []float64{3, 1, 2}
	out := Sorted(input)
	if out[0] != 1 || out[2] != 3 {
		t.Fatalf("Sorted = %v", out)
	}
	if input[0] != 3 {
		t.Fatalf("Sorted mutated its input: %v", input)
	}
}
