package detector

import "math"

// FindShift implements a two‑sided CUSUM change‑point detector on the
// standardized series.  The first half of `values` is used as the baseline.
// It returns the earliest index where either cumulative sum exceeds the
// threshold `h`; found is true only when a change point is detected.

// Defaults: if k <= 0, k = 0.5; if h <= 0, h = 5.
// Preconditions: len(values) >= 8, standard deviation of first half > 0,
// and all values must be finite.
func FindShift(values []float64, k, h float64) (index int, found bool) {
    // require at least 10 points so that the baseline half contains
    // at least 5 samples.
    if len(values) < 10 {
        return 0, false
    }
    if k <= 0 {
        k = 0.5
    }
    if h <= 0 {
        h = 5
    }
    half := len(values) / 2
    mean := 0.0
    for i := 0; i < half; i++ {
        v := values[i]
        if math.IsNaN(v) || math.IsInf(v, 0) {
            return 0, false
        }
        mean += v
    }
    mean /= float64(half)
    varSum := 0.0
    for i := 0; i < half; i++ {
        d := values[i] - mean
        varSum += d * d
    }
    std := math.Sqrt(varSum / float64(half-1))
    // Guard against very small standard deviation which can explode the
    // standardized values.  The baseline mean may be zero, so we use its
    // magnitude in the guard.  This is a conservative lower bound that
    // keeps the detection stable while not modifying the algorithm's
    // core behaviour.
    guard := math.Max(0.05*math.Abs(mean), 1e-9)
    std = math.Max(std, guard)
    pos, neg := 0.0, 0.0
    for i, v := range values {
        if math.IsNaN(v) || math.IsInf(v, 0) {
            return 0, false
        }
        z := (v - mean) / std
        pos = math.Max(0, pos+z-k)
        neg = math.Min(0, neg+z+k)
        if pos > h || math.Abs(neg) > h {
            return i, true
        }
    }
    return 0, false
}
