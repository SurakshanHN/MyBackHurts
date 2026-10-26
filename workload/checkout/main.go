package main

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// BurnCPU consumes CPU cycles continuously for approximately duration d.
func BurnCPU(d time.Duration) {
	start := time.Now()
	for time.Since(start) < d {
		for i := 0; i < 500; i++ {
			_ = math.Sqrt(12345.6789)
		}
	}
}

// ParseCPUBurnMS parses the CPU_BURN_MS environment variable, defaulting to 5ms on empty or invalid input.
func ParseCPUBurnMS(val string) time.Duration {
	const defaultBurn = 5 * time.Millisecond
	if val == "" {
		return defaultBurn
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		log.Printf("warning: invalid CPU_BURN_MS %q, falling back to 5ms", val)
		return defaultBurn
	}
	return time.Duration(n) * time.Millisecond
}

// ParseStartupDelay parses the STARTUP_DELAY_SECONDS environment variable, defaulting to 0s on empty or invalid input.
func ParseStartupDelay(val string) time.Duration {
	if val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		log.Printf("warning: invalid STARTUP_DELAY_SECONDS %q, falling back to 0s", val)
		return 0
	}
	return time.Duration(n) * time.Second
}

// CheckoutServer manages checkout requests, downstream DB calls, and Prometheus metrics.
type CheckoutServer struct {
	dbURL         string
	connsSem      chan struct{}
	httpClient    *http.Client
	reg           *prometheus.Registry
	requestsTotal *prometheus.CounterVec
	durationHisto prometheus.Histogram

	cpuBurnDuration time.Duration
	startupDelay    time.Duration
	startTime       time.Time
	nowFn           func() time.Time
	readyLogged     atomic.Bool
}

// NewCheckoutServer creates and initializes a CheckoutServer with defaults.
func NewCheckoutServer(dbURL string, connsPerPod int) *CheckoutServer {
	return NewCheckoutServerConfig(dbURL, connsPerPod, 5*time.Millisecond, 0, time.Now())
}

// NewCheckoutServerConfig creates a CheckoutServer with explicit cpuBurn and startupDelay settings.
func NewCheckoutServerConfig(dbURL string, connsPerPod int, cpuBurn time.Duration, startupDelay time.Duration, start time.Time) *CheckoutServer {
	if dbURL == "" {
		dbURL = "http://localhost:8080"
	}
	if connsPerPod <= 0 {
		connsPerPod = 20
	}
	if cpuBurn <= 0 {
		cpuBurn = 5 * time.Millisecond
	}

	reg := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests processed by status code",
	}, []string{"code"})

	durationHisto := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds",
		Buckets: []float64{0.005, 0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.0, 10.0},
	})

	reg.MustRegister(requestsTotal, durationHisto)

	return &CheckoutServer{
		dbURL:           dbURL,
		connsSem:        make(chan struct{}, connsPerPod),
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		reg:             reg,
		requestsTotal:   requestsTotal,
		durationHisto:   durationHisto,
		cpuBurnDuration: cpuBurn,
		startupDelay:    startupDelay,
		startTime:       start,
		nowFn:           time.Now,
	}
}

// CheckoutHandler processes a checkout request by acquiring a connection slot,
// invoking fakedb /query, performing CPU work, and recording metrics.
func (s *CheckoutServer) CheckoutHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Acquire per-pod concurrency slot
	select {
	case s.connsSem <- struct{}{}:
		defer func() { <-s.connsSem }()
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusGatewayTimeout)
		s.recordMetrics(http.StatusGatewayTimeout, start)
		return
	}

	// 1. Call downstream fakedb /query once
	dbReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.dbURL+"/query", nil)
	if err != nil {
		http.Error(w, "failed to create db request", http.StatusInternalServerError)
		s.recordMetrics(http.StatusInternalServerError, start)
		return
	}

	dbResp, err := s.httpClient.Do(dbReq)
	if err != nil {
		http.Error(w, "failed to query database", http.StatusBadGateway)
		s.recordMetrics(http.StatusBadGateway, start)
		return
	}
	defer dbResp.Body.Close()
	_, _ = io.Copy(io.Discard, dbResp.Body)

	// If database returned non-200 (e.g. 503 over-limit), propagate it
	if dbResp.StatusCode != http.StatusOK {
		http.Error(w, "database error", dbResp.StatusCode)
		s.recordMetrics(dbResp.StatusCode, start)
		return
	}

	// 2. Perform configurable CPU work
	BurnCPU(s.cpuBurnDuration)

	// 3. Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "checkout complete",
	})

	s.recordMetrics(http.StatusOK, start)
}

func (s *CheckoutServer) recordMetrics(code int, start time.Time) {
	duration := time.Since(start).Seconds()
	s.requestsTotal.WithLabelValues(strconv.Itoa(code)).Inc()
	s.durationHisto.Observe(duration)
}

func (s *CheckoutServer) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// HealthzHandler handles readiness probes. Returns 503 during startup delay, then 200.
func (s *CheckoutServer) HealthzHandler(w http.ResponseWriter, r *http.Request) {
	if s.startupDelay > 0 && s.now().Sub(s.startTime) < s.startupDelay {
		http.Error(w, "starting up", http.StatusServiceUnavailable)
		return
	}
	if s.readyLogged.CompareAndSwap(false, true) {
		log.Printf("Startup delay elapsed; checkout service is now ready")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// LivezHandler handles liveness probes. Always returns 200 OK.
func (s *CheckoutServer) LivezHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Routes configures all endpoints.
func (s *CheckoutServer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.CheckoutHandler)
	mux.HandleFunc("/healthz", s.HealthzHandler)
	mux.HandleFunc("/livez", s.LivezHandler)
	mux.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	return mux
}

func main() {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		dbURL = "http://localhost:8080"
	}

	connsPerPod := 20
	if envConns := os.Getenv("CONNS_PER_POD"); envConns != "" {
		if v, err := strconv.Atoi(envConns); err == nil && v > 0 {
			connsPerPod = v
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cpuBurn := ParseCPUBurnMS(os.Getenv("CPU_BURN_MS"))
	startupDelay := ParseStartupDelay(os.Getenv("STARTUP_DELAY_SECONDS"))

	srv := NewCheckoutServerConfig(dbURL, connsPerPod, cpuBurn, startupDelay, time.Now())

	addr := ":" + port
	log.Printf("Starting checkout service on %s (DB_URL=%s, CONNS_PER_POD=%d, CPU_BURN=%v, STARTUP_DELAY=%v)",
		addr, dbURL, connsPerPod, cpuBurn, startupDelay)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil && err != http.ErrServerClosed {
		log.Fatalf("checkout service failed: %v", err)
	}
}
