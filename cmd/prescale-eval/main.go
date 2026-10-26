package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/report"
)

// ScenarioType identifies the kind of workload test case.
type ScenarioType string

const (
	ScenarioContentionHarmful     ScenarioType = "(A) contention-harmful"
	ScenarioHealthySurge          ScenarioType = "(B) healthy surge"
	ScenarioAlreadyDegrading      ScenarioType = "(C) already-degrading"
	ScenarioNoise                 ScenarioType = "(D) noise"
	ScenarioHardSurge             ScenarioType = "(E) hard surge"
	ScenarioHarmfulHighSaturation ScenarioType = "(F) harmful-with-saturation-high"
	ScenarioBadData               ScenarioType = "(G) bad data"
	ScenarioGenuinelyHelpful      ScenarioType = "(H) genuinely helpful"
)

// Scenario holds an input and its ground truth.
type Scenario struct {
	Type     ScenarioType
	Input    detector.Input
	Harmful  bool // ground truth: true = harmful (should cap), false = not harmful (should not cap)
	SeedDesc string
}

// Arm evaluates an Input and decides whether to cap without seeing ground truth.
type Arm interface {
	Name() string
	Decide(in detector.Input) (cap bool, unknown bool)
}

// 1. Plain HPA: never caps
type armPlainHPA struct{}

func (armPlainHPA) Name() string { return "plain HPA" }
func (armPlainHPA) Decide(in detector.Input) (bool, bool) {
	return false, false
}

// 2. Fixed cap: always caps
type armFixedCap struct{}

func (armFixedCap) Name() string { return "fixed cap" }
func (armFixedCap) Decide(in detector.Input) (bool, bool) {
	return true, false
}

// 3. Threshold guard: caps if max dep Saturation*100 >= 90
type armThresholdGuard struct{}

func (armThresholdGuard) Name() string { return "threshold guard" }
func (armThresholdGuard) Decide(in detector.Input) (bool, bool) {
	for _, depSeries := range in.Deps {
		for _, s := range depSeries {
			if !math.IsNaN(s.Saturation) && s.Saturation*100.0 >= 90.0 {
				return true, false
			}
		}
	}
	return false, false
}

// 4. Simple rule: caps if post mean p99 > pre mean p99 * 1.2 AND post RPS gain < 10%
type armSimpleRule struct{}

func (armSimpleRule) Name() string { return "simple rule" }
func (armSimpleRule) Decide(in detector.Input) (bool, bool) {
	var preP99, postP99 []float64
	var preRPS, postRPS []float64

	for _, s := range in.Series {
		if s.T.Before(in.ScaleUpAt) {
			preP99 = append(preP99, s.P99Ms)
			preRPS = append(preRPS, s.RPS)
		} else {
			postP99 = append(postP99, s.P99Ms)
			postRPS = append(postRPS, s.RPS)
		}
	}

	mPreP99, ok1 := evValidMean(preP99)
	mPostP99, ok2 := evValidMean(postP99)
	mPreRPS, ok3 := evValidMean(preRPS)
	mPostRPS, ok4 := evValidMean(postRPS)

	if !ok1 || !ok2 || !ok3 || !ok4 || mPreRPS <= 0 {
		return false, false
	}

	rpsGain := (mPostRPS - mPreRPS) / mPreRPS
	if mPostP99 > mPreP99*1.2 && rpsGain < 0.10 {
		return true, false
	}
	return false, false
}

