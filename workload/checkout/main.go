package main

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
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

// CheckoutServer manages checkout requests, downstream DB calls, and Prometheus metrics.
type CheckoutServer struct {
	dbURL         string
	connsSem      chan struct{}
	httpClient    *http.Client
	reg           *prometheus.Registry
	requestsTotal *prometheus.CounterVec
	durationHisto prometheus.Histogram
}

// NewCheckoutServer creates and initializes a CheckoutServer.
func NewCheckoutServer(dbURL string, connsPerPod int) *CheckoutServer {
	if dbURL == "" {
		dbURL = "http://localhost:8080"
	}
	if connsPerPod <= 0 {
		connsPerPod = 20
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
		dbURL:         dbURL,
		connsSem:      make(chan struct{}, connsPerPod),
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		reg:           reg,
		requestsTotal: requestsTotal,
		durationHisto: durationHisto,
	}
}

// CheckoutHandler processes a checkout request by acquiring a connection slot,
// invoking fakedb /query, performing ~5ms of CPU work, and recording metrics.
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

	// 2. Perform ~5ms of CPU work
	BurnCPU(5 * time.Millisecond)

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

// HealthzHandler returns 200 OK for probes.
func (s *CheckoutServer) HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Routes configures all endpoints.
func (s *CheckoutServer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.CheckoutHandler)
	mux.HandleFunc("/healthz", s.HealthzHandler)
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

	srv := NewCheckoutServer(dbURL, connsPerPod)

	addr := ":" + port
	log.Printf("Starting checkout service on %s (DB_URL=%s, CONNS_PER_POD=%d)", addr, dbURL, connsPerPod)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil && err != http.ErrServerClosed {
		log.Fatalf("checkout service failed: %v", err)
	}
}
