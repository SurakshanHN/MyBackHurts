package predictor_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prescale/prescale/internal/predictor"
)

func startFakePredictor(t *testing.T, predictedRPS float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/predict" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]float64{"predicted_rps": predictedRPS})
	}))
}

func TestPredict_ok(t *testing.T) {
	srv := startFakePredictor(t, 100.0)
	defer srv.Close()

	c := predictor.New(srv.URL)
	got, err := c.Predict(context.Background(), []float64{80, 90, 100})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if math.Abs(got-100.0) > 0.001 {
		t.Errorf("expected 100.0, got %f", got)
	}
}

func TestPredict_non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := predictor.New(srv.URL)
	_, err := c.Predict(context.Background(), []float64{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for non-200")
	}
}

func TestReplicasFor(t *testing.T) {
	cases := []struct {
		predictedRPS float64
		conns        int
		headroom     float64
		min, max     int32
		want         int32
	}{
		{100, 20, 1.20, 3, 10, 6},  // ceil(100*1.2/20) = ceil(6) = 6
		{10, 20, 1.20, 3, 10, 3},   // ceil(10*1.2/20) = ceil(0.6) = 1 → clamped to min=3
		{500, 20, 1.20, 3, 10, 10}, // ceil(500*1.2/20) = 30 → clamped to max=10
		{40, 20, 1.20, 3, 10, 3},   // ceil(40*1.2/20) = ceil(2.4) = 3 = min
	}
	for _, tc := range cases {
		c := &predictor.Client{ConnsPerPod: tc.conns, Headroom: tc.headroom}
		got := c.ReplicasFor(tc.predictedRPS, tc.min, tc.max)
		if got != tc.want {
			t.Errorf("ReplicasFor(%.0f, conns=%d, h=%.2f, [%d,%d]) = %d, want %d",
				tc.predictedRPS, tc.conns, tc.headroom, tc.min, tc.max, got, tc.want)
		}
	}
}