func evValidMean(xs []float64) (float64, bool) {
	var sum float64
	var count int
	for _, x := range xs {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			sum += x
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

// 5. PreScale: detector.Judge; caps only on Harmful; Unknown = no cap (count unknowns separately)
type armPreScale struct{}

func (armPreScale) Name() string { return "PreScale" }
func (armPreScale) Decide(in detector.Input) (bool, bool) {
	res := detector.Judge(in)
	if res.Verdict == detector.Harmful {
		return true, false
	}
	if res.Verdict == detector.Unknown {
		return false, true
	}
	return false, false
}

// evWilsonScore computes the two-sided 95% Wilson score confidence interval.
func evWilsonScore(k, n int) (lower, upper float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.95996398454 // 95% confidence
	p := float64(k) / float64(n)
	z2 := z * z
	nF := float64(n)
	denom := 1.0 + z2/nF
	center := (p + z2/(2.0*nF)) / denom
	margin := (z / denom) * math.Sqrt((p*(1.0-p))/nF+z2/(4.0*nF*nF))
	lower = math.Max(0.0, center-margin)
	upper = math.Min(1.0, center+margin)
	return lower, upper
}

// evConfig returns the single Config used for all evaluations.
func evConfig() detector.Config {
	return detector.Config{
		MinSamples:        5,
		HarmMargin:        0.10,
		SaturationPct:     90.0,
		MinConfidence:     0.10,
		WarmupSeconds:     60,
		MinThroughputGain: 0.05,
	}
}

// evNoiseStd draws one of 3 noise standard deviation levels.
func evNoiseStd(rng *rand.Rand) float64 {
	levels := []float64{1.0, 3.0, 6.0}
	return levels[rng.Intn(len(levels))]
}

// evGenerateScenarios creates 200 of each scenario type using a deterministic PRNG seed.
func evGenerateScenarios(seed int64) []Scenario {
	rng := rand.New(rand.NewSource(seed))
	cfg := evConfig()

	var scenarios []Scenario

	// (A) Contention-harmful: effect magnitude 10-60% above baseline, dep latency lag 1-5 samples.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildContentionHarmful(rng, i, cfg))
	}

	// (B) Healthy surge: traffic doubles before and after scale-up; p99 rises moderately along pre-existing trend.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildHealthySurge(rng, i, cfg))
	}

	// (C) Already-degrading: p99 was already climbing before scale-up, scale-up has no effect, RPS flat.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildAlreadyDegrading(rng, i, cfg))
	}

	// (D) Noise: nothing changes.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildNoise(rng, i, cfg))
	}

	// (E) Hard surge: RPS rises only 3-9% while pre-existing trend pushes p99 up 30-60%. Truth: not harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildHardSurge(rng, i, cfg))
	}

	// (F) Harmful with saturation high: dep saturation 0.85-0.97 plus contention. Truth: harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildHarmfulHighSaturation(rng, i, cfg))
	}

	// (G) Bad data: 10-40% NaN or missing samples, stale tail. Truth: not harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildBadData(rng, i, cfg))
	}

	// (H) Genuinely helpful: p99 drops 20-40% after scale-up, RPS rises. Truth: not harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildGenuinelyHelpful(rng, i, cfg))
	}

	return scenarios
}

// (A) contention-harmful: replicas 3->10; dependency LatencyMs rises first, then service p99; dep Saturation uniform 0.3-0.7; RPS flat. Truth = harmful.
// Weakened: effect magnitude 10-60% above baseline, dep latency lag 1-5 samples.
func evBuildContentionHarmful(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	onsetLag := 1 + rng.Intn(5)          // lag 1-5 samples between dep latency jump and service p99 jump
	magPct := 0.10 + rng.Float64()*0.50  // magnitude 10-60% above baseline
	baseSat := 0.30 + rng.Float64()*0.40 // uniform 0.3-0.7
	baseP99 := 150.0 + rng.Float64()*100.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre samples: 20 samples, 10s step
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	// Warmup filler: dep latency jumps first during warmup
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + 80.0 + rng.NormFloat64()*noiseStd, // dep latency steps up first
		})
	}

	// Post warmup: dep latency remains high, service p99 steps up after onsetLag samples
	postStart := warmupDone.Add(10 * time.Second)
	p99Degrade := baseP99 * magPct

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		if i >= onsetLag {
			p99 += p99Degrade // service p99 steps up after lag
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + 100.0 + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioContentionHarmful,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  true,
		SeedDesc: fmt.Sprintf("contention-harmful-%d", idx),
	}
}

// (B) healthy surge: traffic doubles before and after scale-up; p99 rises moderately along pre-existing trend; RPS rises ~2x; dep latency flat. Truth = healthy (not harmful).
func evBuildHealthySurge(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	onsetOffset := rng.Intn(4) * 10
	surgedMult := 1.8 + rng.Float64()*0.4
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 130.0 + rng.Float64()*80.0
	baseRPS := 50.0 + rng.Float64()*30.0
	baseLat := 70.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		rps := baseRPS + float64(i)*1.5 + rng.NormFloat64()*noiseStd
		p99 := baseP99 + float64(i)*0.8 + rng.NormFloat64()*noiseStd
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      rps,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS*1.6 + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + 25.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(time.Duration(10+onsetOffset) * time.Second)
	surgedRPS := baseRPS * surgedMult

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      surgedRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + 30.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioHealthySurge,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("healthy-surge-%d", idx),
	}
}

