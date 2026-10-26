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
	ScenarioContentionHarmful ScenarioType = "(A) contention-harmful"
	ScenarioHealthySurge      ScenarioType = "(B) healthy surge"
	ScenarioAlreadyDegrading  ScenarioType = "(C) already-degrading"
	ScenarioNoise             ScenarioType = "(D) noise"
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
			if s.Saturation*100.0 >= 90.0 {
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

	if len(preP99) == 0 || len(postP99) == 0 || len(preRPS) == 0 || len(postRPS) == 0 {
		return false, false
	}

	mPreP99 := evMean(preP99)
	mPostP99 := evMean(postP99)
	mPreRPS := evMean(preRPS)
	mPostRPS := evMean(postRPS)

	if mPreRPS <= 0 {
		return false, false
	}

	rpsGain := (mPostRPS - mPreRPS) / mPreRPS
	if mPostP99 > mPreP99*1.2 && rpsGain < 0.10 {
		return true, false
	}
	return false, false
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

func evMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
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

// Global evaluation configuration
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

// evGenerateScenarios creates 200 of each scenario type using a deterministic PRNG seed.
func evGenerateScenarios(seed int64) []Scenario {
	rng := rand.New(rand.NewSource(seed))
	cfg := evConfig()

	var scenarios []Scenario

	// (A) Contention-harmful: replicas 3->10; dependency LatencyMs rises first, then service p99; dep Saturation uniform 0.3-0.7; RPS flat. Truth = harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildContentionHarmful(rng, i, cfg))
	}

	// (B) Healthy surge: traffic doubles before and after scale-up; p99 rises moderately along pre-existing trend; RPS rises ~2x; dep latency flat. Truth = healthy (not harmful).
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildHealthySurge(rng, i, cfg))
	}

	// (C) Already-degrading: p99 was already climbing before scale-up, scale-up has no effect, RPS flat. Truth = not harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildAlreadyDegrading(rng, i, cfg))
	}

	// (D) Noise: nothing changes. Truth = not harmful.
	for i := 0; i < 200; i++ {
		scenarios = append(scenarios, evBuildNoise(rng, i, cfg))
	}

	return scenarios
}

func evBuildContentionHarmful(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseP99 := 2.0 + rng.Float64()*3.0
	noiseLat := 1.0 + rng.Float64()*2.0
	noiseRPS := 1.0 + rng.Float64()*2.0

	baseRPS := 100.0 + rng.Float64()*10.0
	baseP99 := 200.0 + rng.Float64()*20.0
	baseLat := 100.0 + rng.Float64()*10.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre samples: 20 samples, 10s step
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4, // uniform 0.3 - 0.7
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
		})
	}

	// Warmup filler: dependency latency starts rising during warmup
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + 50.0 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + 80.0 + rng.NormFloat64()*noiseLat, // dep latency jumps earlier
		})
	}

	// Post warmup: dep latency high, service p99 degraded
	postStart := warmupDone.Add(10 * time.Second)
	p99Degrade := 150.0 + rng.Float64()*50.0
	latDegrade := 100.0 + rng.Float64()*30.0

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS, // flat RPS
			P99Ms:    baseP99 + p99Degrade + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + latDegrade + rng.NormFloat64()*noiseLat,
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

func evBuildHealthySurge(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseP99 := 2.0 + rng.Float64()*2.0
	noiseRPS := 2.0 + rng.Float64()*2.0
	noiseLat := 1.0 + rng.Float64()*1.0

	baseRPS := 50.0 + rng.Float64()*10.0
	baseP99 := 150.0 + rng.Float64()*10.0
	baseLat := 100.0 + rng.Float64()*10.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre samples: RPS rises moderately leading into surge
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		rps := baseRPS + float64(i)*1.5 + rng.NormFloat64()*noiseRPS
		p99 := baseP99 + float64(i)*0.8 + rng.NormFloat64()*noiseP99
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
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat, // dep latency flat
		})
	}

	// Warmup filler
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS*1.6 + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + 25.0 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
		})
	}

	// Post warmup: traffic has ~doubled, p99 remains within expected trend
	postStart := warmupDone.Add(10 * time.Second)
	surgedRPS := baseRPS * (1.9 + rng.Float64()*0.3)

	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      surgedRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + 25.0 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat, // flat dep latency
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

