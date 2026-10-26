package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/report"
)

func demoConfig() detector.Config {
	return detector.Config{
		MinSamples:        5,
		HarmMargin:        0.10,
		SaturationPct:     90.0,
		MinConfidence:     0.10,
		WarmupSeconds:     60,
		MinThroughputGain: 0.05,
	}
}

func buildScenario3(rng *rand.Rand, flatDepLatency bool) detector.Input {
	cfg := demoConfig()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre: 20 samples (0s to 190s), step 10s
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      100.0 + rng.NormFloat64()*1.0,
			P99Ms:    150.0 + rng.NormFloat64()*2.0,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  80.0 + rng.NormFloat64()*1.0,
		})
	}

	// Warmup: 200s to 260s (7 samples)
	// dependency LatencyMs rises first
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      100.0 + rng.NormFloat64()*1.0,
			P99Ms:    150.0 + rng.NormFloat64()*2.0,
			ErrRate:  0.01,
		})
		lat := 80.0 + rng.NormFloat64()*1.0
		if !flatDepLatency {
			lat = 180.0 + rng.NormFloat64()*1.0 // dep latency rises first
		}
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  lat,
		})
	}

	// Post: 20 samples (270s to 460s)
	// service p99 rises
	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      100.0 + rng.NormFloat64()*1.0,
			P99Ms:    250.0 + rng.NormFloat64()*2.0, // service p99 rises
			ErrRate:  0.01,
		})
		lat := 80.0 + rng.NormFloat64()*1.0
		if !flatDepLatency {
			lat = 180.0 + rng.NormFloat64()*1.0
		}
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  lat,
		})
	}

	return detector.Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 3,
		ToReplicas:   10,
		Series:       series,
		Deps:         map[string][]detector.DepSample{"db": depSeries},
		Cfg:          cfg,
	}
}

func buildHealthySurge(rng *rand.Rand) detector.Input {
	cfg := demoConfig()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scaleUpAt := t0.Add(200 * time.Second)
	warmupDone := scaleUpAt.Add(time.Duration(cfg.WarmupSeconds) * time.Second)

	var series []detector.Sample
	var depSeries []detector.DepSample

	// Pre: 20 samples, RPS = 50, P99 = 150
	for i := 0; i < 20; i++ {
		ts := t0.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      50.0 + rng.NormFloat64()*1.0,
			P99Ms:    150.0 + rng.NormFloat64()*2.0,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  80.0 + rng.NormFloat64()*1.0,
		})
	}

	// Warmup:
	for ts := scaleUpAt; !ts.After(warmupDone); ts = ts.Add(10 * time.Second) {
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      75.0 + rng.NormFloat64()*1.0,
			P99Ms:    151.0 + rng.NormFloat64()*2.0,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  80.0 + rng.NormFloat64()*1.0,
		})
	}

	// Post: RPS rises to 100, P99 stays within interval (150)
	postStart := warmupDone.Add(10 * time.Second)
	for i := 0; i < 20; i++ {
		ts := postStart.Add(time.Duration(i*10) * time.Second)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 10,
			Ready:    10,
			RPS:      100.0 + rng.NormFloat64()*1.0,
			P99Ms:    152.0 + rng.NormFloat64()*2.0,
			ErrRate:  0.01,
		})
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  80.0 + rng.NormFloat64()*1.0,
		})
	}

	return detector.Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: 3,
		ToReplicas:   10,
		Series:       series,
		Deps:         map[string][]detector.DepSample{"db": depSeries},
		Cfg:          cfg,
	}
}

func runDemo() string {
	rng := rand.New(rand.NewSource(42))

	in := buildScenario3(rng, false)

	var maxSat float64
	for _, depList := range in.Deps {
		for _, ds := range depList {
			if ds.Saturation > maxSat {
				maxSat = ds.Saturation
			}
		}
	}

	res := detector.Judge(in)

	controlIn := buildScenario3(rng, true)
	controlRes := detector.Judge(controlIn)

	healthyIn := buildHealthySurge(rng)
	healthyRes := detector.Judge(healthyIn)

	out := fmt.Sprintf("Alert check: max dependency saturation = %d%%, threshold = 90%%, alert fires: false\n", int(maxSat*100))
	out += report.Render(res)
	out += fmt.Sprintf("Control verdict: %s\n", controlRes.Verdict)
	out += report.Render(controlRes)
	out += fmt.Sprintf("Healthy-surge verdict: %s\n", healthyRes.Verdict)

	return out
}

func main() {
	fmt.Print(runDemo())
}
