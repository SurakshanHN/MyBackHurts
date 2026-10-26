package detector

import "math"

// FindShift runs a two-sided CUSUM over values and returns the first index
// where the cumulative deviation from the baseline exceeds h standard
// deviations. The baseline is the first half of the series.
// k is the allowance and h the threshold, both in baseline-std units.
// Defaults: k=0.5, h=10 (h=5 gave false alarms on clean noise).
func FindShift(values []float64, k, h float64) (index int, found bool) {
	// Need at least 5 baseline points, so at least 10 values in total.
	if len(values) < 10 {
		return 0, false
	}
	if k <= 0 {
		k = 0.5
	}
	if h <= 0 {
		h = 10
	}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
	}

	half := len(values) / 2
	mean := 0.0
	for i := 0; i < half; i++ {
		mean += values[i]
	}
	mean /= float64(half)

	varSum := 0.0
	for i := 0; i < half; i++ {
		d := values[i] - mean
		varSum += d * d
	}
	std := math.Sqrt(varSum / float64(half-1))

	// Floor the std so a near-constant baseline cannot turn tiny wobbles into
	// huge z-scores. The floor scales with the signal (5% of the baseline
	// level), so it does not depend on units (ms vs error rate).
	std = math.Max(std, math.Max(0.05*math.Abs(mean), 1e-9))

	pos, neg := 0.0, 0.0
	for i, v := range values {
		z := (v - mean) / std
		pos = math.Max(0, pos+z-k)
		neg = math.Min(0, neg+z+k)
		if pos > h || -neg > h {
			return i, true
		}
	}
	return 0, false
}
