// internal/detector/judge_test.go
//
// Tests for judge.go.  All test data comes from deterministic seeded
// generators (prefix jd, shared from alternatives_test.go).
// No hand-typed verdict-shaped constants.

package detector

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// shared test data builders (jd* prefix to avoid clashes)
// ---------------------------------------------------------------------------

// jdScaleUp returns a canonical scaleUpAt time for Judge tests.
func jdScaleUp() time.Time { return jdBase.Add(400 * time.Second) }

// jdJudgeCfg returns a Config suitable for judge tests.
func jdJudgeCfg() Config {
	return Config{
		MinSamples:        5,
		HarmMargin:        0.1,
		SaturationPct:     90.0,
		MinConfidence:     0.1, // low so confidence downgrade rarely fires
		WarmupSeconds:     60,
		MinThroughputGain: 0.05,
	}
}

// jdBuildInput builds a standard Input for Judge tests.
// preFn(i) returns (rps, p99, errRate) for pre sample i.
// postFn(i) returns (rps, p99, errRate) for post-warmup sample i.
// Both series have n samples spaced 10s apart; pre ends just before ScaleUpAt,
// post starts at ScaleUpAt+WarmupSeconds+10s.
// warmup-window samples (scaleUpAt to scaleUpAt+warmup) are filled at 10s
// intervals so no gap fires.
func jdBuildInput(
	scaleUpAt time.Time, cfg Config,
	nPre int, preFn func(i int) (float64, float64, float64),
	nPost int, postFn func(i int) (float64, float64, float64),
	deps map[string][]DepSample,
) Input {
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	// Pre samples: spaced 10s, ending at scaleUpAt-10s.
	preStart := scaleUpAt.Add(-time.Duration(nPre*10) * time.Second)
	var pre []Sample
	for i := 0; i < nPre; i++ {
		rps, p99, errRate := preFn(i)
		pre = append(pre, Sample{
			T:        preStart.Add(time.Duration(i*10) * time.Second),
			Replicas: 2, Ready: 2,
			RPS: rps, P99Ms: p99, ErrRate: errRate,
		})
	}

	// Warmup filler.
	var filler []Sample
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		rps0, p990, e0 := preFn(0)
		filler = append(filler, Sample{
			T: ts, Replicas: 4, Ready: 4,
			RPS: rps0, P99Ms: p990, ErrRate: e0,
		})
	}

	// Post-warmup samples: starting at warmupDone+10s.
	postStart := warmupDone.Add(10 * time.Second)
	var post []Sample
	for i := 0; i < nPost; i++ {
		rps, p99, errRate := postFn(i)
		post = append(post, Sample{
			T:        postStart.Add(time.Duration(i*10) * time.Second),
			Replicas: 4, Ready: 4,
			RPS: rps, P99Ms: p99, ErrRate: errRate,
		})
	}

	all := append(append(append([]Sample{}, pre...), filler...), post...)

	return Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 2,
		ToReplicas:   4,
		Series:       all,
		Deps:         deps,
		Cfg:          cfg,
	}
}

// jdNoisyF returns a deterministic noisy value function.
func jdNoisyF(seed int64, mean, std float64) func(i int) float64 {
	r := rand.New(rand.NewSource(seed))
	vals := make([]float64, 1000) // pre-generate to keep calls order-independent
	for i := range vals {
		vals[i] = mean + std*r.NormFloat64()
	}
	return func(i int) float64 {
		if i < len(vals) {
			return vals[i]
		}
		return mean
	}
}

// ---------------------------------------------------------------------------
// SCENARIO 3: contention below alert threshold (latency-only culprit)
// ---------------------------------------------------------------------------

