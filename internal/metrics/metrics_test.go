package metrics_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prescale/prescale/internal/metrics"
)

// ---- helpers ---------------------------------------------------------------

// mtConfig builds a Config pointing at baseURL with simple PromQL templates.
func mtConfig(baseURL string) metrics.Config {
	return metrics.Config{
		BaseURL:    baseURL,
		RPS:        `rate(http_requests_total{namespace="{{namespace}}",app="{{name}}"}[{{step}}])`,
		P99Ms:      `histogram_quantile(0.99, rate(http_request_duration_bucket{namespace="{{namespace}}",app="{{name}}"}[{{step}}]))`,
		ErrRate:    `rate(http_errors_total{namespace="{{namespace}}",app="{{name}}"}[{{step}}])`,
		Replicas:   `kube_deployment_replicas{namespace="{{namespace}}",deployment="{{name}}"}`,
		Ready:      `kube_deployment_replicas_ready{namespace="{{namespace}}",deployment="{{name}}"}`,
		Saturation: `rate(dep_saturation{namespace="{{namespace}}",dep="{{dep}}"}[{{step}}])`,
		LatencyMs:  `rate(dep_latency_ms{namespace="{{namespace}}",dep="{{dep}}"}[{{step}}])`,
	}
}

// mtTarget is a reusable valid target.
var mtTarget = metrics.Target{Namespace: "default", Name: "my-svc"}

// mtPromResp builds a minimal Prometheus success response with one time-series.
func mtPromResp(tsSec int64, val string) string {
	type point [2]interface{}
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []point           `json:"values"`
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type resp struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}
	r := resp{
		Status: "success",
		Data: data{
			ResultType: "matrix",
			Result: []result{{
				Metric: map[string]string{},
				Values: []point{{tsSec, val}},
			}},
		},
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// mtAllMetricsHandler creates an HTTP handler that returns `val` for every query
// at timestamp `tsSec`.
func mtAllMetricsHandler(tsSec int64, val string) http.HandlerFunc {
	body := mtPromResp(tsSec, val)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

// mtNearTo returns a timestamp close enough to `to` to not be stale.
func mtNearTo(to time.Time, step time.Duration) int64 {
	return to.Add(-step / 2).Unix()
}

// ---- good data test --------------------------------------------------------

func TestPrometheus_GoodData(t *testing.T) {
	to := time.Now().UTC().Truncate(time.Minute)
	step := time.Minute
	ts := mtNearTo(to, step)

	srv := httptest.NewServer(mtAllMetricsHandler(ts, "1.5"))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	from := to.Add(-5 * step)

	samples, err := p.Series(context.Background(), mtTarget, from, to, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("expected at least one sample")
	}
	s := samples[0]
	if s.RPS != 1.5 || s.P99Ms != 1.5 || s.ErrRate != 1.5 || s.Replicas != 2 || s.Ready != 2 {
		t.Errorf("unexpected sample values: %+v", s)
	}
}

func TestPrometheus_DepSeries_GoodData(t *testing.T) {
	to := time.Now().UTC().Truncate(time.Minute)
	step := time.Minute
	ts := mtNearTo(to, step)

	srv := httptest.NewServer(mtAllMetricsHandler(ts, "0.75"))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	from := to.Add(-5 * step)

	samples, err := p.DepSeries(context.Background(), mtTarget, metrics.DepRef{Name: "redis"}, from, to, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("expected at least one sample")
	}
	ds := samples[0]
	if ds.Saturation != 0.75 || ds.LatencyMs != 0.75 {
		t.Errorf("unexpected dep sample values: %+v", ds)
	}
}

// ---- HTTP 500 --------------------------------------------------------------

func TestPrometheus_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	to := time.Now().UTC()
	_, err := p.Series(context.Background(), mtTarget, to.Add(-time.Minute), to, time.Minute)
	if err == nil || !mtContainsStr(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 error, got %v", err)
	}
}

// ---- status != success -----------------------------------------------------

func TestPrometheus_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"error","error":"bad_data","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	to := time.Now().UTC()
	_, err := p.Series(context.Background(), mtTarget, to.Add(-time.Minute), to, time.Minute)
	if err == nil || !mtContainsStr(err.Error(), "error") {
		t.Errorf("expected status error, got %v", err)
	}
}