// (C) already-degrading: p99 was already climbing before scale-up, scale-up has no effect, RPS flat. Truth = not harmful.
func evBuildAlreadyDegrading(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	preShiftIdx := 8 + rng.Intn(5)        // onset time within pre window
	shiftMag := 40.0 + rng.Float64()*50.0 // magnitude
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 140.0 + rng.Float64()*80.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		if i >= preShiftIdx {
			p99 += shiftMag // shift starts before scale-up
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + shiftMag + 80.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + 80.0 + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + shiftMag + 180.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + 100.0 + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioAlreadyDegrading,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("already-degrading-%d", idx),
	}
}

// (D) noise: nothing changes. Truth = not harmful.
func evBuildNoise(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 150.0 + rng.Float64()*100.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioNoise,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("noise-%d", idx),
	}
}

// (E) hard surge: RPS rises only 3-9% while pre-existing trend pushes p99 up 30-60%. Truth: not harmful.
func evBuildHardSurge(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	onsetIdx := 6 + rng.Intn(6)             // pre-existing trend starts in pre window
	p99PctRise := 0.30 + rng.Float64()*0.30 // 30-60%
	rpsPctRise := 0.03 + rng.Float64()*0.06 // 3-9%
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 150.0 + rng.Float64()*80.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		if i >= onsetIdx {
			p99 += float64(i-onsetIdx+1) * (baseP99 * p99PctRise / 15.0)
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd, // dep latency flat
		})
	}

	preEndP99 := baseP99 * (1.0 + p99PctRise)

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS*(1.0+rpsPctRise/2.0) + rng.NormFloat64()*noiseStd,
			P99Ms:    preEndP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	surgedRPS := baseRPS * (1.0 + rpsPctRise)

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      surgedRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    preEndP99 + 10.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioHardSurge,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("hard-surge-%d", idx),
	}
}

// (F) harmful-with-saturation-high: dep saturation 0.85-0.97 plus contention. Truth: harmful.
func evBuildHarmfulHighSaturation(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	highSat := 0.85 + rng.Float64()*0.12        // 0.85 - 0.97
	onsetLag := 1 + rng.Intn(3)                 // onset timing
	p99DegradeMult := 0.40 + rng.Float64()*0.40 // 40-80% increase
	baseP99 := 150.0 + rng.Float64()*80.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.50 + rng.Float64()*0.20,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + 50.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: highSat,
			LatencyMs:  baseLat + 100.0 + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	p99Degrade := baseP99 * p99DegradeMult

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		if i >= onsetLag {
			p99 += p99Degrade
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: highSat,
			LatencyMs:  baseLat + 150.0 + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioHarmfulHighSaturation,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  true,
		SeedDesc: fmt.Sprintf("harmful-high-sat-%d", idx),
	}
}

// (G) bad data: 10-40% NaN or missing samples, stale tail. Truth: not harmful.
func evBuildBadData(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	badRate := 0.10 + rng.Float64()*0.30 // 10-40% NaN or missing
	staleDropCount := 2 + rng.Intn(5)    // stale tail: drop last 2-6 samples
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 150.0 + rng.Float64()*80.0
	baseRPS := 80.0 + rng.Float64()*40.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		rps := baseRPS + rng.NormFloat64()*noiseStd
		if rng.Float64() < badRate {
			if rng.Intn(2) == 0 {
				p99 = math.NaN()
			} else {
				continue // missing sample
			}
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      rps,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	numPost := 20 - staleDropCount // stale tail
	for i := 0; i < numPost; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseStd
		rps := baseRPS + rng.NormFloat64()*noiseStd
		if rng.Float64() < badRate {
			if rng.Intn(2) == 0 {
				p99 = math.NaN()
			} else {
				continue
			}
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      rps,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioBadData,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("bad-data-%d", idx),
	}
}

// (H) genuinely helpful: p99 drops 20-40% after scale-up, RPS rises. Truth: not harmful.
func evBuildGenuinelyHelpful(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseStd := evNoiseStd(rng)
	p99DropPct := 0.20 + rng.Float64()*0.20 // 20-40% drop
	rpsRisePct := 0.20 + rng.Float64()*0.30 // 20-50% rise
	onsetOffset := rng.Intn(3) * 10
	baseSat := 0.30 + rng.Float64()*0.40
	baseP99 := 250.0 + rng.Float64()*80.0 // elevated before scaling
	baseRPS := 70.0 + rng.Float64()*30.0
	baseLat := 80.0 + rng.Float64()*40.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS*(1.0+rpsRisePct/2.0) + rng.NormFloat64()*noiseStd,
			P99Ms:    baseP99 - (baseP99*p99DropPct)/2.0 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	postStart := warmupDone.Add(time.Duration(10+onsetOffset) * time.Second)
	postP99 := baseP99 * (1.0 - p99DropPct)
	postRPS := baseRPS * (1.0 + rpsRisePct)

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      postRPS + rng.NormFloat64()*noiseStd,
			P99Ms:    postP99 + rng.NormFloat64()*noiseStd,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: baseSat,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseStd,
		})
	}

	return Scenario{
		Type: ScenarioGenuinelyHelpful,
		Input: detector.Input{
			ScaleUpAt:    scaleUpAt,
			FromReplicas: 3,
			ToReplicas:   10,
			Series:       series,
			Deps:         map[string][]detector.DepSample{"db": depSeries},
			Cfg:          cfg,
		},
		Harmful:  false,
		SeedDesc: fmt.Sprintf("genuinely-helpful-%d", idx),
	}
}

