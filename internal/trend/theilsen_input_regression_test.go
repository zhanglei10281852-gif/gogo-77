package trend

import (
	"math"
	"testing"
)

func TestTheilSenLeavesItsInputSlicesUntouched(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 4}
	ys := []float64{9, 1, 7, 3, 5}
	wantXs := append([]float64(nil), xs...)
	wantYs := append([]float64(nil), ys...)

	slope, intercept, pairs, ok := TheilSen(xs, ys)
	if !ok {
		t.Fatal("TheilSen refused the series")
	}
	if pairs != 10 {
		t.Fatalf("pairs = %d", pairs)
	}
	for index := range wantYs {
		if ys[index] != wantYs[index] {
			t.Fatalf("the caller's values were rearranged: got %v, want %v", ys, wantYs)
		}
		if xs[index] != wantXs[index] {
			t.Fatalf("the caller's days were rearranged: got %v, want %v", xs, wantXs)
		}
	}

	again, againIntercept, _, ok := TheilSen(xs, ys)
	if !ok {
		t.Fatal("the second fit was refused")
	}
	if math.Abs(again-slope) > 1e-12 || math.Abs(againIntercept-intercept) > 1e-12 {
		t.Fatalf("refitting the same slices changed the answer: %v/%v then %v/%v",
			slope, intercept, again, againIntercept)
	}
}
