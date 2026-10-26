// internal/detector/judge.go
//
// Judge evaluates whether a scale-up was Helpful, Harmful, Useless, or
// Unknown given pre- and post-scale observations.
//
// The logic is pure Go, stdlib only, and never mutates input slices.

package detector

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// floor constants for the "worse" check (5f).
const (
	p99FloorMs   = 1.0  // minimum denominator for P99Ms harm margin
	errRateFloor = 0.01 // minimum denominator for ErrRate harm margin
)

// Judge evaluates the given Input and returns a Result.
// It never panics, even on empty input.
func Judge(in Input) Result {
	var r Result

	// 5a. Data quality.
	r.DataQuality = assessDataQuality(in)

	// Split samples into pre (T < ScaleUpAt) and post-warmup
	// (T >= ScaleUpAt + WarmupSeconds).
	warmupDone := in.ScaleUpAt.Add(time.Duration(in.Cfg.WarmupSeconds) * time.Second)
	var pre, postWarmup []Sample
	for _, s := range in.Series {
		if s.T.Before(in.ScaleUpAt) {
			pre = append(pre, s)
		} else if !s.T.Before(warmupDone) {
			postWarmup = append(postWarmup, s)
		}
	}

	minS := in.Cfg.MinSamples
	if minS <= 0 {
		minS = 1
	}

	// Helper to return Unknown with a reason.
	unknown := func(reason string) Result {
		r.Verdict = Unknown
		r.Confidence = 0
		r.Reasons = append(r.Reasons, reason)
		return r
	}

	// 5b. Early returns for insufficient/invalid data.
	if len(pre) < minS {
		return unknown(fmt.Sprintf("fewer than %d pre-scale samples", minS))
	}
	if len(postWarmup) < minS {
		return unknown(fmt.Sprintf("fewer than %d post-warmup samples", minS))
	}

	// NaN / Inf / negative / out-of-range check.
	if hasInvalidValues(in) {
		return unknown("input contains NaN, Inf, negative, or out-of-range values")
	}

	// Warm-up check.
	warmupAlt := checkWarmup(in)
	if warmupAlt.Status == AltNotRuledOut {
		return unknown(fmt.Sprintf("warm-up not finished: %s", warmupAlt.Why))
	}

	// Fit P99Ms and ErrRate on pre samples.
	extractP99 := func(s Sample) float64 { return s.P99Ms }
	extractErr := func(s Sample) float64 { return s.ErrRate }

	fitP99, okP99 := FitMetric(in.Series, extractP99, in.ScaleUpAt)
	fitErr, okErr := FitMetric(in.Series, extractErr, in.ScaleUpAt)

	if !okP99 {
		return unknown("FitMetric failed for P99Ms (too few or degenerate pre samples)")
	}
	if !okErr {
		return unknown("FitMetric failed for ErrRate (too few or degenerate pre samples)")
	}

	// Mean pre RPS.
	meanPreRPS := mean(rpsValues(pre))
	if meanPreRPS <= 0 {
		return unknown("mean pre-scale RPS is <= 0")
	}

	// 5c. Counterfactual predictions.
	// For every post-warmup sample, predict the counterfactual and collect observed/expected/bounds.
	// Averaging per-sample bounds is conservative (the real interval of a mean is narrower).
	var obsP99, expP99, loP99, hiP99 []float64
	var obsErr, expErr, loErr, hiErr []float64
	for _, s := range postWarmup {
		mP, lP, uP := fitP99.Predict(s.T, 1.96)
		obsP99 = append(obsP99, s.P99Ms)
		expP99 = append(expP99, mP)
		loP99 = append(loP99, lP)
		hiP99 = append(hiP99, uP)

		mE, lE, uE := fitErr.Predict(s.T, 1.96)
		obsErr = append(obsErr, s.ErrRate)
		expErr = append(expErr, mE)
		loErr = append(loErr, lE)
		hiErr = append(hiErr, uE)
	}

	meanObsP99 := mean(obsP99)
	meanExpP99 := mean(expP99)
	meanLoP99 := mean(loP99)
	meanHiP99 := mean(hiP99)

	meanObsErr := mean(obsErr)
	meanExpErr := mean(expErr)
	meanLoErr := mean(loErr)
	meanHiErr := mean(hiErr)

	r.Findings = append(r.Findings, Finding{
		Name:     "p99_ms",
		Observed: meanObsP99,
		Expected: meanExpP99,
		Lower:    meanLoP99,
		Upper:    meanHiP99,
	})
	r.Findings = append(r.Findings, Finding{
		Name:     "err_rate",
		Observed: meanObsErr,
		Expected: meanExpErr,
		Lower:    meanLoErr,
		Upper:    meanHiErr,
	})

	// 5d. Throughput gain.
	n := len(postWarmup)
	if n > len(pre) {
		n = len(pre)
	}
	// Last n pre samples.
	lastNPre := pre[len(pre)-n:]
	meanPreRPSN := mean(rpsValues(lastNPre))
	meanPostRPS := mean(rpsValues(postWarmup))
	var throughputGain float64
	if meanPreRPSN > 0 {
		throughputGain = (meanPostRPS - meanPreRPSN) / meanPreRPSN
	}
	r.Findings = append(r.Findings, Finding{
		Name:     "throughput_gain",
		Observed: throughputGain,
	})

	// 5e. Dependency evidence.
	// FindShift uses the first half as baseline, so pre and post lengths should be similar;
	// this is a known limit.
	//
	// Collect all service P99Ms (pre + post-warmup) to find service shift time.
	var svcP99All []float64
	for _, s := range pre {
		svcP99All = append(svcP99All, s.P99Ms)
	}
	for _, s := range postWarmup {
		svcP99All = append(svcP99All, s.P99Ms)
	}
	// Map sample index to time for the combined pre+post series.
	svcTimes := make([]Sample, 0, len(pre)+len(postWarmup))
	svcTimes = append(svcTimes, pre...)
	svcTimes = append(svcTimes, postWarmup...)

	svcShiftIdx, svcShiftFound := FindShift(svcP99All, 0, 0)
	var svcShiftTime = in.ScaleUpAt // default: no constraint if not found

	// Build sorted dep names for deterministic processing.
	depNames := make([]string, 0, len(in.Deps))
	for name := range in.Deps {
		depNames = append(depNames, name)
	}
	sort.Strings(depNames)

	type depEvResult struct {
		name         string
		saturationEv bool
		latencyEv    bool
		shiftIdx     int
		maxSat       float64
	}
	depResults := make([]depEvResult, 0, len(depNames))

	for _, name := range depNames {
		samples := in.Deps[name]

		// Post-warmup dep samples for saturation check.
		var maxSat float64
		for _, ds := range samples {
			if !ds.T.Before(warmupDone) {
				sat := ds.Saturation * 100
				if sat > maxSat {
					maxSat = sat
				}
			}
		}
		satEv := maxSat >= in.Cfg.SaturationPct

		// LatencyEv: FindShift on all dep latency values (pre+post), shift time
		// must be >= ScaleUpAt AND <= service p99 shift time.
		var depLatAll []float64
		var depTimeAll []DepSample
		for _, ds := range samples {
			depLatAll = append(depLatAll, ds.LatencyMs)
			depTimeAll = append(depTimeAll, ds)
		}
		latEv := false
		latShiftIdx := 0
		if svcShiftFound {
			svcShiftT := svcTimes[svcShiftIdx].T
			svcShiftTime = svcShiftT
			depShiftIdx, depShiftFound := FindShift(depLatAll, 0, 0)
			if depShiftFound && depShiftIdx < len(depTimeAll) {
				depShiftT := depTimeAll[depShiftIdx].T
				if !depShiftT.Before(in.ScaleUpAt) && !depShiftT.After(svcShiftTime) {
					latEv = true
					latShiftIdx = depShiftIdx
				}
			}
		}

		depResults = append(depResults, depEvResult{
			name:         name,
			saturationEv: satEv,
			latencyEv:    latEv,
			shiftIdx:     latShiftIdx,
			maxSat:       maxSat,
		})
	}

	// Determine culprit.
	// Culprit = the dependency with latencyEv and earliest shift time;
	// if none, the one with highest maxSat among saturationEv; ties alphabetical.
	culprit := ""
	depEvidence := false
	for _, dr := range depResults {
		if dr.saturationEv || dr.latencyEv {
			depEvidence = true
			break
		}
	}
	_ = svcShiftTime // consumed above

	if depEvidence {
		// First try latencyEv with earliest shift time.
		bestLatIdx := math.MaxInt64
		for _, dr := range depResults {
			if dr.latencyEv && dr.shiftIdx < bestLatIdx {
				bestLatIdx = dr.shiftIdx
				culprit = dr.name
			} else if dr.latencyEv && dr.shiftIdx == bestLatIdx && dr.name < culprit {
				culprit = dr.name
			}
		}
		// If no latencyEv, use highest maxSat among saturationEv; ties alphabetical.
		if culprit == "" {
			var bestSat float64
			for _, dr := range depResults {
				if dr.saturationEv {
					if dr.maxSat > bestSat || (dr.maxSat == bestSat && (culprit == "" || dr.name < culprit)) {
						bestSat = dr.maxSat
						culprit = dr.name
					}
				}
			}
		}
	}

	// 5f. Metric worse.
	latencyWorse := meanObsP99 > meanHiP99+in.Cfg.HarmMargin*math.Max(math.Abs(meanExpP99), p99FloorMs)
	errWorse := meanObsErr > meanHiErr+in.Cfg.HarmMargin*math.Max(math.Abs(meanExpErr), errRateFloor)

	// 5g. Verdict rules (in order).
	var verdict Verdict
	switch {
	case (latencyWorse || errWorse) && throughputGain < in.Cfg.MinThroughputGain && depEvidence:
		verdict = Harmful
	case latencyWorse || errWorse:
		verdict = Unknown
		r.Reasons = append(r.Reasons, "metrics worse but no dependency evidence or throughput also rose")
	case throughputGain >= in.Cfg.MinThroughputGain:
		verdict = Helped
	default:
		verdict = Useless
	}

	r.Verdict = verdict

	// 5h. Downgrade: Harmful → Unknown if alternatives are not ruled out.
	// Always attach all three alternatives.
	altDegrading := checkTrafficAlreadyDegrading(in)
	altWarmup := checkWarmup(in)
	altOtherDeps := checkOtherDependencies(in, culprit)
	r.Alternatives = []Alternative{altDegrading, altWarmup, altOtherDeps}

	if verdict == Harmful {
		var downgrades []string
		if altDegrading.Status == AltNotRuledOut {
			downgrades = append(downgrades, fmt.Sprintf("traffic already degrading: %s", altDegrading.Why))
		}
		if altWarmup.Status == AltNotRuledOut {
			downgrades = append(downgrades, fmt.Sprintf("warmup not ruled out: %s", altWarmup.Why))
		}
		if altOtherDeps.Status == AltNotRuledOut {
			downgrades = append(downgrades, fmt.Sprintf("other dependency not ruled out: %s", altOtherDeps.Why))
		}
		if len(downgrades) > 0 {
			r.Verdict = Unknown
			for _, d := range downgrades {
				r.Reasons = append(r.Reasons, d)
			}
		}
	}

	if r.Verdict == Harmful {
		r.Culprit = culprit
	}

	// 5i. Confidence.
	nConf := len(pre)
	if len(postWarmup) < nConf {
		nConf = len(postWarmup)
	}
	missingFrac := math.Min(1, 0.1*float64(len(r.DataQuality)))

	var ci ConfidenceInput
	ci.N = nConf
	ci.MissingFraction = missingFrac

	switch r.Verdict {
	case Harmful:
		// Use the worse metric's numbers.
		var excess, halfWidth float64
		if latencyWorse && errWorse {
			excessP99 := meanObsP99 - meanHiP99
			hwP99 := (meanHiP99 - meanLoP99) / 2.0
			excessErr := meanObsErr - meanHiErr
			hwErr := (meanHiErr - meanLoErr) / 2.0
			// Pick the one with higher signal-to-noise.
			if hwP99 > 0 && excessP99/hwP99 >= excessErr/math.Max(hwErr, 1e-12) {
				excess, halfWidth = excessP99, hwP99
			} else {
				excess, halfWidth = excessErr, hwErr
			}
		} else if latencyWorse {
			excess = meanObsP99 - meanHiP99
			halfWidth = (meanHiP99 - meanLoP99) / 2.0
		} else {
			excess = meanObsErr - meanHiErr
			halfWidth = (meanHiErr - meanLoErr) / 2.0
		}
		ci.Excess = excess
		ci.HalfWidth = halfWidth
		// signals: {latencyWorse, errWorse, throughput low, saturationEv, latencyEv}
		signals := 0
		if latencyWorse {
			signals++
		}
		if errWorse {
			signals++
		}
		if throughputGain < in.Cfg.MinThroughputGain {
			signals++
		}
		anySatEv := false
		anyLatEv := false
		for _, dr := range depResults {
			if dr.saturationEv {
				anySatEv = true
			}
			if dr.latencyEv {
				anyLatEv = true
			}
		}
		if anySatEv {
			signals++
		}
		if anyLatEv {
			signals++
		}
		ci.Signals = signals
		ci.Total = 5

	case Helped, Useless:
		ci.Excess = math.Abs(throughputGain - in.Cfg.MinThroughputGain)
		ci.HalfWidth = math.Max(in.Cfg.MinThroughputGain, 0.05)
		signals := 0
		// throughput matches verdict
		if (r.Verdict == Helped && throughputGain >= in.Cfg.MinThroughputGain) ||
			(r.Verdict == Useless && throughputGain < in.Cfg.MinThroughputGain) {
			signals++
		}
		if !latencyWorse {
			signals++
		}
		if !errWorse {
			signals++
		}
		ci.Signals = signals
		ci.Total = 3

	default: // Unknown
		// Minimal confidence.
		ci.Excess = 0
		ci.HalfWidth = 1
		ci.Signals = 0
		ci.Total = 1
	}

	conf := Score(ci)

	if conf < in.Cfg.MinConfidence && r.Verdict != Unknown {
		r.Reasons = append(r.Reasons, fmt.Sprintf("confidence %.2f below minimum %.2f", conf, in.Cfg.MinConfidence))
		r.Verdict = Unknown
	}
	r.Confidence = conf

	// Build plain-English reasons if not already set.
	if len(r.Reasons) == 0 {
		r.Reasons = buildReasons(r.Verdict, meanObsP99, meanExpP99, meanLoP99, meanHiP99,
			meanObsErr, meanExpErr, meanLoErr, meanHiErr, throughputGain, culprit,
			in.Cfg.MinThroughputGain, latencyWorse, errWorse)
	}

	return r
}

