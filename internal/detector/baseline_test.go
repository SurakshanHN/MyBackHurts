// internal/detector/baseline_test.go
//
// Tests for the linear regression helpers in baseline.go.  They are
// intentionally simple – we construct deterministic sample series and
// compare the predicted values against the formula we use in the
// implementation.  No external testing frameworks are used beyond Go's
// standard `testing` package.

package detector

import (
	"math"
	"testing"
	"time"
)

// helper to build evenly spaced samples.
func buildSamples(start time.Time, intervals []float64, fn func(t float64) Sample) []Sample {
	samples := make([]Sample, 0, len(intervals))
	for _, d := range intervals {
		samples = append(samples, fn(d))
	}
	return samples
}

func TestProjectP99MsFlat(t *testing.T) {
	now := time.Now()
	start := now.Add(-10 * time.Second)
	// 5 samples spaced 2s apart, all with P99Ms = 100
	samples := buildSamples(start, []float64{0, 2, 4, 6, 8}, func(d float64) Sample {
		return Sample{T: start.Add(time.Duration(d) * time.Second), P99Ms: 100}
	})
	preds := ProjectP99Ms(samples, now, 10*time.Second, 3)
	// Expect all predictions 100
	for _, p := range preds {
		if math.Abs(p-100) > 1e-6 {
			t.Fatalf("expected 100 got %v", p)
		}
	}
}

func TestProjectP99MsSlope(t *testing.T) {
	now := time.Now()
	start := now.Add(-10 * time.Second)
	// slope 5 per second, intercept 50
	samples := buildSamples(start, []float64{0, 2, 4, 6, 8}, func(d float64) Sample {
		return Sample{T: start.Add(time.Duration(d) * time.Second), P99Ms: 50 + 5*d}
	})
	preds := ProjectP99Ms(samples, now, 6*time.Second, 4)
	// Expected values: for elapsed 0,2,4,6
	expected := []float64{50, 60, 70, 80}
	for i, p := range preds {
		if math.Abs(p-expected[i]) > 1e-6 {
			t.Fatalf("idx %d: expected %v got %v", i, expected[i], p)
		}
	}
}

func TestProjectErrRateNoisy(t *testing.T) {
	now := time.Now()
	start := now.Add(-8 * time.Second)
	// true line slope 0.1 per second, but add noise +/-0.02
	samples := buildSamples(start, []float64{0, 2, 4, 6, 8}, func(d float64) Sample {
		return Sample{T: start.Add(time.Duration(d) * time.Second), ErrRate: 0.05 + 0.1*d}
	})
	preds := ProjectErrRate(samples, now, 4*time.Second, 3)
	// Test that predictions are close to real line; allow small error.
	for i, p := range preds {
		exp := 0.05 + 0.1*float64(i*2) // 0s,2s,4s
		if math.Abs(p-exp) > 0.05 {
			t.Fatalf("idx %d: expected approx %v got %v", i, exp, p)
		}
	}
}

func TestProjectWithFewSamples(t *testing.T) {
	// Less than 2 samples should produce zeros
	samples := []Sample{{T: time.Now(), P99Ms: 100}}
	preds := ProjectP99Ms(samples, time.Now(), time.Second, 3)
	for _, p := range preds {
		if p != 0 {
			t.Fatalf("expected 0 got %v", p)
		}
	}
}