// ---- malformed JSON --------------------------------------------------------

func TestPrometheus_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	to := time.Now().UTC()
	_, err := p.Series(context.Background(), mtTarget, to.Add(-time.Minute), to, time.Minute)
	if err == nil {
		t.Error("expected malformed JSON error, got nil")
	}
}

// ---- NaN points dropped but < 20% -----------------------------------------

func TestPrometheus_NaNPointsDropped(t *testing.T) {
	to := time.Now().UTC().Truncate(time.Minute)
	step := time.Minute
	ts := mtNearTo(to, step)

	// Build a response with 10 good points and 1 NaN – 1/11 ≈ 9% → OK
	type point [2]interface{}
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []point           `json:"values"`
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type resp struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}
	var vals []point
	for i := 0; i < 10; i++ {
		vals = append(vals, point{ts - int64(i)*60, "1.0"})
	}
	vals = append(vals, point{ts - 10*60, "NaN"})
	r := resp{
		Status: "success",
		Data: data{
			ResultType: "matrix",
			Result:     []result{{Metric: map[string]string{}, Values: vals}},
		},
	}
	body, _ := json.Marshal(r)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	from := to.Add(-20 * step)
	_, err := p.Series(context.Background(), mtTarget, from, to, step)
	if err != nil {
		t.Errorf("9%% NaN drop should not error, got %v", err)
	}
}

// ---- >20% NaN → ErrNoData -------------------------------------------------

func TestPrometheus_TooManyDropped(t *testing.T) {
	// Return 3 NaN, 7 good → 30% > 20%
	type point [2]interface{}
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []point           `json:"values"`
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type resp struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}
	to := time.Now().UTC().Truncate(time.Minute)
	ts := mtNearTo(to, time.Minute)
	var vals []point
	for i := 0; i < 7; i++ {
		vals = append(vals, point{ts - int64(i)*60, "1.0"})
	}
	for i := 0; i < 3; i++ {
		vals = append(vals, point{ts - int64(7+i)*60, "NaN"})
	}
	r := resp{Status: "success", Data: data{ResultType: "matrix", Result: []result{{Metric: map[string]string{}, Values: vals}}}}
	body, _ := json.Marshal(r)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	_, err := p.Series(context.Background(), mtTarget, to.Add(-20*time.Minute), to, time.Minute)
	if !errors.Is(err, metrics.ErrNoData) {
		t.Errorf("expected ErrNoData for >20%% drop, got %v", err)
	}
}

// ---- stale data ------------------------------------------------------------

func TestPrometheus_Stale(t *testing.T) {
	to := time.Now().UTC().Truncate(time.Minute)
	step := time.Minute
	// Newest point is 3 steps before `to` → stale (need to be within 2 steps)
	ts := to.Add(-3 * step).Unix()

	srv := httptest.NewServer(mtAllMetricsHandler(ts, "1.0"))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	_, err := p.Series(context.Background(), mtTarget, to.Add(-10*step), to, step)
	if !errors.Is(err, metrics.ErrStale) {
		t.Errorf("expected ErrStale, got %v", err)
	}
}

// ---- empty result ----------------------------------------------------------

func TestPrometheus_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()

	p := metrics.NewPrometheus(mtConfig(srv.URL))
	to := time.Now().UTC()
	_, err := p.Series(context.Background(), mtTarget, to.Add(-time.Minute), to, time.Minute)
	if !errors.Is(err, metrics.ErrNoData) {
		t.Errorf("expected ErrNoData for empty result, got %v", err)
	}
}

// ---- invalid labels --------------------------------------------------------