func evBuildAlreadyDegrading(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseP99 := 2.0 + rng.Float64()*2.0
	noiseRPS := 1.0 + rng.Float64()*2.0
	noiseLat := 1.0 + rng.Float64()*1.0

	baseRPS := 100.0 + rng.Float64()*10.0
	baseP99 := 150.0 + rng.Float64()*10.0
	baseLat := 100.0 + rng.Float64()*10.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre samples: P99 shifts up halfway through pre-window (already degrading before scale-up)
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		p99 := baseP99 + rng.NormFloat64()*noiseP99
		if i >= 10 {
			p99 += 60.0 // shift occurred before scale-up
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
		})
	}

	// Warmup filler
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + 150.0 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + 80.0 + rng.NormFloat64()*noiseLat,
		})
	}

	// Post warmup: p99 stays degraded (400ms); dep latency elevated
	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + 250.0 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + 100.0 + rng.NormFloat64()*noiseLat,
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

func evBuildNoise(rng *rand.Rand, idx int, cfg detector.Config) Scenario {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	noiseP99 := 2.0 + rng.Float64()*3.0
	noiseRPS := 1.0 + rng.Float64()*2.0
	noiseLat := 1.0 + rng.Float64()*1.0

	baseRPS := 100.0 + rng.Float64()*10.0
	baseP99 := 200.0 + rng.Float64()*20.0
	baseLat := 100.0 + rng.Float64()*10.0

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
		})
	}

	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
		})
	}

	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      baseRPS + rng.NormFloat64()*noiseRPS,
			P99Ms:    baseP99 + rng.NormFloat64()*noiseP99,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.3 + rng.Float64()*0.4,
			LatencyMs:  baseLat + rng.NormFloat64()*noiseLat,
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
	fmt.Println("PRESCALE 5-ARM EVALUATION (N=200 per scenario, 800 total)")
	fmt.Println("========================================================================================================")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Scenario\tArm\tN\tCaps\tCorrect\tFalse Freezes\tMissed Harmful\tUnknown")

	types := []string{
		string(ScenarioContentionHarmful),
		string(ScenarioHealthySurge),
		string(ScenarioAlreadyDegrading),
		string(ScenarioNoise),
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
	fmt.Println("TOTALS PER ARM (N=800: 200 Harmful, 600 Not Harmful)")
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
	fmt.Println("95% WILSON CONFIDENCE INTERVALS")
	fmt.Println("========================================================================================================")

	wCI := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(wCI, "Arm\tFalse-Freeze Rate (vs 600 non-harmful)\tMissed-Harmful Rate (vs 200 harmful)")
	for _, arm := range arms {
		tot := totals[arm.Name()]
		ffLow, ffHigh := evWilsonScore(tot.FalseFreezes, 600)
		mhLow, mhHigh := evWilsonScore(tot.MissedHarmful, 200)

		ffRate := float64(tot.FalseFreezes) / 600.0 * 100.0
		mhRate := float64(tot.MissedHarmful) / 200.0 * 100.0

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

	// Pick 5 representative scenarios across types, ensuring at least one Unknown
	var samplesToPrint []Scenario
	for _, s := range scenarios {
		if s.Type == ScenarioContentionHarmful && len(samplesToPrint) == 0 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioHealthySurge && len(samplesToPrint) == 1 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioAlreadyDegrading && len(samplesToPrint) == 2 {
			samplesToPrint = append(samplesToPrint, s)
		} else if s.Type == ScenarioNoise && len(samplesToPrint) == 3 {
			samplesToPrint = append(samplesToPrint, s)
		}
	}

	// 5th sample: an already-degrading or noisy case with Unknown verdict
	for _, s := range scenarios {
		res := detector.Judge(s.Input)
		if res.Verdict == detector.Unknown && len(samplesToPrint) == 4 {
			samplesToPrint = append(samplesToPrint, s)
			break
		}
	}
	if len(samplesToPrint) < 5 {
		samplesToPrint = append(samplesToPrint, scenarios[len(scenarios)-1])
	}

	for i, smp := range samplesToPrint {
		res := detector.Judge(smp.Input)
		fmt.Printf("\n--- Sample %d: %s (Ground Truth: Harmful=%v) ---\n", i+1, smp.Type, smp.Harmful)
		fmt.Print(report.Render(res))
	}

	_ = sort.Strings
}
