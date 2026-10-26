// Package main is the prescale-live command. It fetches real Prometheus metrics
// for a running checkout workload, detects the HPA scale-up event automatically,
// builds a detector.Input, calls detector.Judge, and prints the verdict.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/metrics"
	"github.com/prescale/prescale/internal/report"
)

// detectorConfig returns the same Config as cmd/prescale-eval so results are comparable.
func detectorConfig() detector.Config {
	return detector.Config{
		MinSamples:        5,
		HarmMargin:        0.10,
		SaturationPct:     90.0,
		MinConfidence:     0.10,
		WarmupSeconds:     60,
		MinThroughputGain: 0.05,
	}
}

// metricsConfig builds a metrics.Config using pod-based label selectors.
// Uses rate windows of 4*step, at least 30s.
func metricsConfig(promURL string, step time.Duration) metrics.Config {
	rateWindow := 4 * step
	if rateWindow < 30*time.Second {
		rateWindow = 30 * time.Second
	}
	win := fmtDuration(rateWindow)

	return metrics.Config{
		BaseURL: promURL,

		// Service RPS: sum over all pods matching the deployment name
		RPS: `sum(rate(http_requests_total{namespace="{{namespace}}",pod=~"{{name}}-.*"}[` + win + `]))`,

		// P99 latency in milliseconds from the histogram
		P99Ms: `histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{namespace="{{namespace}}",pod=~"{{name}}-.*"}[` + win + `])) by (le)) * 1000`,

		// Error rate fraction
		ErrRate: `sum(rate(http_requests_total{namespace="{{namespace}}",pod=~"{{name}}-.*",code=~"5.."}[` + win + `])) / clamp_min(sum(rate(http_requests_total{namespace="{{namespace}}",pod=~"{{name}}-.*"}[` + win + `])), 0.001)`,

		// Desired replicas from kube_deployment_status_replicas
		Replicas: `kube_deployment_status_replicas{namespace="{{namespace}}",deployment="{{name}}"}`,

		// Ready replicas
		Ready: `kube_deployment_status_replicas_ready{namespace="{{namespace}}",deployment="{{name}}"}`,

		// Dependency saturation: active / max connections
		Saturation: `sum(db_active_connections{namespace="{{namespace}}",pod=~"{{dep}}-.*"}) / clamp_min(sum(db_max_connections{namespace="{{namespace}}",pod=~"{{dep}}-.*"}), 1)`,

		// Dependency p99 latency in milliseconds
		LatencyMs: `histogram_quantile(0.99, sum(rate(db_query_duration_seconds_bucket{namespace="{{namespace}}",pod=~"{{dep}}-.*"}[` + win + `])) by (le)) * 1000`,
	}
}

// fmtDuration converts a duration to a Prometheus range string (e.g., "20s", "2m").
func fmtDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// detectScaleUp scans samples to find the first sample where Replicas rises
// above the earlier value and stays >= that for at least 3 consecutive samples.
// Returns (scaleUpIndex, fromReplicas, toReplicas, found).
func detectScaleUp(samples []detector.Sample) (idx, fromReplicas, toReplicas int, found bool) {
	if len(samples) < 4 {
		return 0, 0, 0, false
	}

	for i := 1; i < len(samples); i++ {
		cur := samples[i].Replicas
		prev := samples[i-1].Replicas
		if cur <= prev {
			continue
		}
		// Replicas rose at index i. Check that it stays >= cur for 3 samples total.
		stayCount := 1
		for j := i + 1; j < len(samples) && stayCount < 3; j++ {
			if samples[j].Replicas >= cur {
				stayCount++
			} else {
				break
			}
		}
		if stayCount >= 3 {
			return i, prev, cur, true
		}
	}
	return 0, 0, 0, false
}

// splitSeries splits samples into pre-warmup and post-warmup based on the
// scale-up event time and the warmup duration (cfg.WarmupSeconds).
// It returns the index at which warmup ends in the sample slice.
func splitSeries(samples []detector.Sample, scaleUpIdx int, warmupSec int) (pre, post []detector.Sample, scaleUpAt time.Time) {
	scaleUpAt = samples[scaleUpIdx].T
	warmupEnd := scaleUpAt.Add(time.Duration(warmupSec) * time.Second)
	pre = samples[:scaleUpIdx]
	for i := scaleUpIdx; i < len(samples); i++ {
		if samples[i].T.After(warmupEnd) {
			post = samples[i:]
			break
		}
	}
	return pre, post, scaleUpAt
}

// maxSaturation returns the maximum saturation value across all dep samples.
func maxSaturation(depSamples []detector.DepSample) float64 {
	var max float64
	for _, s := range depSamples {
		if !math.IsNaN(s.Saturation) && s.Saturation > max {
			max = s.Saturation
		}
	}
	return max
}