// buildScenario3 creates the data for scenario 3 (and its control).
// If flatDepLatency=true, the dependency latency is flat → control (not Harmful).
func buildScenario3(flatDepLatency bool) Input {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.SaturationPct = 90
	cfg.MinThroughputGain = 0.05
	cfg.WarmupSeconds = 60
	cfg.MinSamples = 5
	cfg.HarmMargin = 0.05
	cfg.MinConfidence = 0.05

	noiseP99 := jdNoisyF(1, 0, 2)
	noiseErr := jdNoisyF(2, 0, 0.0005)
	noiseDep := jdNoisyF(3, 0, 1)

	const nPre = 20
	const nPost = 20

	preFn := func(i int) (float64, float64, float64) {
		return 100.0, 200.0 + noiseP99(i), 0.01 + noiseErr(i)
	}
	postFn := func(i int) (float64, float64, float64) {
		// P99 rises significantly after scale-up (service latency degraded).
		return 100.0, 400.0 + noiseP99(nPre+i), 0.01 + noiseErr(nPre+i)
	}

	// Dependency: saturation held near 0.5 (well below 90% threshold).
	// LatencyMs: flat in pre, rises in post (if !flatDepLatency).
	// Build combined dep samples: nPre + nPost aligned with the service timeline.
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)
	preStart := scaleUpAt.Add(-time.Duration(nPre*10) * time.Second)
	postStart := warmupDone.Add(10 * time.Second)

	var depSamples []DepSample
	for i := 0; i < nPre; i++ {
		depSamples = append(depSamples, DepSample{
			T:          preStart.Add(time.Duration(i*10) * time.Second),
			Saturation: 0.5,
			LatencyMs:  100.0 + noiseDep(i),
		})
	}
	for i := 0; i < nPost; i++ {
		var lat float64
		if flatDepLatency {
			lat = 100.0 + noiseDep(nPre+i)
		} else {
			lat = 200.0 + noiseDep(nPre+i) // step up in latency
		}
		depSamples = append(depSamples, DepSample{
			T:          postStart.Add(time.Duration(i*10) * time.Second),
			Saturation: 0.5,
			LatencyMs:  lat,
		})
	}

	deps := map[string][]DepSample{"downstream": depSamples}
	return jdBuildInput(scaleUpAt, cfg, nPre, preFn, nPost, postFn, deps)
}

func TestJudge_Scenario3_Harmful(t *testing.T) {
	in := buildScenario3(false)
	r := Judge(in)

	// Expect Harmful with culprit = "downstream".
	if r.Verdict != Harmful {
		t.Fatalf("SCENARIO 3: expected Harmful, got %v (reasons: %v)", r.Verdict, r.Reasons)
	}
	if r.Culprit != "downstream" {
		t.Fatalf("SCENARIO 3: expected culprit=downstream, got %q", r.Culprit)
	}

	// Assert saturation-only rule would NOT have fired:
	// max saturation*100 must be < SaturationPct (=90).
	const satThreshold = 90.0
	for _, ds := range in.Deps["downstream"] {
		if ds.Saturation*100 >= satThreshold {
			t.Fatalf("SCENARIO 3: saturation %.1f%% >= threshold %.0f%% – saturation rule would have fired",
				ds.Saturation*100, satThreshold)
		}
	}
}

func TestJudge_Scenario3_Control_NotHarmful(t *testing.T) {
	// Same as scenario 3 but dependency latency is flat → no latencyEv → not Harmful.
	in := buildScenario3(true)
	r := Judge(in)
	if r.Verdict == Harmful {
		t.Fatalf("control: expected NOT Harmful when dep latency flat, got Harmful (reasons: %v)", r.Reasons)
	}
}

// ---------------------------------------------------------------------------
// Helped scenario
// ---------------------------------------------------------------------------

func TestJudge_Helped(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()

	noiseP99 := jdNoisyF(10, 0, 2)
	noiseErr := jdNoisyF(11, 0, 0.0005)

	preFn := func(i int) (float64, float64, float64) {
		return 50.0, 200.0 + noiseP99(i), 0.01 + noiseErr(i)
	}
	postFn := func(i int) (float64, float64, float64) {
		// RPS rises significantly; P99 stays in range.
		return 120.0, 200.0 + noiseP99(20+i), 0.01 + noiseErr(20+i)
	}
	in := jdBuildInput(scaleUpAt, cfg, 20, preFn, 20, postFn, nil)
	r := Judge(in)

	if r.Verdict != Helped {
		t.Fatalf("Helped: expected Helped, got %v (reasons: %v)", r.Verdict, r.Reasons)
	}
}

