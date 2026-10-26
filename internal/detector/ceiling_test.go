// internal/detector/ceiling_test.go
//
// Tests for ceiling.go (ConnectionCeiling, SafeCeiling).
// All data is generated deterministically (seeded RNG or arithmetic).

package detector

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ConnectionCeiling
// ---------------------------------------------------------------------------

func TestConnectionCeiling_Basic(t *testing.T) {
	cases := []struct {
		max, perPod, want int
	}{
		{100, 10, 10},
		{100, 3, 33},
		{1, 1, 1},
		{0, 10, 0},
		{10, 0, 0},
		{-1, 10, 0},
		{10, -1, 0},
		{0, 0, 0},
	}
	for _, c := range cases {
		got := ConnectionCeiling(c.max, c.perPod)
		if got != c.want {
			t.Errorf("ConnectionCeiling(%d,%d) = %d, want %d", c.max, c.perPod, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// SafeCeiling helpers
// ---------------------------------------------------------------------------

// ceilSeries builds a []Sample with nReplicas replicas and n*nReplicas total
// samples, each group having a different replica count.
// replicaCounts is the list of distinct replica levels.
// The times are evenly spaced 10s apart.
func ceilSeries(start time.Time, replicaCounts []int, samplesPerLevel int) []Sample {
	var out []Sample
	t := start
	for _, rep := range replicaCounts {
		for j := 0; j < samplesPerLevel; j++ {
			out = append(out, Sample{
				T:        t,
				Replicas: rep,
				Ready:    rep,
				RPS:      100,
				P99Ms:    200,
				ErrRate:  0.01,
			})
			t = t.Add(10 * time.Second)
		}
	}
	return out
}

// ceilDep builds dep samples that linearly track Saturation = slope*replicas + intercept + noise.
func ceilDep(start time.Time, replicaCounts []int, samplesPerLevel int,
	slope, intercept float64, noiseSeed int64, noiseStd float64,
) []DepSample {
	r := rand.New(rand.NewSource(noiseSeed))
	var out []DepSample
	t := start
	for _, rep := range replicaCounts {
		for j := 0; j < samplesPerLevel; j++ {
			sat := slope*float64(rep) + intercept + noiseStd*r.NormFloat64()
			if sat < 0 {
				sat = 0
			}
			if sat > 1 {
				sat = 1
			}
			out = append(out, DepSample{
				T:          t,
				Saturation: sat,
				LatencyMs:  100,
			})
			t = t.Add(10 * time.Second)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Good linear data
// ---------------------------------------------------------------------------

func TestSafeCeiling_GoodLinear(t *testing.T) {
	// Linear: Saturation = 0.05 * replicas + 0.1
	// At threshold 80%: replicas = (0.8 - 0.1) / 0.05 = 14.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0.05, 0.1, 42, 0.001)

	replicas, ok := SafeCeiling(series, dep, 80.0)
	if !ok {
		t.Fatal("SafeCeiling: expected ok=true for good linear data")
	}
	// floor((0.8 - 0.1) / 0.05) = floor(14.0) = 14.
	if replicas < 12 || replicas > 16 {
		t.Errorf("SafeCeiling: expected replicas near 14, got %d", replicas)
	}
}

// ---------------------------------------------------------------------------
// Flat data (slope ≈ 0)
// ---------------------------------------------------------------------------

func TestSafeCeiling_FlatData(t *testing.T) {
	// Saturation = 0.5 for all replica levels (slope ≈ 0).
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0, 0.5, 99, 0.001)

	_, ok := SafeCeiling(series, dep, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for flat (zero slope) data")
	}
}

// ---------------------------------------------------------------------------
// Fewer than 3 distinct replica levels
// ---------------------------------------------------------------------------

func TestSafeCeiling_FewerThan3Levels(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4} // only 2 levels
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0.05, 0.1, 7, 0.001)

	_, ok := SafeCeiling(series, dep, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for fewer than 3 replica levels")
	}
}

// ---------------------------------------------------------------------------
// Noisy data (R² < 0.7)
// ---------------------------------------------------------------------------

func TestSafeCeiling_NoisyData(t *testing.T) {
	// High noise → R² will fall below 0.7.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	// Very high noise (std = 0.5 vs slope = 0.01 → terrible R²).
	dep := ceilDep(start, reps, 5, 0.01, 0.1, 17, 0.5)

	_, ok := SafeCeiling(series, dep, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for noisy data (R² < 0.7)")
	}
}

// ---------------------------------------------------------------------------
// Far extrapolation
// ---------------------------------------------------------------------------

func TestSafeCeiling_FarExtrapolation(t *testing.T) {
	// slope very small → ceiling far in the future.
	// Saturation = 0.001 * replicas + 0.1; threshold = 80% → 700 replicas.
	// Max observed = 10 → 700 > 2*10 = 20 → refused.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0.001, 0.1, 55, 0.0001)

	_, ok := SafeCeiling(series, dep, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for far extrapolation")
	}
}

// ---------------------------------------------------------------------------
// NaN in dep samples
// ---------------------------------------------------------------------------

func TestSafeCeiling_NaN(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0.05, 0.1, 42, 0.001)
	// Inject NaN.
	for i := range dep {
		dep[i].Saturation = math.NaN()
	}
	_, ok := SafeCeiling(series, dep, 80.0)
	// All pairs will be skipped due to NaN → fewer than 3 → ok=false.
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for all-NaN saturation")
	}
}

// ---------------------------------------------------------------------------
// Empty input
// ---------------------------------------------------------------------------

func TestSafeCeiling_EmptySeries(t *testing.T) {
	_, ok := SafeCeiling(nil, nil, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for nil inputs")
	}
}

func TestSafeCeiling_EmptyDep(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := ceilSeries(start, []int{2, 4, 6}, 5)
	_, ok := SafeCeiling(series, nil, 80.0)
	if ok {
		t.Fatal("SafeCeiling: expected ok=false for empty dep")
	}
}

// ---------------------------------------------------------------------------
// Result must be >= 1
// ---------------------------------------------------------------------------

func TestSafeCeiling_MinimumOne(t *testing.T) {
	// Threshold very low → raw formula gives 0 → should be clamped to 1.
	// Saturation = 0.05 * replicas + 0.5; threshold = 1% (0.01):
	// replicas = (0.01 - 0.5) / 0.05 = -9.8 → floor(-9.8) = -10 → clamped to 1.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reps := []int{2, 4, 6, 8, 10}
	series := ceilSeries(start, reps, 5)
	dep := ceilDep(start, reps, 5, 0.05, 0.5, 42, 0.001)

	// At threshold 1%: (0.01 - 0.5) / 0.05 = -9.8 → clamped to 1,
	// but max observed is 10, so 1 ≤ 2*10 = 20 → ok=true.
	replicas, ok := SafeCeiling(series, dep, 1.0)
	if !ok {
		t.Fatal("SafeCeiling: expected ok=true even when formula gives negative")
	}
	if replicas < 1 {
		t.Errorf("SafeCeiling: replicas should be >= 1, got %d", replicas)
	}
}
