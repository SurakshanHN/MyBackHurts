// internal/detector/alternatives_test.go
//
// Tests for alternatives.go (checkTrafficAlreadyDegrading, checkWarmup,
// checkOtherDependencies, assessDataQuality).
//
// All test data comes from deterministic seeded generators (prefix jd)
// or simple arithmetic. No hand-typed verdict-shaped constants.

package detector

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// jd helpers
// ---------------------------------------------------------------------------

// jdBase is a fixed base time used across tests for reproducibility.
var jdBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// jdSeries builds n samples spaced intervalSec seconds apart starting at
// start. P99Ms, ErrRate and RPS come from the supplied function of the
// sample index.
func jdSeries(start time.Time, n int, intervalSec float64,
	fn func(i int) (rps, p99, errRate float64),
) []Sample {
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		rps, p99, errRate := fn(i)
		out[i] = Sample{
			T:        start.Add(time.Duration(float64(i) * intervalSec * float64(time.Second))),
			Replicas: 3, Ready: 3,
			RPS: rps, P99Ms: p99, ErrRate: errRate,
		}
	}
	return out
}

// jdDepSamples builds n dep samples spaced intervalSec seconds apart.
func jdDepSamples(start time.Time, n int, intervalSec float64,
	fn func(i int) (sat, latMs float64),
) []DepSample {
	out := make([]DepSample, n)
	for i := 0; i < n; i++ {
		sat, latMs := fn(i)
		out[i] = DepSample{
			T:          start.Add(time.Duration(float64(i) * intervalSec * float64(time.Second))),
			Saturation: sat,
			LatencyMs:  latMs,
		}
	}
	return out
}

// jdNoise returns a seeded noise value at index i with given mean and std.
func jdNoise(seed int64, mean, std float64) func(i int) float64 {
	r := rand.New(rand.NewSource(seed))
	return func(i int) float64 {
		return mean + std*r.NormFloat64()
	}
}

// jdDefaultCfg returns a sensible default Config.
func jdDefaultCfg() Config {
	return Config{
		MinSamples:        5,
		HarmMargin:        0.1,
		SaturationPct:     90.0,
		MinConfidence:     0.3,
		WarmupSeconds:     60,
		MinThroughputGain: 0.05,
	}
}

// jdInput returns an Input with the given pre and post Series slices.
// It splices in gap-filling samples (at 10s intervals) between the end of
// pre and the start of post so that the gap check in assessDataQuality does
// not fire for tests that are not specifically testing the gap condition.
func jdInput(pre, post []Sample, deps map[string][]DepSample) Input {
	scaleUpAt := jdBase.Add(300 * time.Second)

	// Build a filler that covers the warmup window at 10s intervals so there
	// are no large gaps in in.Series.
	warmupDone := scaleUpAt.Add(60 * time.Second)
	var filler []Sample
	var lastT time.Time
	if len(pre) > 0 {
		lastT = pre[len(pre)-1].T
	} else {
		lastT = jdBase
	}
	for ts := lastT.Add(10 * time.Second); !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		filler = append(filler, Sample{T: ts, Replicas: 4, Ready: 4, RPS: 100, P99Ms: 200, ErrRate: 0.01})
	}

	all := append(append(append([]Sample{}, pre...), filler...), post...)
	return Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2,
		ToReplicas:   4,
		Series:       all,
		Deps:         deps,
		Cfg:          jdDefaultCfg(),
	}
}

// ---------------------------------------------------------------------------
// checkTrafficAlreadyDegrading
// ---------------------------------------------------------------------------