// writeCSV writes all raw samples to a CSV file for evidence.
func writeCSV(path string, series []detector.Sample, depSeries []detector.DepSample) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Header
	if err := w.Write([]string{
		"timestamp", "replicas", "ready", "rps", "p99_ms", "err_rate",
		"dep_saturation", "dep_latency_ms",
	}); err != nil {
		return err
	}

	// Build dep lookup by timestamp
	depByTs := make(map[int64]detector.DepSample, len(depSeries))
	for _, d := range depSeries {
		depByTs[d.T.Unix()] = d
	}

	for _, s := range series {
		dep := depByTs[s.T.Unix()]
		sat := ""
		lat := ""
		if !math.IsNaN(dep.Saturation) && dep.T.Unix() != 0 {
			sat = strconv.FormatFloat(dep.Saturation, 'f', 6, 64)
			lat = strconv.FormatFloat(dep.LatencyMs, 'f', 3, 64)
		}
		row := []string{
			s.T.UTC().Format(time.RFC3339),
			strconv.Itoa(s.Replicas),
			strconv.Itoa(s.Ready),
			strconv.FormatFloat(s.RPS, 'f', 3, 64),
			strconv.FormatFloat(s.P99Ms, 'f', 3, 64),
			strconv.FormatFloat(s.ErrRate, 'f', 6, 64),
			sat,
			lat,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// run contains all the logic so it can be tested without os.Exit.
func run(
	src metrics.Source,
	promURL, namespace, name, dep, depName, csvPath string,
	last, step time.Duration,
	now time.Time,
	out io.Writer,
) (exitCode int) {
	ctx := context.Background()
	cfg := detectorConfig()
	tgt := metrics.Target{Namespace: namespace, Name: name}
	depRef := metrics.DepRef{Name: depName}

	from := now.Add(-last)
	to := now

	// 1. Fetch service series
	series, seriesErr := src.Series(ctx, tgt, from, to, step)
	if seriesErr != nil {
		if errors.Is(seriesErr, metrics.ErrStale) {
			fmt.Fprintln(out, "metrics error: stale data")
		} else if errors.Is(seriesErr, metrics.ErrNoData) {
			fmt.Fprintln(out, "metrics error: no data")
		} else {
			fmt.Fprintf(out, "metrics error: %v\n", seriesErr)
		}
		// Still build an unknown result so the rest of the code can run
		result := detector.Result{Verdict: detector.Unknown, Reasons: []string{seriesErr.Error()}}
		fmt.Fprint(out, report.Render(result))
		return 0
	}

	// 2. Detect scale-up event
	scaleUpIdx, fromReplicas, toReplicas, found := detectScaleUp(series)
	if !found {
		fmt.Fprintln(out, "no scale-up found")
		return 0
	}

	// 3. Fetch dependency series
	depSamples, depErr := src.DepSeries(ctx, tgt, depRef, from, to, step)
	metricsErrMsg := ""
	if depErr != nil {
		if errors.Is(depErr, metrics.ErrStale) {
			metricsErrMsg = "dep metrics error: stale data"
		} else if errors.Is(depErr, metrics.ErrNoData) {
			metricsErrMsg = "dep metrics error: no data"
		} else {
			metricsErrMsg = fmt.Sprintf("dep metrics error: %v", depErr)
		}
	}

	// 4. Build detector.Input
	in := detector.Input{
		ScaleUpAt:    series[scaleUpIdx].T,
		FromReplicas: fromReplicas,
		ToReplicas:   toReplicas,
		Series:       series,
		Deps:         map[string][]detector.DepSample{dep: depSamples},
		Cfg:          cfg,
	}

	// 5. Count pre/post samples
	pre, post, _ := splitSeries(series, scaleUpIdx, cfg.WarmupSeconds)

	// 6. Max dep saturation
	maxSat := maxSaturation(depSamples)
	satAlertFires := maxSat*100 >= cfg.SaturationPct

	// 7. Call detector
	result := detector.Judge(in)

	// 8. Print summary
	fmt.Fprintf(out, "Scale-up detected at %s: replicas %d → %d\n",
		series[scaleUpIdx].T.UTC().Format(time.RFC3339), fromReplicas, toReplicas)
	fmt.Fprintf(out, "Samples: %d pre-scale, %d post-warmup\n", len(pre), len(post))
	if metricsErrMsg != "" {
		fmt.Fprintln(out, metricsErrMsg)
	}
	fmt.Fprintf(out, "Max dependency saturation: %.1f%%\n", maxSat*100)
	fmt.Fprintf(out, "Saturation alert (>=%.0f%%): %v\n", cfg.SaturationPct, satAlertFires)
	fmt.Fprintln(out)
	fmt.Fprint(out, report.Render(result))

	// 9. Write CSV
	if csvPath != "" {
		if err := writeCSV(csvPath, series, depSamples); err != nil {
			fmt.Fprintf(out, "warning: failed to write CSV: %v\n", err)
		} else {
			fmt.Fprintf(out, "Raw samples written to: %s\n", csvPath)
		}
	}

	return 0
}

func main() {
	prom := flag.String("prom", "http://localhost:9090", "Prometheus base URL")
	ns := flag.String("ns", "demo", "Kubernetes namespace")
	name := flag.String("name", "checkout", "Deployment name (workload)")
	dep := flag.String("dep", "db", "Dependency key (used as map key in detector.Input.Deps)")
	depName := flag.String("depname", "fakedb", "Dependency pod name prefix for metrics scraping")
	last := flag.Duration("last", 20*time.Minute, "Look-back window (e.g. 20m, 1h)")
	step := flag.Duration("step", 5*time.Second, "Prometheus query step (e.g. 5s, 15s)")
	csvOut := flag.String("csv", "out.csv", "Path for CSV output of raw samples")
	flag.Parse()

	mcfg := metricsConfig(*prom, *step)
	src := metrics.NewPrometheus(mcfg)

	code := run(src, *prom, *ns, *name, *dep, *depName, *csvOut, *last, *step, time.Now(), os.Stdout)
	os.Exit(code)
}
