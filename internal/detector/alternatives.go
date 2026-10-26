// internal/detector/alternatives.go
//
// Competing-explanation checks and data-quality assessment for the detector.
// All functions are pure: they never mutate input slices and never panic
// on empty input.

package detector

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// checkTrafficAlreadyDegrading inspects the pre-scale samples for evidence
// that the service was already degrading before the scale-up occurred.
// "Pre" samples are those with T < in.ScaleUpAt.
//
// It returns:
//   - not_ruled_out("too few pre-scale samples") if fewer than 10 pre samples.
//   - not_ruled_out("pre-scale P99Ms already shifting") if FindShift detects a
//     shift in the pre P99Ms values.
//   - not_ruled_out("pre-scale ErrRate already shifting") if FindShift detects a
//     shift in the pre ErrRate values.
//   - ruled_out otherwise.
func checkTrafficAlreadyDegrading(in Input) Alternative {
	const hyp = "traffic already degrading before scale-up"

	var preP99, preErr []float64
	for _, s := range in.Series {
		if s.T.Before(in.ScaleUpAt) {
			preP99 = append(preP99, s.P99Ms)
			preErr = append(preErr, s.ErrRate)
		}
	}

	if len(preP99) < 10 {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltNotRuledOut,
			Why:        "too few pre-scale samples",
		}
	}

	if _, found := FindShift(preP99, 0, 0); found {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltNotRuledOut,
			Why:        "pre-scale P99Ms already shifting",
		}
	}
	if _, found := FindShift(preErr, 0, 0); found {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltNotRuledOut,
			Why:        "pre-scale ErrRate already shifting",
		}
	}

	return Alternative{
		Hypothesis: hyp,
		Status:     AltRuledOut,
		Why:        "no shift detected in pre-scale P99Ms or ErrRate",
	}
}

// checkWarmup verifies that the post-scale-up warm-up period has concluded
// and that the deployment has reached the desired replica count.
//
// It returns ruled_out only if:
//   - at least one sample has T >= ScaleUpAt+WarmupSeconds, AND
//   - the latest sample has Ready >= ToReplicas.
//
// Otherwise it returns not_ruled_out with a descriptive reason.
func checkWarmup(in Input) Alternative {
	const hyp = "warm-up not yet finished"

	warmupDone := in.ScaleUpAt.Add(time.Duration(in.Cfg.WarmupSeconds) * time.Second)

	// Find whether any sample is past the warmup cutoff.
	var pastWarmup bool
	var latestReady int
	var latestT time.Time
	for _, s := range in.Series {
		if !s.T.Before(warmupDone) {
			pastWarmup = true
		}
		if s.T.After(latestT) {
			latestT = s.T
			latestReady = s.Ready
		}
	}

	if !pastWarmup {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltNotRuledOut,
			Why:        "no samples yet past the warmup window",
		}
	}
	if latestReady < in.ToReplicas {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltNotRuledOut,
			Why: fmt.Sprintf("latest Ready=%d < ToReplicas=%d",
				latestReady, in.ToReplicas),
		}
	}
	return Alternative{
		Hypothesis: hyp,
		Status:     AltRuledOut,
		Why:        "warmup finished and replicas at target",
	}
}

// checkOtherDependencies looks for evidence that a dependency OTHER than
// culprit is responsible for the observed degradation.
//
// For each dependency (except culprit), using samples with T >= ScaleUpAt, it
// checks:
//   - max Saturation*100 >= SaturationPct → not_ruled_out
//   - FindShift finds a shift in LatencyMs → not_ruled_out
//   - no post-scale samples at all → not_ruled_out
//
// If no dependencies are declared (or all are the culprit), returns ruled_out
// ("none declared"). If all other dependencies are ruled out, returns ruled_out.
// Otherwise returns not_ruled_out naming the dependency in Why.
func checkOtherDependencies(in Input, culprit string) Alternative {
	const hyp = "another dependency caused the degradation"

	// Collect sorted dep names (deterministic order).
	depNames := make([]string, 0, len(in.Deps))
	for name := range in.Deps {
		if name != culprit {
			depNames = append(depNames, name)
		}
	}
	if len(depNames) == 0 {
		return Alternative{
			Hypothesis: hyp,
			Status:     AltRuledOut,
			Why:        "none declared",
		}
	}
	sort.Strings(depNames)

	for _, name := range depNames {
		samples := in.Deps[name]

		// Collect post samples.
		var postLatency []float64
		var maxSat float64
		var hasPost bool
		for _, ds := range samples {
			if !ds.T.Before(in.ScaleUpAt) {
				hasPost = true
				postLatency = append(postLatency, ds.LatencyMs)
				sat := ds.Saturation * 100
				if sat > maxSat {
					maxSat = sat
				}
			}
		}

		if !hasPost {
			return Alternative{
				Hypothesis: hyp,
				Status:     AltNotRuledOut,
				Why:        fmt.Sprintf("dependency %q has no post-scale samples", name),
			}
		}

		if maxSat >= in.Cfg.SaturationPct {
			return Alternative{
				Hypothesis: hyp,
				Status:     AltNotRuledOut,
				Why: fmt.Sprintf("dependency %q max saturation %.1f%% >= threshold %.1f%%",
					name, maxSat, in.Cfg.SaturationPct),
			}
		}

		// Collect all latency values (pre+post) for FindShift.
		var allLatency []float64
		for _, ds := range samples {
			allLatency = append(allLatency, ds.LatencyMs)
		}
		if _, found := FindShift(allLatency, 0, 0); found {
			return Alternative{
				Hypothesis: hyp,
				Status:     AltNotRuledOut,
				Why:        fmt.Sprintf("dependency %q LatencyMs shows a shift", name),
			}
		}
	}

	return Alternative{
		Hypothesis: hyp,
		Status:     AltRuledOut,
		Why:        "no other dependency shows saturation or latency shift",
	}
}

