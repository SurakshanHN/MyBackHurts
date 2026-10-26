package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/metrics"
)

// liveBase is the base time used for test fixtures.
var liveBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// liveStep is the scrape interval used in tests.
const liveStep = 5 * time.Second

// buildHarmfulFixture creates a series where:
// - replicas start at 3, rise to 10 at sample 20, and stay there
// - p99 rises well above expected after warmup
// - dep latency jumps at scale-up time
// - dep saturation stays below 90%
func buildHarmfulFixture() ([]detector.Sample, []detector.DepSample) {
	scaleUpAt := liveBase.Add(20 * liveStep)     // index 20: 20 pre samples
	warmupEnd := scaleUpAt.Add(60 * time.Second) // 12 samples of 5s warmup

	var series []detector.Sample
	var depSeries []detector.DepSample

	for i := 0; i < 50; i++ {
		ts := liveBase.Add(time.Duration(i) * liveStep)
		replicas := 3
		if !ts.Before(scaleUpAt) {
			replicas = 10
		}
		p99 := 150.0
		// Post-warmup: p99 jumps up significantly
		if ts.After(warmupEnd) {
			p99 = 320.0
		}
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: replicas,
			Ready:    replicas,
			RPS:      100.0,
			P99Ms:    p99,
			ErrRate:  0.01,
		})
		lat := 80.0
		if !ts.Before(scaleUpAt) {
			lat = 200.0 // dep latency jumps at scale-up
		}
		depSeries = append(depSeries, detector.DepSample{
			T:          ts,
			Saturation: 0.5,
			LatencyMs:  lat,
		})
	}
	return series, depSeries
}

// TestRun_HarmfulFixture verifies that a clear contention scenario produces Harmful.
func TestRun_HarmfulFixture(t *testing.T) {
	series, depSeries := buildHarmfulFixture()

	fk := metrics.NewFake()
	tgt := metrics.Target{Namespace: "demo", Name: "checkout"}
	depRef := metrics.DepRef{Name: "fakedb"}
	fk.SetSeries(tgt, series)
	fk.SetDepSeries(tgt, depRef, depSeries)

	var buf bytes.Buffer
	now := liveBase.Add(50 * liveStep)
	from := liveBase
	_ = from

	code := run(fk, "http://unused", "demo", "checkout", "db", "fakedb", "",
		50*liveStep, liveStep, now, &buf)

	output := buf.String()
	t.Logf("output:\n%s", output)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(output, "Verdict: harmful") {
		t.Errorf("expected 'Verdict: harmful', got:\n%s", output)
	}
	if !strings.Contains(output, "Scale-up detected") {
		t.Errorf("expected 'Scale-up detected', got:\n%s", output)
	}
}

// TestRun_NoScaleUp verifies that when no scale-up is detected, the program
// prints "no scale-up found" and exits 0.
func TestRun_NoScaleUp(t *testing.T) {
	// Build a flat series with constant replicas
	var series []detector.Sample
	for i := 0; i < 30; i++ {
		ts := liveBase.Add(time.Duration(i) * liveStep)
		series = append(series, detector.Sample{
			T:        ts,
			Replicas: 3,
			Ready:    3,
			RPS:      100.0,
			P99Ms:    150.0,
			ErrRate:  0.01,
		})
	}

	fk := metrics.NewFake()
	tgt := metrics.Target{Namespace: "demo", Name: "checkout"}
	fk.SetSeries(tgt, series)

	var buf bytes.Buffer
	now := liveBase.Add(30 * liveStep)

	code := run(fk, "http://unused", "demo", "checkout", "db", "fakedb", "",
		30*liveStep, liveStep, now, &buf)

	output := buf.String()
	t.Logf("output:\n%s", output)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(output, "no scale-up found") {
		t.Errorf("expected 'no scale-up found', got:\n%s", output)
	}
}

// TestRun_MetricsError verifies that a metrics fetch error yields Unknown verdict
// with the error message printed plainly.
func TestRun_MetricsError(t *testing.T) {
	fk := metrics.NewFake()
	fk.SeriesErr = metrics.ErrNoData

	var buf bytes.Buffer
	now := liveBase.Add(30 * liveStep)

	code := run(fk, "http://unused", "demo", "checkout", "db", "fakedb", "",
		30*liveStep, liveStep, now, &buf)

	output := buf.String()
	t.Logf("output:\n%s", output)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(output, "metrics error: no data") {
		t.Errorf("expected 'metrics error: no data', got:\n%s", output)
	}
	if !strings.Contains(output, "Verdict: unknown") {
		t.Errorf("expected 'Verdict: unknown', got:\n%s", output)
	}
}

