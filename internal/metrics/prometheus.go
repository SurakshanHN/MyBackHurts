package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prescale/prescale/internal/detector"
)

// labelRe is the Kubernetes label-value pattern that namespace and name must match.
var labelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func validateLabel(v string) error {
	if !labelRe.MatchString(v) {
		return fmt.Errorf("metrics: label %q is not a valid DNS label", v)
	}
	return nil
}

// Config holds the Prometheus base URL and one PromQL template per metric.
// Placeholders {{namespace}}, {{name}}, {{dep}}, {{step}} are replaced at query time.
type Config struct {
	BaseURL    string
	RPS        string
	P99Ms      string
	ErrRate    string
	Replicas   string
	Ready      string
	Saturation string
	LatencyMs  string

	// HTTPClient allows tests to inject a custom *http.Client (e.g. with a tiny timeout).
	// If nil the Prometheus constructor sets a 10 s default.
	HTTPClient *http.Client
}

func (cfg Config) expand(tmpl, ns, name, dep string, step time.Duration) string {
	r := strings.NewReplacer(
		"{{namespace}}", ns,
		"{{name}}", name,
		"{{dep}}", dep,
		"{{step}}", fmtStep(step),
	)
	return r.Replace(tmpl)
}

func fmtStep(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// Prometheus is a Source that queries Prometheus via /api/v1/query_range.
type Prometheus struct {
	cfg    Config
	client *http.Client
}

// NewPrometheus constructs a Prometheus Source using cfg.
// A 10 s HTTP timeout is applied unless cfg.HTTPClient is provided.
func NewPrometheus(cfg Config) *Prometheus {
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	return &Prometheus{cfg: cfg, client: c}
}

// --- Prometheus wire types --------------------------------------------------

type promResponse struct {
	Status string   `json:"status"`
	Error  string   `json:"error,omitempty"`
	Data   promData `json:"data"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promSeries `json:"result"`
}

type promSeries struct {
	Metric map[string]string `json:"metric"`
	Values []promPoint       `json:"values"`
}

// promPoint is a [timestamp, value] pair from Prometheus.
// We decode it manually from a mixed-type JSON array.
type promPoint struct {
	T   float64
	Val string // raw string form; NaN/Inf detected here
}

func (p *promPoint) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || len(raw) != 2 {
		return fmt.Errorf("metrics: unexpected point shape %s", b)
	}
	if err := json.Unmarshal(raw[0], &p.T); err != nil {
		return fmt.Errorf("metrics: bad timestamp: %w", err)
	}
	return json.Unmarshal(raw[1], &p.Val)
}

// --- query helper -----------------------------------------------------------

// queryRange fetches a single PromQL query and returns a map of unix-second → float64.
// Drops NaN/Inf values; returns (map, totalPoints, droppedPoints, error).
func (p *Prometheus) queryRange(ctx context.Context, query string, from, to time.Time, step time.Duration) (map[int64]float64, int, int, error) {
	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/api/v1/query_range"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	q := req.URL.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(from.Unix(), 10))
	q.Set("end", strconv.FormatInt(to.Unix(), 10))
	q.Set("step", fmtStep(step))
	req.URL.RawQuery = q.Encode()

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, 0, fmt.Errorf("metrics: prometheus HTTP %d", resp.StatusCode)
	}

	var pr promResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, 0, 0, fmt.Errorf("metrics: malformed JSON from prometheus: %w", err)
	}
	if pr.Status != "success" {
		return nil, 0, 0, fmt.Errorf("metrics: prometheus status=%q error=%q", pr.Status, pr.Error)
	}

	out := make(map[int64]float64)
	total, dropped := 0, 0
	for _, series := range pr.Data.Result {
		for _, pt := range series.Values {
			total++
			ts := int64(math.Round(pt.T))
			upper := strings.ToUpper(strings.TrimSpace(pt.Val))
			if upper == "NAN" || upper == "+INF" || upper == "-INF" || upper == "INF" {
				dropped++
				continue
			}
			f, err := strconv.ParseFloat(pt.Val, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				dropped++
				continue
			}
			out[ts] = f
		}
	}
	return out, total, dropped, nil
}

// --- Series -----------------------------------------------------------------

// Series fetches the five service metrics, aligns them by timestamp, and
// returns samples for timestamps where all five are present.
func (p *Prometheus) Series(ctx context.Context, t Target, from, to time.Time, step time.Duration) ([]detector.Sample, error) {
	if err := validateLabel(t.Namespace); err != nil {
		return nil, err
	}
	if err := validateLabel(t.Name); err != nil {
		return nil, err
	}

	ns, name := t.Namespace, t.Name
	type fetched struct {
		m       map[int64]float64
		total   int
		dropped int
	}
	fetch := func(tmpl string) (fetched, error) {
		m, total, dropped, err := p.queryRange(ctx, p.cfg.expand(tmpl, ns, name, "", step), from, to, step)
		return fetched{m, total, dropped}, err
	}

	rpsF, err := fetch(p.cfg.RPS)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(rpsF.total, rpsF.dropped); err != nil {
		return nil, err
	}

	p99F, err := fetch(p.cfg.P99Ms)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(p99F.total, p99F.dropped); err != nil {
		return nil, err
	}

	errF, err := fetch(p.cfg.ErrRate)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(errF.total, errF.dropped); err != nil {
		return nil, err
	}

	repF, err := fetch(p.cfg.Replicas)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(repF.total, repF.dropped); err != nil {
		return nil, err
	}

	readyF, err := fetch(p.cfg.Ready)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(readyF.total, readyF.dropped); err != nil {
		return nil, err
	}

	// Collect union of timestamps from rpsF (primary), sorted.
	tsList := sortedKeys(rpsF.m)
	if len(tsList) == 0 {
		return nil, ErrNoData
	}

	var samples []detector.Sample
	for _, ts := range tsList {
		rps, ok1 := rpsF.m[ts]
		p99, ok2 := p99F.m[ts]
		er, ok3 := errF.m[ts]
		rep, ok4 := repF.m[ts]
		rdy, ok5 := readyF.m[ts]
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			continue // skip timestamps missing any service metric
		}
		samples = append(samples, detector.Sample{
			T:        time.Unix(ts, 0).UTC(),
			RPS:      rps,
			P99Ms:    p99,
			ErrRate:  er,
			Replicas: int(math.Round(rep)),
			Ready:    int(math.Round(rdy)),
		})
	}
	if len(samples) == 0 {
		return nil, ErrNoData
	}
	if err := checkStale(samples[len(samples)-1].T, to, step); err != nil {
		return nil, err
	}
	return samples, nil
}

// --- DepSeries --------------------------------------------------------------

// DepSeries fetches saturation and latency_ms, aligns them, and returns dep samples.
func (p *Prometheus) DepSeries(ctx context.Context, t Target, d DepRef, from, to time.Time, step time.Duration) ([]detector.DepSample, error) {
	if err := validateLabel(t.Namespace); err != nil {
		return nil, err
	}
	if err := validateLabel(t.Name); err != nil {
		return nil, err
	}
	if err := validateLabel(d.Name); err != nil {
		return nil, err
	}

	ns, name, dep := t.Namespace, t.Name, d.Name

	satM, satTotal, satDrop, err := p.queryRange(ctx, p.cfg.expand(p.cfg.Saturation, ns, name, dep, step), from, to, step)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(satTotal, satDrop); err != nil {
		return nil, err
	}

	latM, latTotal, latDrop, err := p.queryRange(ctx, p.cfg.expand(p.cfg.LatencyMs, ns, name, dep, step), from, to, step)
	if err != nil {
		return nil, err
	}
	if err := checkDropRate(latTotal, latDrop); err != nil {
		return nil, err
	}

	tsList := sortedKeys(satM)
	if len(tsList) == 0 {
		return nil, ErrNoData
	}

	var samples []detector.DepSample
	for _, ts := range tsList {
		sat, ok1 := satM[ts]
		lat, ok2 := latM[ts]
		if !ok1 || !ok2 {
			continue
		}
		samples = append(samples, detector.DepSample{
			T:          time.Unix(ts, 0).UTC(),
			Saturation: sat,
			LatencyMs:  lat,
		})
	}
	if len(samples) == 0 {
		return nil, ErrNoData
	}
	if err := checkStale(samples[len(samples)-1].T, to, step); err != nil {
		return nil, err
	}
	return samples, nil
}

// --- helpers ----------------------------------------------------------------

func checkDropRate(total, dropped int) error {
	if total == 0 {
		return ErrNoData
	}
	if float64(dropped)/float64(total) > 0.20 {
		return ErrNoData
	}
	return nil
}

func checkStale(newest, to time.Time, step time.Duration) error {
	if newest.Before(to.Add(-2 * step)) {
		return ErrStale
	}
	return nil
}

func sortedKeys(m map[int64]float64) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
