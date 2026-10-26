package detector

import (
	"math"
	"testing"
	"time"
)

// helper to generate samples with constant slope and optional noise.
func genSamples(start time.Time, intervals []float64, base float64, slope float64, noise float64) []Sample {
	out := make([]Sample, 0, len(intervals))
	for _, d := range intervals {
		t := start.Add(time.Duration(d) * time.Second)
		y := base + slope*d
		if noise != 0 {
			y += noise * (math.Sin(d) * 0.1) // deterministic "noise"
		}
		out = append(out, Sample{T: t, P99Ms: y})
	}
	return out
}

func TestFitMetric(t *testing.T) {
	now := time.Now()
	start := now.Add(-10 * time.Second)
	intervals := []float64{0, 2, 4, 6, 8}
	// flat line
	flat := genSamples(start, intervals, 100, 0, 0)
	f, ok := FitMetric(flat, func(s Sample) float64 { return s.P99Ms }, now)
	if !ok || f.N != 5 || math.Abs(f.Slope) > 1e-12 {
		t.Fatalf("flat fit failed: %+v ok=%v", f, ok)
	}
	// up slope
	up := genSamples(start, intervals, 50, 5, 0)
	f, ok = FitMetric(up, func(s Sample) float64 { return s.P99Ms }, now)
	if !ok || f.N != 5 || math.Abs(f.Slope-5) > 1e-12 {
		t.Fatalf("up slope fit failed: %+v", f)
	}
	// down slope
	down := genSamples(start, intervals, 150, -5, 0)
	f, ok = FitMetric(down, func(s Sample) float64 { return s.P99Ms }, now)
	if !ok || f.N != 5 || math.Abs(f.Slope+5) > 1e-12 {
		t.Fatalf("down slope fit failed: %+v", f)
	}
	// noisy > interval broader than flat
	noisy := genSamples(start, intervals, 100, 5, 0.5)
	f, ok = FitMetric(noisy, func(s Sample) float64 { return s.P99Ms }, now)
	if !ok || math.Abs(f.Slope-5) > 1e-2 {
		t.Fatalf("noisy fit failed: %+v", f)
	}
	// far future prediction
	f, ok = FitMetric(up, func(s Sample) float64 { return s.P99Ms }, now)
	if !ok {
		t.Fatalf("fit not ok for future test")
	}
	future := now.Add(60 * time.Second)
	mean, lower, upper := f.Predict(future, 2) // 95% approx
	if lower > mean || upper < mean {
		t.Fatalf("prediction bounds invalid: mean=%v lo=%v hi=%v", mean, lower, upper)
	}
	// less than 3 points
	small := genSamples(start, []float64{0}, 100, 0, 0)
	_, ok = FitMetric(small, func(s Sample) float64 { return s.P99Ms }, now)
	if ok {
		t.Fatalf("fit should be false for 1 sample")
	}
	// NaN values
	bad := []Sample{{T: start, P99Ms: math.NaN()}}
	_, ok = FitMetric(bad, func(s Sample) float64 { return s.P99Ms }, now)
	if ok {
		t.Fatalf("fit should be false for NaN")
	}
	// all equal timestamps -> zero variance x
	eqts := []Sample{{T: start, P99Ms: 100}, {T: start, P99Ms: 110}}
	_, ok = FitMetric(eqts, func(s Sample) float64 { return s.P99Ms }, now)
	if ok {
		t.Fatalf("fit should be false for equal timestamps")
	}
	// ensure samples slice unchanged after FitMetric
	origSamples := genSamples(start, []float64{0, 2, 4}, 100, 0, 0)
	preCopy := make([]Sample, len(origSamples))
	copy(preCopy, origSamples)
	_, _ = FitMetric(origSamples, func(s Sample) float64 { return s.P99Ms }, now)
	for i, s := range origSamples {
		if s.T != preCopy[i].T || s.P99Ms != preCopy[i].P99Ms {
			t.Fatalf("sample modified at index %d", i)
		}
	}
}