// ArmStats aggregates evaluation counts.
type ArmStats struct {
	N             int
	Caps          int
	Correct       int
	FalseFreezes  int
	MissedHarmful int
	Unknown       int
}

func evEvaluate(scenarios []Scenario, arms []Arm) (map[string]map[string]ArmStats, map[string]ArmStats) {
	perScenario := make(map[string]map[string]ArmStats)
	totals := make(map[string]ArmStats)

	for _, s := range scenarios {
		sType := string(s.Type)
		if perScenario[sType] == nil {
			perScenario[sType] = make(map[string]ArmStats)
		}

		for _, arm := range arms {
			armName := arm.Name()
			capped, unknown := arm.Decide(s.Input)

			st := perScenario[sType][armName]
			tot := totals[armName]

			st.N++
			tot.N++

			if capped {
				st.Caps++
				tot.Caps++
			}
			if unknown {
				st.Unknown++
				tot.Unknown++
			}

			if s.Harmful {
				if capped {
					st.Correct++
					tot.Correct++
				} else {
					st.MissedHarmful++
					tot.MissedHarmful++
				}
			} else {
				if !capped {
					st.Correct++
					tot.Correct++
				} else {
					st.FalseFreezes++
					tot.FalseFreezes++
				}
			}

			perScenario[sType][armName] = st
			totals[armName] = tot
		}
	}

	return perScenario, totals
}