func TestPrometheus_InvalidLabel(t *testing.T) {
	p := metrics.NewPrometheus(mtConfig("http://localhost:9090"))
	to := time.Now().UTC()

	bad := []metrics.Target{
		{Namespace: "", Name: "svc"},
		{Namespace: "ns", Name: ""},
		{Namespace: "UPPER", Name: "svc"},
		{Namespace: "ns", Name: "UPPER"},
		{Namespace: "ns-", Name: "svc"}, // trailing hyphen
		{Namespace: "-ns", Name: "svc"}, // leading hyphen
	}
	for _, t2 := range bad {
		_, err := p.Series(context.Background(), t2, to.Add(-time.Minute), to, time.Minute)
		if err == nil {
			t.Errorf("expected error for target %+v", t2)
		}
	}
}

// ---- timeout ---------------------------------------------------------------

func TestPrometheus_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	cfg := mtConfig(srv.URL)
	cfg.HTTPClient = &http.Client{Timeout: 10 * time.Millisecond}
	p := metrics.NewPrometheus(cfg)
	to := time.Now().UTC()
	_, err := p.Series(context.Background(), mtTarget, to.Add(-time.Minute), to, time.Minute)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// ---- Fake: range filtering and copy isolation ------------------------------

func TestFake_RangeAndCopies(t *testing.T) {
	f := metrics.NewFake()
	tgt := metrics.Target{Namespace: "ns", Name: "svc"}
	dep := metrics.DepRef{Name: "db"}

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	origSamples := []metrics.Sample{
		{T: t0.Add(0 * time.Minute), RPS: 10, P99Ms: 1, ErrRate: 0, Replicas: 2, Ready: 2},
		{T: t0.Add(1 * time.Minute), RPS: 20, P99Ms: 2, ErrRate: 0, Replicas: 2, Ready: 2},
		{T: t0.Add(2 * time.Minute), RPS: 30, P99Ms: 3, ErrRate: 0, Replicas: 2, Ready: 2},
	}
	origDep := []metrics.DepSample{
		{T: t0.Add(0 * time.Minute), Saturation: 0.1, LatencyMs: 5},
		{T: t0.Add(1 * time.Minute), Saturation: 0.2, LatencyMs: 6},
		{T: t0.Add(2 * time.Minute), Saturation: 0.3, LatencyMs: 7},
	}

	f.SetSeries(tgt, origSamples)
	f.SetDepSeries(tgt, dep, origDep)

	// Mutate originals after Set → should not affect stored values
	origSamples[0].RPS = 999
	origDep[0].Saturation = 999

	ctx := context.Background()

	// Range [t0+30s, t0+90s] → only the middle point (t0+1m) falls in range
	from := t0.Add(30 * time.Second)
	to := t0.Add(90 * time.Second)

	got, err := f.Series(ctx, tgt, from, to, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample in range, got %d", len(got))
	}
	if got[0].RPS != 20 {
		t.Errorf("wrong sample returned or mutation leaked: RPS=%v", got[0].RPS)
	}

	// Mutate return value → must not affect subsequent calls
	got[0].RPS = 0
	got2, _ := f.Series(ctx, tgt, from, to, time.Minute)
	if got2[0].RPS != 20 {
		t.Errorf("returned slice is not a copy: RPS=%v", got2[0].RPS)
	}

	gotDep, err := f.DepSeries(ctx, tgt, dep, from, to, time.Minute)
	if err != nil {
		t.Fatalf("unexpected dep error: %v", err)
	}
	if len(gotDep) != 1 || gotDep[0].Saturation != 0.2 {
		t.Errorf("unexpected dep samples: %+v", gotDep)
	}
}

func TestFake_InjectErrors(t *testing.T) {
	f := metrics.NewFake()
	injErr := errors.New("injected")
	f.SeriesErr = injErr
	f.DepSeriesErr = injErr
	tgt := metrics.Target{Namespace: "ns", Name: "svc"}
	dep := metrics.DepRef{Name: "d"}
	to := time.Now()
	from := to.Add(-time.Minute)
	step := time.Minute

	if _, err := f.Series(context.Background(), tgt, from, to, step); !errors.Is(err, injErr) {
		t.Errorf("expected injected Series error, got %v", err)
	}
	if _, err := f.DepSeries(context.Background(), tgt, dep, from, to, step); !errors.Is(err, injErr) {
		t.Errorf("expected injected DepSeries error, got %v", err)
	}
}

// ---- helper ----------------------------------------------------------------

func mtContainsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}