// TestRun_MetricsStaleError verifies ErrStale is printed plainly.
func TestRun_MetricsStaleError(t *testing.T) {
	fk := metrics.NewFake()
	fk.SeriesErr = metrics.ErrStale

	var buf bytes.Buffer
	now := liveBase.Add(30 * liveStep)

	_ = run(fk, "http://unused", "demo", "checkout", "db", "fakedb", "",
		30*liveStep, liveStep, now, &buf)

	output := buf.String()
	if !strings.Contains(output, "metrics error: stale data") {
		t.Errorf("expected 'metrics error: stale data', got:\n%s", output)
	}
}

// TestDetectScaleUp exercises the scale-up detection logic directly.
func TestDetectScaleUp(t *testing.T) {
	// No scale-up: flat series
	var flat []detector.Sample
	for i := 0; i < 10; i++ {
		flat = append(flat, detector.Sample{T: liveBase.Add(time.Duration(i) * liveStep), Replicas: 3})
	}
	_, _, _, found := detectScaleUp(flat)
	if found {
		t.Error("expected no scale-up in flat series")
	}

	// Scale-up at index 5, stays for 3+ samples
	var rising []detector.Sample
	for i := 0; i < 10; i++ {
		r := 3
		if i >= 5 {
			r = 10
		}
		rising = append(rising, detector.Sample{T: liveBase.Add(time.Duration(i) * liveStep), Replicas: r})
	}
	idx, from, to, found := detectScaleUp(rising)
	if !found {
		t.Fatal("expected scale-up in rising series")
	}
	if from != 3 || to != 10 {
		t.Errorf("expected from=3 to=10, got from=%d to=%d", from, to)
	}
	if idx != 5 {
		t.Errorf("expected scale-up at idx=5, got %d", idx)
	}

	// Scale-up with only 2 sustained samples (should NOT be detected)
	var shortRise []detector.Sample
	reps := []int{3, 3, 3, 3, 10, 10, 3, 3, 3, 3}
	for i, r := range reps {
		shortRise = append(shortRise, detector.Sample{T: liveBase.Add(time.Duration(i) * liveStep), Replicas: r})
	}
	_, _, _, found = detectScaleUp(shortRise)
	if found {
		t.Error("expected no scale-up when sustained < 3 samples")
	}
}

// TestWriteCSV verifies that the CSV is written with the correct columns.
func TestWriteCSV(t *testing.T) {
	series := []detector.Sample{
		{T: liveBase, Replicas: 3, Ready: 3, RPS: 100.0, P99Ms: 150.0, ErrRate: 0.01},
		{T: liveBase.Add(liveStep), Replicas: 10, Ready: 10, RPS: 120.0, P99Ms: 200.0, ErrRate: 0.01},
	}
	depSeries := []detector.DepSample{
		{T: liveBase, Saturation: 0.5, LatencyMs: 80.0},
	}

	f, err := os.CreateTemp(t.TempDir(), "test_*.csv")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := writeCSV(f.Name(), series, depSeries); err != nil {
		t.Fatalf("writeCSV failed: %v", err)
	}

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "timestamp") {
		t.Errorf("CSV missing header row")
	}
	if !strings.Contains(text, "150.000") {
		t.Errorf("CSV missing p99 value")
	}
	if !strings.Contains(text, "0.500000") {
		t.Errorf("CSV missing dep saturation value")
	}
}

// TestMetricsConfig ensures config placeholders expand correctly.
func TestMetricsConfig(t *testing.T) {
	cfg := metricsConfig("http://localhost:9090", 5*time.Second)
	if cfg.BaseURL != "http://localhost:9090" {
		t.Errorf("unexpected BaseURL: %q", cfg.BaseURL)
	}
	if !strings.Contains(cfg.RPS, "{{namespace}}") {
		t.Errorf("RPS query missing {{namespace}} placeholder: %s", cfg.RPS)
	}
	if !strings.Contains(cfg.RPS, "{{name}}") {
		t.Errorf("RPS query missing {{name}} placeholder: %s", cfg.RPS)
	}
	if !strings.Contains(cfg.Saturation, "{{dep}}") {
		t.Errorf("Saturation query missing {{dep}} placeholder: %s", cfg.Saturation)
	}
	// rate window should be at least 30s (4*5s = 20s → clamped to 30s)
	if strings.Contains(cfg.RPS, "20s") {
		t.Errorf("rate window should be at least 30s, got 20s in: %s", cfg.RPS)
	}
	if !strings.Contains(cfg.RPS, "30s") {
		t.Errorf("expected 30s rate window for 5s step, got: %s", cfg.RPS)
	}
}

// TestFmtDuration checks the duration formatter.
func TestFmtDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{1 * time.Minute, "1m"},
		{2 * time.Minute, "2m"},
		{90 * time.Second, "90s"},
	}
	for _, tc := range cases {
		got := fmtDuration(tc.in)
		if got != tc.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
