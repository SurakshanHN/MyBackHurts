package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CalcLatency calculates the query duration based on mode and active queries.
// In normal mode: ~10ms flat.
// In contention mode: 10ms * (1 + active_queries/20)^2.
func CalcLatency(mode string, activeQueries int) time.Duration {
	if mode == "contention" {
		term := 1.0 + float64(activeQueries)/20.0
		ms := 10.0 * term * term
		return time.Duration(ms * float64(time.Millisecond))
	}
	return 10 * time.Millisecond
}

// DBServer manages active connections and serves simulated database queries.
type DBServer struct {
	maxConns      int64
	mode          string
	active        atomic.Int64
	reg           *prometheus.Registry
	activeGauge   prometheus.Gauge
	maxGauge      prometheus.Gauge
	durationHisto prometheus.Histogram
}

// NewDBServer creates a new DBServer with configured max connections and mode.
func NewDBServer(maxConns int64, mode string) *DBServer {
	if maxConns <= 0 {
		maxConns = 100
	}
	if mode == "" {
		mode = "normal"
	}

	reg := prometheus.NewRegistry()

	activeGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_active_connections",
		Help: "Current active database connections",
	})

	maxGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "db_max_connections",
		Help: "Maximum allowed database connections",
	})

	durationHisto := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "db_query_duration_seconds",
		Help:    "Database query duration in seconds",
		Buckets: []float64{0.005, 0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.0},
	})

	reg.MustRegister(activeGauge, maxGauge, durationHisto)
	maxGauge.Set(float64(maxConns))
	activeGauge.Set(0)

	return &DBServer{
		maxConns:      maxConns,
		mode:          mode,
		reg:           reg,
		activeGauge:   activeGauge,
		maxGauge:      maxGauge,
		durationHisto: durationHisto,
	}
}

// SetMode updates the operating mode (normal or contention).
func (s *DBServer) SetMode(mode string) {
	s.mode = mode
}

// GetMode returns the current operating mode.
func (s *DBServer) GetMode() string {
	return s.mode
}

// QueryHandler handles POST /query. Holds one connection slot for query duration.
// If active queries exceed maxConns, returns 503 Service Unavailable.
func (s *DBServer) QueryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Try to acquire connection slot
	for {
		cur := s.active.Load()
		if cur >= s.maxConns {
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}
		if s.active.CompareAndSwap(cur, cur+1) {
			break
		}
	}

	newActive := s.active.Load()
	s.activeGauge.Set(float64(newActive))

	defer func() {
		remaining := s.active.Add(-1)
		s.activeGauge.Set(float64(remaining))
	}()

	start := time.Now()
	latency := CalcLatency(s.mode, int(newActive))

	select {
	case <-time.After(latency):
	case <-r.Context().Done():
		http.Error(w, "request cancelled", http.StatusGatewayTimeout)
		return
	}

	s.durationHisto.Observe(time.Since(start).Seconds())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"mode":           s.mode,
		"active_queries": newActive,
		"latency_ms":     latency.Milliseconds(),
	})
}

// ModeHandler allows checking and updating the server mode dynamically.
func (s *DBServer) ModeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		m := r.URL.Query().Get("mode")
		if m != "" {
			s.SetMode(m)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"mode": s.GetMode(),
	})
}

// HealthzHandler returns healthy status for liveness/readiness probes.
func (s *DBServer) HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Routes sets up the HTTP multiplexer.
func (s *DBServer) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/query", s.QueryHandler)
	mux.HandleFunc("/mode", s.ModeHandler)
	mux.HandleFunc("/healthz", s.HealthzHandler)
	mux.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	return mux
}

func main() {
	maxConns := int64(100)
	if envMax := os.Getenv("MAX_CONNS"); envMax != "" {
		if v, err := strconv.ParseInt(envMax, 10, 64); err == nil && v > 0 {
			maxConns = v
		}
	}

	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "normal"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := NewDBServer(maxConns, mode)

	addr := ":" + port
	log.Printf("Starting fakedb on %s (MAX_CONNS=%d, MODE=%s)", addr, maxConns, mode)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil && err != http.ErrServerClosed {
		log.Fatalf("fakedb failed: %v", err)
	}
}