func main() {
	seed := int64(42)
	scenarios := evGenerateScenarios(seed)

	arms := []Arm{
		armPlainHPA{},
		armFixedCap{},
		armThresholdGuard{},
		armSimpleRule{},
		armPreScale{},
	}

	perScenario, totals := evEvaluate(scenarios, arms)

	fmt.Println("========================================================================================================")
	fmt.Println("PRESCALE 5-ARM EVALUATION (N=200 per scenario, 1600 total)")
	fmt.Println("========================================================================================================")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Scenario\tArm\tN\tCaps\tCorrect\tFalse Freezes\tMissed Harmful\tUnknown")

	types := []string{
		string(ScenarioContentionHarmful),
		string(ScenarioHealthySurge),
		string(ScenarioAlreadyDegrading),
		string(ScenarioNoise),
		string(ScenarioHardSurge),
		string(ScenarioHarmfulHighSaturation),
		string(ScenarioBadData),
		string(ScenarioGenuinelyHelpful),
	}

	for _, sType := range types {
		for _, arm := range arms {
			st := perScenario[sType][arm.Name()]
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
				sType, arm.Name(), st.N, st.Caps, st.Correct, st.FalseFreezes, st.MissedHarmful, st.Unknown)
		}
	}
	w.Flush()

	fmt.Println("\n========================================================================================================")
	fmt.Println("TOTALS PER ARM (N=1600: 400 Harmful, 1200 Not Harmful)")
	fmt.Println("========================================================================================================")

	wTot := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(wTot, "Arm\tN\tCaps\tCorrect\tFalse Freezes\tMissed Harmful\tUnknown")
	for _, arm := range arms {
		tot := totals[arm.Name()]
		fmt.Fprintf(wTot, "%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
			arm.Name(), tot.N, tot.Caps, tot.Correct, tot.FalseFreezes, tot.MissedHarmful, tot.Unknown)
	}
	wTot.Flush()

	fmt.Println("\n========================================================================================================")
	fmt.Println("JUDGE VERDICTS PER SCENARIO TYPE")
	fmt.Println("========================================================================================================")

	type verdictCounts struct {
		helped  int
		useless int
		harmful int
		unknown int
	}
	judgeCounts := make(map[string]*verdictCounts)
	for _, t := range types {
		judgeCounts[t] = &verdictCounts{}
	}

	var scenAMissedMags []float64
	var scenACaughtMags []float64

	for _, s := range scenarios {
		res := detector.Judge(s.Input)
		st := judgeCounts[string(s.Type)]
		switch res.Verdict {
		case detector.Helped:
			st.helped++
		case detector.Useless:
			st.useless++
		case detector.Harmful:
			st.harmful++
		case detector.Unknown:
			st.unknown++
		}

		if s.Type == ScenarioContentionHarmful {
			var p99Mag float64
			for _, f := range res.Findings {
				if f.Name == "p99_ms" {
					if f.Expected > 0 {
						p99Mag = (f.Observed - f.Expected) / f.Expected * 100.0
					}
					break
				}
			}
			if res.Verdict == detector.Harmful {
				scenACaughtMags = append(scenACaughtMags, p99Mag)
			} else {
				scenAMissedMags = append(scenAMissedMags, p99Mag)
			}
		}
	}

	wJudge := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(wJudge, "Scenario\tHelped\tUseless\tHarmful\tUnknown")
	for _, sType := range types {
		st := judgeCounts[sType]
		fmt.Fprintf(wJudge, "%s\t%d\t%d\t%d\t%d\n",
			sType, st.helped, st.useless, st.harmful, st.unknown)
	}
	wJudge.Flush()

	var meanMissed, meanCaught float64
	if len(scenAMissedMags) > 0 {
		var sum float64
		for _, m := range scenAMissedMags {
			sum += m
		}
		meanMissed = sum / float64(len(scenAMissedMags))
	}
	if len(scenACaughtMags) > 0 {
		var sum float64
		for _, m := range scenACaughtMags {
			sum += m
		}
		meanCaught = sum / float64(len(scenACaughtMags))
	}

	fmt.Println("\nScenario (A) p99 effect magnitude (post vs expected, as a percent):")
	fmt.Printf("  PreScale missed (N=%d): mean = %.1f%%\n", len(scenAMissedMags), meanMissed)
	fmt.Printf("  PreScale caught (N=%d): mean = %.1f%%\n", len(scenACaughtMags), meanCaught)

	fmt.Println("\n========================================================================================================")
	fmt.Println("95% WILSON CONFIDENCE INTERVALS")
	fmt.Println("========================================================================================================")

	wCI := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(wCI, "Arm\tFalse-Freeze Rate (vs 1200 non-harmful)\tMissed-Harmful Rate (vs 400 harmful)")
	for _, arm := range arms {
		tot := totals[arm.Name()]
		ffLow, ffHigh := evWilsonScore(tot.FalseFreezes, 1200)
		mhLow, mhHigh := evWilsonScore(tot.MissedHarmful, 400)

		ffRate := float64(tot.FalseFreezes) / 1200.0 * 100.0
		mhRate := float64(tot.MissedHarmful) / 400.0 * 100.0

		fmt.Fprintf(wCI, "%s\t%.1f%% [%.1f%%, %.1f%%]\t%.1f%% [%.1f%%, %.1f%%]\n",
			arm.Name(),
			ffRate, ffLow*100.0, ffHigh*100.0,
			mhRate, mhLow*100.0, mhHigh*100.0,
		)
	}
	wCI.Flush()

	fmt.Println("\n========================================================================================================")
	fmt.Println("SAMPLE JUDGE REPORTS (5 REPRESENTATIVE SAMPLES)")
	fmt.Println("========================================================================================================")

	var samplesToPrint []Scenario
	for _, s := range scenarios {
		if s.Type == ScenarioContentionHarmful && len(samplesToPrint) == 0 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioHealthySurge && len(samplesToPrint) == 1 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioAlreadyDegrading && len(samplesToPrint) == 2 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioBadData && len(samplesToPrint) == 3 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioGenuinelyHelpful && len(samplesToPrint) == 4 {
			samplesToPrint = append(samplesToPrint, s)
		}
	}

	for i, smp := range samplesToPrint {
		res := detector.Judge(smp.Input)
		fmt.Printf("\n--- Sample %d: %s (Ground Truth: Harmful=%v) ---\n", i+1, smp.Type, smp.Harmful)
		fmt.Print(report.Render(res))
	}

	_ = sort.Strings
}