func TestCheckTrafficAlreadyDegrading_TooFewPre(t *testing.T) {
	// Only 5 pre samples → not_ruled_out
	scaleUpAt := jdBase.Add(300 * time.Second)
	pre := jdSeries(jdBase, 5, 10, func(i int) (float64, float64, float64) {
		return 100, 200, 0.01
	})
	in := Input{ScaleUpAt: scaleUpAt, Series: pre, Cfg: jdDefaultCfg()}
	alt := checkTrafficAlreadyDegrading(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
	if alt.Why != "too few pre-scale samples" {
		t.Fatalf("unexpected why: %q", alt.Why)
	}
}

func TestCheckTrafficAlreadyDegrading_P99Shifting(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(42, 0, 1)
	// 20 pre samples: flat then big step up at index 10
	pre := jdSeries(jdBase, 20, 10, func(i int) (float64, float64, float64) {
		p99 := 200.0 + noise(i)
		if i >= 10 {
			p99 += 50 // step up
		}
		return 100, p99, 0.01
	})
	in := Input{ScaleUpAt: scaleUpAt, Series: pre, Cfg: jdDefaultCfg()}
	alt := checkTrafficAlreadyDegrading(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
	if alt.Why != "pre-scale P99Ms already shifting" {
		t.Fatalf("unexpected why: %q", alt.Why)
	}
}

func TestCheckTrafficAlreadyDegrading_ErrRateShifting(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(43, 0, 0.001)
	pre := jdSeries(jdBase, 20, 10, func(i int) (float64, float64, float64) {
		errRate := 0.01 + noise(i)
		if i >= 10 {
			errRate += 0.1 // step up in error rate
		}
		return 100, 200, errRate
	})
	in := Input{ScaleUpAt: scaleUpAt, Series: pre, Cfg: jdDefaultCfg()}
	alt := checkTrafficAlreadyDegrading(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
	if alt.Why != "pre-scale ErrRate already shifting" {
		t.Fatalf("unexpected why: %q", alt.Why)
	}
}

func TestCheckTrafficAlreadyDegrading_RuledOut(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(44, 0, 1)
	// 20 pre samples: flat noisy, no step
	pre := jdSeries(jdBase, 20, 10, func(i int) (float64, float64, float64) {
		return 100, 200 + noise(i), 0.01
	})
	in := Input{ScaleUpAt: scaleUpAt, Series: pre, Cfg: jdDefaultCfg()}
	alt := checkTrafficAlreadyDegrading(in)
	if alt.Status != AltRuledOut {
		t.Fatalf("expected ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
}

func TestCheckTrafficAlreadyDegrading_Determinism(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(44, 0, 1)
	pre := jdSeries(jdBase, 20, 10, func(i int) (float64, float64, float64) {
		return 100, 200 + noise(i), 0.01
	})
	in := Input{ScaleUpAt: scaleUpAt, Series: pre, Cfg: jdDefaultCfg()}
	first := checkTrafficAlreadyDegrading(in)
	for i := 0; i < 10; i++ {
		got := checkTrafficAlreadyDegrading(in)
		if got != first {
			t.Fatalf("non-deterministic result at iteration %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// checkWarmup
// ---------------------------------------------------------------------------

func TestCheckWarmup_NoPastWarmup(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	// All samples before scaleUpAt+60s
	pre := jdSeries(jdBase, 10, 10, func(i int) (float64, float64, float64) {
		return 100, 200, 0.01
	})
	// A few post samples but all within warmup window
	postStart := scaleUpAt.Add(10 * time.Second)
	post := jdSeries(postStart, 3, 10, func(i int) (float64, float64, float64) {
		return 100, 200, 0.01
	})
	for j := range post {
		post[j].Ready = 4
	}
	all := append(pre, post...)
	in := Input{
		ScaleUpAt: scaleUpAt, ToReplicas: 4,
		Series: all, Cfg: jdDefaultCfg(),
	}
	alt := checkWarmup(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
}

func TestCheckWarmup_ReadyBelowTarget(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	warmupDone := scaleUpAt.Add(60 * time.Second)
	// One sample past warmup but Ready only 2
	s := Sample{T: warmupDone.Add(10 * time.Second), Ready: 2, Replicas: 4}
	in := Input{
		ScaleUpAt: scaleUpAt, ToReplicas: 4,
		Series: []Sample{s}, Cfg: jdDefaultCfg(),
	}
	alt := checkWarmup(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
}

func TestCheckWarmup_RuledOut(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	warmupDone := scaleUpAt.Add(60 * time.Second)
	s := Sample{T: warmupDone.Add(10 * time.Second), Ready: 4, Replicas: 4}
	in := Input{
		ScaleUpAt: scaleUpAt, ToReplicas: 4,
		Series: []Sample{s}, Cfg: jdDefaultCfg(),
	}
	alt := checkWarmup(in)
	if alt.Status != AltRuledOut {
		t.Fatalf("expected ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
}

func TestCheckWarmup_EmptyInput(t *testing.T) {
	in := Input{ScaleUpAt: jdBase, ToReplicas: 4, Cfg: jdDefaultCfg()}
	alt := checkWarmup(in)
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out for empty input, got %q", alt.Status)
	}
}

// ---------------------------------------------------------------------------
// checkOtherDependencies
// ---------------------------------------------------------------------------

func TestCheckOtherDependencies_NoneDeclaed(t *testing.T) {
	in := Input{Cfg: jdDefaultCfg()}
	alt := checkOtherDependencies(in, "")
	if alt.Status != AltRuledOut {
		t.Fatalf("expected ruled_out for no deps, got %q", alt.Status)
	}
	if alt.Why != "none declared" {
		t.Fatalf("unexpected why: %q", alt.Why)
	}
}

func TestCheckOtherDependencies_AllAreCulprit(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	postStart := scaleUpAt.Add(10 * time.Second)
	deps := map[string][]DepSample{
		"db": jdDepSamples(postStart, 5, 10, func(i int) (float64, float64) {
			return 0.5, 100
		}),
	}
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: jdDefaultCfg()}
	alt := checkOtherDependencies(in, "db") // "db" is the culprit, skip it
	if alt.Status != AltRuledOut {
		t.Fatalf("expected ruled_out when all deps are culprit, got %q", alt.Status)
	}
}

func TestCheckOtherDependencies_NoPostSamples(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	// dep samples all before scaleUpAt
	preStart := jdBase
	deps := map[string][]DepSample{
		"cache": jdDepSamples(preStart, 5, 10, func(i int) (float64, float64) {
			return 0.5, 100
		}),
	}
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: jdDefaultCfg()}
	alt := checkOtherDependencies(in, "")
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out for no post samples, got %q", alt.Status)
	}
}

func TestCheckOtherDependencies_HighSaturation(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	postStart := scaleUpAt.Add(10 * time.Second)
	deps := map[string][]DepSample{
		"redis": jdDepSamples(postStart, 5, 10, func(i int) (float64, float64) {
			return 0.95, 100 // 95% saturation > 90% threshold
		}),
	}
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: jdDefaultCfg()}
	alt := checkOtherDependencies(in, "")
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out for high saturation, got %q", alt.Status)
	}
}

func TestCheckOtherDependencies_LatencyShift(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	// Build 20 dep samples: 10 pre (flat) then 10 post (high latency)
	allDep := make([]DepSample, 0, 20)
	for i := 0; i < 20; i++ {
		lat := 100.0
		if i >= 10 {
			lat = 200.0 // step up
		}
		allDep = append(allDep, DepSample{
			T:          jdBase.Add(time.Duration(i*30) * time.Second),
			Saturation: 0.5,
			LatencyMs:  lat,
		})
	}
	deps := map[string][]DepSample{"pg": allDep}
	cfg := jdDefaultCfg()
	cfg.SaturationPct = 90
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: cfg}
	alt := checkOtherDependencies(in, "")
	if alt.Status != AltNotRuledOut {
		t.Fatalf("expected not_ruled_out for latency shift, got %q", alt.Status)
	}
}

func TestCheckOtherDependencies_RuledOut(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(99, 0, 1)
	// 20 samples: 10 pre + 10 post, flat latency, low saturation
	allDep := make([]DepSample, 0, 20)
	for i := 0; i < 20; i++ {
		allDep = append(allDep, DepSample{
			T:          jdBase.Add(time.Duration(i*30) * time.Second),
			Saturation: 0.5,
			LatencyMs:  100 + noise(i),
		})
	}
	deps := map[string][]DepSample{"pg": allDep}
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: jdDefaultCfg()}
	alt := checkOtherDependencies(in, "")
	if alt.Status != AltRuledOut {
		t.Fatalf("expected ruled_out, got %q why=%q", alt.Status, alt.Why)
	}
}

func TestCheckOtherDependencies_Determinism(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	noise := jdNoise(99, 0, 1)
	allDep := make([]DepSample, 0, 20)
	for i := 0; i < 20; i++ {
		allDep = append(allDep, DepSample{
			T:          jdBase.Add(time.Duration(i*30) * time.Second),
			Saturation: 0.5,
			LatencyMs:  100 + noise(i),
		})
	}
	deps := map[string][]DepSample{"pg": allDep}
	in := Input{ScaleUpAt: scaleUpAt, Deps: deps, Cfg: jdDefaultCfg()}
	first := checkOtherDependencies(in, "")
	for i := 0; i < 10; i++ {
		got := checkOtherDependencies(in, "")
		if got != first {
			t.Fatalf("non-deterministic at %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// assessDataQuality
// ---------------------------------------------------------------------------

func TestAssessDataQuality_Clean(t *testing.T) {
	// Build a dense series spanning pre → warmup → post with consistent 10s intervals,
	// so no gap check fires and we have enough pre and post-warmup samples.
	scaleUpAt := jdBase.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(60 * time.Second)

	cfg := jdDefaultCfg()
	cfg.WarmupSeconds = 60

	// 20 pre samples ending at scaleUpAt - 10s (so last is at jdBase+190s, gap to
	// warmupDone+10s = jdBase+270s is 80s at 10s intervals → ≤8x; but to be safe we
	// include warmup-period samples as well, which are neither pre nor post-warmup and
	// therefore don't affect the gap detection (which only uses in.Series).
	// Strategy: fill every 10s from jdBase to jdBase+400s; pre is <200, post-warmup is >=260.
	var all []Sample
	for i := 0; i <= 40; i++ {
		all = append(all, Sample{
			T:        jdBase.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: 100, P99Ms: 200, ErrRate: 0.01,
		})
	}
	in := Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2, ToReplicas: 4,
		Series: all,
		Cfg:    cfg,
	}
	// Count pre and post-warmup to ensure test validity.
	var preCnt, postCnt int
	for _, s := range all {
		if s.T.Before(scaleUpAt) {
			preCnt++
		} else if !s.T.Before(warmupDone) {
			postCnt++
		}
	}
	if preCnt < cfg.MinSamples || postCnt < cfg.MinSamples {
		t.Fatalf("test setup error: pre=%d post=%d", preCnt, postCnt)
	}
	problems := assessDataQuality(in)
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func TestAssessDataQuality_TooFewPre(t *testing.T) {
	// Use a dense series where only 2 samples are before scaleUpAt.
	// scaleUpAt at jdBase+20s, so samples at 0s and 10s are pre (2 total).
	// Post-warmup: warmupDone = jdBase+80s; build 10 samples from jdBase+90s.
	scaleUpAt := jdBase.Add(20 * time.Second)
	cfg := jdDefaultCfg()
	cfg.WarmupSeconds = 60
	// Build a fully dense series at 10s intervals from 0 to 200s.
	var all []Sample
	for i := 0; i <= 20; i++ {
		all = append(all, Sample{
			T:        jdBase.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: 100, P99Ms: 200, ErrRate: 0.01,
		})
	}
	in := Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2, ToReplicas: 4,
		Series: all,
		Cfg:    cfg,
	}
	// Verify: count actual pre samples
	var preCnt int
	for _, s := range all {
		if s.T.Before(scaleUpAt) {
			preCnt++
		}
	}
	problems := assessDataQuality(in)
	want := fmt.Sprintf("fewer than %d pre-scale samples (have %d)", cfg.MinSamples, preCnt)
	found := false
	for _, p := range problems {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %q, got %v", want, problems)
	}
}

func TestAssessDataQuality_TooFewPost(t *testing.T) {
	// Use a layout where only 1 sample falls in the post-warmup window.
	// scaleUpAt at jdBase+200s, warmupDone = jdBase+260s.
	// Dense pre samples from 0..190s (20 samples). One post-warmup at 270s.
	scaleUpAt := jdBase.Add(200 * time.Second)
	cfg := jdDefaultCfg()
	cfg.WarmupSeconds = 60
	var all []Sample
	// Dense from 0 to 200s (pre)
	for i := 0; i <= 20; i++ {
		all = append(all, Sample{
			T:        jdBase.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: 100, P99Ms: 200, ErrRate: 0.01,
		})
	}
	// Samples covering the warmup window (210s to 260s) so no gap fires
	for i := 21; i <= 26; i++ {
		all = append(all, Sample{
			T:        jdBase.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: 100, P99Ms: 200, ErrRate: 0.01,
		})
	}
	// Only 1 post-warmup sample at 270s
	all = append(all, Sample{
		T:        jdBase.Add(270 * time.Second),
		Replicas: 4, Ready: 4,
		RPS: 100, P99Ms: 200, ErrRate: 0.01,
	})
	in := Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2, ToReplicas: 4,
		Series: all,
		Cfg:    cfg,
	}
	var postCnt int
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)
	for _, s := range all {
		if !s.T.Before(warmupDone) {
			postCnt++
		}
	}
	want := fmt.Sprintf("fewer than %d post-warmup samples (have %d)", cfg.MinSamples, postCnt)
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %q, got %v", want, problems)
	}
}

func TestAssessDataQuality_NaN(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	s := Sample{T: jdBase, P99Ms: math.NaN()}
	in := Input{ScaleUpAt: scaleUpAt, Series: []Sample{s}, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "NaN or Inf values present" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected NaN message, got %v", problems)
	}
}

func TestAssessDataQuality_Inf(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	s := Sample{T: jdBase, RPS: math.Inf(1)}
	in := Input{ScaleUpAt: scaleUpAt, Series: []Sample{s}, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "NaN or Inf values present" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Inf message, got %v", problems)
	}
}

func TestAssessDataQuality_Negative(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	s := Sample{T: jdBase, RPS: -1}
	in := Input{ScaleUpAt: scaleUpAt, Series: []Sample{s}, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "negative values present" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected negative values message, got %v", problems)
	}
}

func TestAssessDataQuality_ErrRateOutOfRange(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	s := Sample{T: jdBase, ErrRate: 1.5}
	in := Input{ScaleUpAt: scaleUpAt, Series: []Sample{s}, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "ErrRate outside [0,1]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ErrRate message, got %v", problems)
	}
}

func TestAssessDataQuality_SaturationOutOfRange(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	dep := []DepSample{{T: jdBase, Saturation: 1.5, LatencyMs: 100}}
	in := Input{
		ScaleUpAt: scaleUpAt,
		Deps:      map[string][]DepSample{"x": dep},
		Cfg:       jdDefaultCfg(),
	}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "Saturation outside [0,1]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Saturation message, got %v", problems)
	}
}

func TestAssessDataQuality_TimestampsNotIncreasing(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	s1 := Sample{T: jdBase.Add(20 * time.Second)}
	s2 := Sample{T: jdBase.Add(10 * time.Second)} // going backwards
	in := Input{ScaleUpAt: scaleUpAt, Series: []Sample{s1, s2}, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "timestamps not strictly increasing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected timestamp message, got %v", problems)
	}
}

func TestAssessDataQuality_Gap(t *testing.T) {
	scaleUpAt := jdBase.Add(300 * time.Second)
	// Normal intervals 10s, then a 100s gap (>3x)
	ss := []Sample{
		{T: jdBase.Add(0 * time.Second)},
		{T: jdBase.Add(10 * time.Second)},
		{T: jdBase.Add(20 * time.Second)},
		{T: jdBase.Add(120 * time.Second)}, // 100s gap
	}
	in := Input{ScaleUpAt: scaleUpAt, Series: ss, Cfg: jdDefaultCfg()}
	problems := assessDataQuality(in)
	found := false
	for _, p := range problems {
		if p == "gap larger than 3x the median sample interval" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gap message, got %v", problems)
	}
}

func TestAssessDataQuality_Determinism(t *testing.T) {
	scaleUpAt := jdBase.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(60 * time.Second)
	cfg := jdDefaultCfg()
	cfg.WarmupSeconds = 60
	var all []Sample
	for i := 0; i <= 40; i++ {
		all = append(all, Sample{
			T:        jdBase.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: 100, P99Ms: 200, ErrRate: 0.01,
		})
	}
	_ = warmupDone
	in := Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2, ToReplicas: 4,
		Series: all,
		Cfg:    cfg,
	}
	first := assessDataQuality(in)
	for i := 0; i < 20; i++ {
		got := assessDataQuality(in)
		if len(got) != len(first) {
			t.Fatalf("non-deterministic length at %d: %v vs %v", i, got, first)
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("non-deterministic message[%d] at %d", j, i)
			}
		}
	}
}