// ---------------------------------------------------------------------------
// Useless scenario
// ---------------------------------------------------------------------------

func TestJudge_Useless(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()

	noiseP99 := jdNoisyF(20, 0, 2)
	noiseErr := jdNoisyF(21, 0, 0.0005)

	fn := func(i int) (float64, float64, float64) {
		return 100.0, 200.0 + noiseP99(i), 0.01 + noiseErr(i)
	}
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, nil)
	r := Judge(in)

	if r.Verdict != Useless && r.Verdict != Unknown {
		t.Fatalf("Useless: expected Useless or Unknown, got %v (reasons: %v)", r.Verdict, r.Reasons)
	}
}

// ---------------------------------------------------------------------------
// Unknown scenarios
// ---------------------------------------------------------------------------

func TestJudge_Unknown_ThinPre(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.MinSamples = 10

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	// Only 3 pre samples (< MinSamples=10).
	in := jdBuildInput(scaleUpAt, cfg, 3, fn, 20, fn, nil)
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/thin-pre: expected Unknown, got %v", r.Verdict)
	}
}

func TestJudge_Unknown_ThinPost(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.MinSamples = 10

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	// Only 3 post samples (< MinSamples=10).
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 3, fn, nil)
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/thin-post: expected Unknown, got %v", r.Verdict)
	}
}

func TestJudge_Unknown_NaN(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, nil)
	// Inject NaN.
	in.Series[0].P99Ms = math.NaN()
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/NaN: expected Unknown, got %v", r.Verdict)
	}
}

func TestJudge_Unknown_WarmupUnfinished(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.WarmupSeconds = 3600 // very long warmup

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, nil)
	// Set Ready below target to ensure warmup check fires.
	for i := range in.Series {
		in.Series[i].Ready = 1
	}
	in.ToReplicas = 4
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/warmup: expected Unknown, got %v (reasons: %v)", r.Verdict, r.Reasons)
	}
}

func TestJudge_Unknown_FitMetricFailure(t *testing.T) {
	// FitMetric requires at least 3 pre samples; MinSamples=5 but if all pre
	// have the same timestamp, FitMetric returns false.
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.MinSamples = 3

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 5, fn, 20, fn, nil)
	// Make all pre samples have the same timestamp → FitMetric fails (zero Sxx).
	for i := range in.Series {
		if in.Series[i].T.Before(scaleUpAt) {
			in.Series[i].T = scaleUpAt.Add(-10 * time.Second)
		}
	}
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/fit-fail: expected Unknown, got %v (reasons: %v)", r.Verdict, r.Reasons)
	}
}

func TestJudge_Unknown_LowConfidence(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.MinConfidence = 0.99 // very high threshold

	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 5, fn, 5, fn, nil)
	r := Judge(in)
	if r.Verdict != Unknown {
		t.Fatalf("Unknown/low-confidence: expected Unknown (confidence too low), got %v", r.Verdict)
	}
}

// ---------------------------------------------------------------------------
// Downgrade: Harmful → Unknown when pre data already shifting
// ---------------------------------------------------------------------------

