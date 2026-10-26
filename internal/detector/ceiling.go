// internal/detector/ceiling.go
//
// Connection ceiling utilities.  Pure Go, stdlib only.

package detector

import (
	"math"
	"sort"
	"time"
)

// ConnectionCeiling returns the maximum number of pods supportable given
// a per-pod connection limit and a total maximum.
// Returns 0 (unknown) if max <= 0 or perPod <= 0.
func ConnectionCeiling(max, perPod int) int {
	if max <= 0 || perPod <= 0 {
		return 0
	}
	return max / perPod
}

// SafeCeiling estimates the replica count at which dependency saturation
// reaches thresholdPct percent, using a linear regression of Saturation on
// Replicas.
//
// Steps:
//  1. Pair each dep sample with the nearest-in-time series sample; skip if
//     the gap exceeds 2× the median inter-sample interval of the service series.
//  2. Regress Saturation on Replicas using OLS.
//  3. Require ≥3 distinct replica levels, slope > 0, R² ≥ 0.7, and finite
//     values; otherwise ok=false.
//  4. replicas = floor((thresholdPct/100 − intercept) / slope), capped to ≥1.
//  5. If replicas > 2× the maximum observed replica count in the paired data,
//     ok=false (refusing to extrapolate far).
func SafeCeiling(series []Sample, dep []DepSample, thresholdPct float64) (replicas int, ok bool) {
	if len(series) == 0 || len(dep) == 0 {
		return 0, false
	}

	// Compute median inter-sample interval of the service series.
	medInterval := medianInterval(series)
	maxGap := 2 * medInterval

	// Pair each dep sample with the nearest series sample.
	type pair struct {
		replicas   float64
		saturation float64
	}
	var pairs []pair
	maxObsReplicas := 0

	for _, ds := range dep {
		// Find the nearest series sample.
		nearest, gap := nearestSample(series, ds.T)
		if medInterval > 0 && gap > maxGap {
			continue // too far
		}
		if math.IsNaN(ds.Saturation) || math.IsInf(ds.Saturation, 0) {
			continue
		}
		if math.IsNaN(float64(nearest.Replicas)) {
			continue
		}
		pairs = append(pairs, pair{
			replicas:   float64(nearest.Replicas),
			saturation: ds.Saturation,
		})
		if nearest.Replicas > maxObsReplicas {
			maxObsReplicas = nearest.Replicas
		}
	}

	if len(pairs) < 3 {
		return 0, false
	}

	// Check distinct replica levels.
	replicaSet := make(map[float64]struct{})
	for _, p := range pairs {
		replicaSet[p.replicas] = struct{}{}
	}
	if len(replicaSet) < 3 {
		return 0, false
	}

	// Extract x (replicas) and y (saturation) slices.
	xs := make([]float64, len(pairs))
	ys := make([]float64, len(pairs))
	for i, p := range pairs {
		xs[i] = p.replicas
		ys[i] = p.saturation
	}

	// Verify all values are finite.
	for i := range xs {
		if math.IsNaN(xs[i]) || math.IsInf(xs[i], 0) ||
			math.IsNaN(ys[i]) || math.IsInf(ys[i], 0) {
			return 0, false
		}
	}

	slope, intercept, fitOK := fitLinear(xs, ys)
	if !fitOK {
		return 0, false
	}
	if slope <= 0 {
		return 0, false
	}

	// Compute R².
	r2 := computeR2(xs, ys, slope, intercept)
	if r2 < 0.7 {
		return 0, false
	}

	// Solve for the replica count at threshold.
	threshold := thresholdPct / 100.0
	replicasF := (threshold - intercept) / slope
	if math.IsNaN(replicasF) || math.IsInf(replicasF, 0) {
		return 0, false
	}
	replicasInt := int(math.Floor(replicasF))
	if replicasInt < 1 {
		replicasInt = 1
	}

	// Refuse to extrapolate far beyond observed data.
	if maxObsReplicas > 0 && replicasInt > 2*maxObsReplicas {
		return 0, false
	}

	return replicasInt, true
}

// medianInterval returns the median gap between successive samples.
func medianInterval(series []Sample) time.Duration {
	if len(series) < 2 {
		return 0
	}
	gaps := make([]float64, 0, len(series)-1)
	for i := 1; i < len(series); i++ {
		dt := series[i].T.Sub(series[i-1].T).Seconds()
		if dt > 0 {
			gaps = append(gaps, dt)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	m := median(gaps)
	return time.Duration(m * float64(time.Second))
}

// nearestSample returns the sample in series closest in time to t, and the
// absolute time gap.
func nearestSample(series []Sample, t time.Time) (Sample, time.Duration) {
	var best Sample
	bestGap := time.Duration(math.MaxInt64)
	for _, s := range series {
		gap := s.T.Sub(t)
		if gap < 0 {
			gap = -gap
		}
		if gap < bestGap {
			bestGap = gap
			best = s
		}
	}
	return best, bestGap
}

// computeR2 computes the coefficient of determination (R²) for the linear
// regression y = slope*x + intercept.
func computeR2(xs, ys []float64, slope, intercept float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	var meanY float64
	for _, y := range ys {
		meanY += y
	}
	meanY /= float64(n)

	var ssTot, ssRes float64
	for i := range xs {
		pred := slope*xs[i] + intercept
		d := ys[i] - pred
		ssRes += d * d
		d2 := ys[i] - meanY
		ssTot += d2 * d2
	}
	if ssTot == 0 {
		return 0
	}
	return 1 - ssRes/ssTot
}

// sortedKeys returns the sorted keys of a map.
func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