// assessDataQuality returns a slice of short problem descriptions.
// One message per distinct problem detected in in.
// An empty slice means the data look clean.
func assessDataQuality(in Input) []string {
	var problems []string

	// Split samples into pre and post-warmup.
	warmupDone := in.ScaleUpAt.Add(time.Duration(in.Cfg.WarmupSeconds) * time.Second)
	var pre, postWarmup []Sample
	for _, s := range in.Series {
		if s.T.Before(in.ScaleUpAt) {
			pre = append(pre, s)
		} else if !s.T.Before(warmupDone) {
			postWarmup = append(postWarmup, s)
		}
	}

	min := in.Cfg.MinSamples
	if min <= 0 {
		min = 1
	}
	if len(pre) < min {
		problems = append(problems,
			fmt.Sprintf("fewer than %d pre-scale samples (have %d)", min, len(pre)))
	}
	if len(postWarmup) < min {
		problems = append(problems,
			fmt.Sprintf("fewer than %d post-warmup samples (have %d)", min, len(postWarmup)))
	}

	// Check for NaN/Inf anywhere.
	nanInf := false
	for _, s := range in.Series {
		if isNanOrInf(s.RPS) || isNanOrInf(s.P99Ms) || isNanOrInf(s.ErrRate) {
			nanInf = true
			break
		}
	}
	if !nanInf {
		for _, depSamples := range in.Deps {
			for _, ds := range depSamples {
				if isNanOrInf(ds.Saturation) || isNanOrInf(ds.LatencyMs) {
					nanInf = true
					break
				}
			}
			if nanInf {
				break
			}
		}
	}
	if nanInf {
		problems = append(problems, "NaN or Inf values present")
	}

	// Check for negative values.
	negative := false
	for _, s := range in.Series {
		if s.RPS < 0 || s.P99Ms < 0 || s.ErrRate < 0 {
			negative = true
			break
		}
	}
	if !negative {
		for _, depSamples := range in.Deps {
			for _, ds := range depSamples {
				if ds.Saturation < 0 || ds.LatencyMs < 0 {
					negative = true
					break
				}
			}
			if negative {
				break
			}
		}
	}
	if negative {
		problems = append(problems, "negative values present")
	}

	// Check ErrRate outside [0,1].
	errOutOfRange := false
	for _, s := range in.Series {
		if s.ErrRate < 0 || s.ErrRate > 1 {
			errOutOfRange = true
			break
		}
	}
	if errOutOfRange {
		problems = append(problems, "ErrRate outside [0,1]")
	}

	// Check Saturation outside [0,1].
	satOutOfRange := false
	for _, depSamples := range in.Deps {
		for _, ds := range depSamples {
			if ds.Saturation < 0 || ds.Saturation > 1 {
				satOutOfRange = true
				break
			}
		}
		if satOutOfRange {
			break
		}
	}
	if satOutOfRange {
		problems = append(problems, "Saturation outside [0,1]")
	}

	// Check timestamps not increasing and gap > 3x median interval.
	if len(in.Series) >= 2 {
		notIncreasing := false
		intervals := make([]float64, 0, len(in.Series)-1)
		for i := 1; i < len(in.Series); i++ {
			dt := in.Series[i].T.Sub(in.Series[i-1].T).Seconds()
			if dt <= 0 {
				notIncreasing = true
			} else {
				intervals = append(intervals, dt)
			}
		}
		if notIncreasing {
			problems = append(problems, "timestamps not strictly increasing")
		}
		if len(intervals) > 0 {
			med := median(intervals)
			if med > 0 {
				for _, iv := range intervals {
					if iv > 3*med {
						problems = append(problems, "gap larger than 3x the median sample interval")
						break
					}
				}
			}
		}
	}

	return problems
}

// isNanOrInf returns true if v is NaN or infinite.
func isNanOrInf(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

// median returns the median of a slice (which is sorted in place on a copy).
func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 0 {
		return (cp[n/2-1] + cp[n/2]) / 2.0
	}
	return cp[n/2]
}