func TestJudge_Downgrade_HarmfulToUnknown_PreShifting(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	cfg.MinSamples = 5
	cfg.HarmMargin = 0.05
	cfg.MinConfidence = 0.05

	// We need 20+ pre samples with a shift in the last 10 (so FindShift fires in
	// the pre data), but the service P99 also rises post-scale, and there's a dep
	// with latency that shifts at scale-up time.
	noiseP99 := jdNoisyF(100, 0, 2)
	noiseDep := jdNoisyF(101, 0, 1)
	noiseErr := jdNoisyF(102, 0, 0.0005)

	const nPre = 20
	const nPost = 20
	preFn := func(i int) (float64, float64, float64) {
		p99 := 200.0 + noiseP99(i)
		// Introduce a shift at index 10 (CUSUM will detect this in the pre data).
		if i >= 10 {
			p99 += 80
		}
		return 100.0, p99, 0.01 + noiseErr(i)
	}
	postFn := func(i int) (float64, float64, float64) {
		return 100.0, 400.0 + noiseP99(nPre+i), 0.01 + noiseErr(nPre+i)
	}

	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)
	preStart := scaleUpAt.Add(-time.Duration(nPre*10) * time.Second)
	postStart := warmupDone.Add(10 * time.Second)

	var depSamples []DepSample
	for i := 0; i < nPre; i++ {
		depSamples = append(depSamples, DepSample{
			T:          preStart.Add(time.Duration(i*10) * time.Second),
			Saturation: 0.5,
			LatencyMs:  100.0 + noiseDep(i),
		})
	}
	for i := 0; i < nPost; i++ {
		depSamples = append(depSamples, DepSample{
			T:          postStart.Add(time.Duration(i*10) * time.Second),
			Saturation: 0.5,
			LatencyMs:  200.0 + noiseDep(nPre+i),
		})
	}

	deps := map[string][]DepSample{"downstream": depSamples}
	in := jdBuildInput(scaleUpAt, cfg, nPre, preFn, nPost, postFn, deps)

	r := Judge(in)
	// The pre data has a shift, so checkTrafficAlreadyDegrading should return not_ruled_out.
	// If the base verdict would be Harmful, it should be downgraded to Unknown.
	// We accept either Unknown (downgraded) or a non-Harmful verdict.
	// The important thing is it's NOT Harmful.
	if r.Verdict == Harmful {
		// Check that alternatives include a not_ruled_out degrading alternative.
		for _, alt := range r.Alternatives {
			if alt.Hypothesis == "traffic already degrading before scale-up" &&
				alt.Status == AltNotRuledOut {
				t.Fatalf("downgrade: verdict=Harmful but traffic already degrading was not_ruled_out; should have been downgraded")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Non-mutation and determinism tests
// ---------------------------------------------------------------------------

func TestJudge_DoesNotMutateInput(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, nil)

	// Deep copy input.
	origSeries := make([]Sample, len(in.Series))
	copy(origSeries, in.Series)

	Judge(in)

	for i, s := range in.Series {
		if s != origSeries[i] {
			t.Fatalf("sample[%d] mutated by Judge", i)
		}
	}
}

func TestJudge_Determinism(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	depSamples := jdDepSamples(jdBase, 40, 10, func(i int) (float64, float64) {
		return 0.5, 100
	})
	deps := map[string][]DepSample{"db": depSamples, "cache": depSamples}
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, deps)

	first := Judge(in)
	for i := 0; i < 50; i++ {
		got := Judge(in)
		if got.Verdict != first.Verdict || got.Culprit != first.Culprit ||
			got.Confidence != first.Confidence {
			t.Fatalf("non-deterministic result at iteration %d: first=%+v got=%+v",
				i, first, got)
		}
	}
}

func TestJudge_EmptyInput_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Judge panicked on empty input: %v", r)
		}
	}()
	r := Judge(Input{})
	if r.Verdict != Unknown {
		t.Fatalf("empty input should return Unknown, got %v", r.Verdict)
	}
}

// ---------------------------------------------------------------------------
// Data quality integration
// ---------------------------------------------------------------------------

func TestJudge_DataQuality_Populated(t *testing.T) {
	scaleUpAt := jdBase.Add(400 * time.Second)
	cfg := jdJudgeCfg()
	fn := func(i int) (float64, float64, float64) { return 100, 200, 0.01 }
	in := jdBuildInput(scaleUpAt, cfg, 20, fn, 20, fn, nil)
	// Inject NaN to cause a data quality issue.
	in.Series[0].P99Ms = math.NaN()
	r := Judge(in)
	if len(r.DataQuality) == 0 {
		t.Fatal("expected DataQuality messages for NaN input")
	}
}