// buildReasons constructs plain-English reason strings with numbers.
func buildReasons(v Verdict,
	obsP99, expP99, loP99, hiP99 float64,
	obsErr, expErr, loErr, hiErr float64,
	gain float64, culprit string,
	minGain float64, latWorse, errWorse bool,
) []string {
	var rs []string
	switch v {
	case Helped:
		rs = append(rs, fmt.Sprintf("throughput gain %.1f%% >= %.1f%% threshold",
			gain*100, minGain*100))
		rs = append(rs, fmt.Sprintf("P99Ms observed=%.1f within expected [%.1f, %.1f]",
			obsP99, loP99, hiP99))
		rs = append(rs, fmt.Sprintf("ErrRate observed=%.4f within expected [%.4f, %.4f]",
			obsErr, loErr, hiErr))
	case Useless:
		rs = append(rs, fmt.Sprintf("throughput gain %.1f%% < %.1f%% threshold",
			gain*100, minGain*100))
		rs = append(rs, fmt.Sprintf("P99Ms observed=%.1f within expected [%.1f, %.1f]",
			obsP99, loP99, hiP99))
	case Harmful:
		if latWorse {
			rs = append(rs, fmt.Sprintf("P99Ms observed=%.1f > upper bound %.1f (expected=%.1f)",
				obsP99, hiP99, expP99))
		}
		if errWorse {
			rs = append(rs, fmt.Sprintf("ErrRate observed=%.4f > upper bound %.4f (expected=%.4f)",
				obsErr, hiErr, expErr))
		}
		rs = append(rs, fmt.Sprintf("throughput gain %.1f%% < %.1f%% threshold",
			gain*100, minGain*100))
		if culprit != "" {
			rs = append(rs, fmt.Sprintf("culprit dependency: %s", culprit))
		}
	}
	return rs
}

// hasInvalidValues checks whether any sample or dep sample contains NaN, Inf,
// negative values, or out-of-range ErrRate/Saturation.
func hasInvalidValues(in Input) bool {
	for _, s := range in.Series {
		if isNanOrInf(s.RPS) || isNanOrInf(s.P99Ms) || isNanOrInf(s.ErrRate) {
			return true
		}
		if s.RPS < 0 || s.P99Ms < 0 || s.ErrRate < 0 {
			return true
		}
		if s.ErrRate > 1 {
			return true
		}
	}
	for _, depSamples := range in.Deps {
		for _, ds := range depSamples {
			if isNanOrInf(ds.Saturation) || isNanOrInf(ds.LatencyMs) {
				return true
			}
			if ds.Saturation < 0 || ds.LatencyMs < 0 {
				return true
			}
			if ds.Saturation > 1 {
				return true
			}
		}
	}
	return false
}

// rpsValues extracts RPS from a slice of samples.
func rpsValues(ss []Sample) []float64 {
	out := make([]float64, len(ss))
	for i, s := range ss {
		out[i] = s.RPS
	}
	return out
}

// mean returns the arithmetic mean of a slice. Returns 0 for empty slices.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
