package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCalcLatency(t *testing.T) {
	// Normal mode: flat 10ms
	for _, q := range []int{0, 1, 10, 20, 50, 100} {
		lat := CalcLatency("normal", q)
		if lat != 10*time.Millisecond {
			t.Errorf("normal mode with %d queries: expected 10ms, got %v", q, lat)
		}
	}

	// Contention mode: 10ms * (1 + q/20)^2
	cases := []struct {
		activeQueries int
		expectedMs    float64
	}{
		{activeQueries: 0, expectedMs: 10.0},                  // 10 * 1^2 = 10
		{activeQueries: 20, expectedMs: 40.0},                 // 10 * (1 + 1)^2 = 40
		{activeQueries: 40, expectedMs: 90.0},                 // 10 * (1 + 2)^2 = 90
		{activeQueries: 60, expectedMs: 160.0},                // 10 * (1 + 3)^2 = 160
		{activeQueries: 80, expectedMs: 250.0},                // 10 * (1 + 4)^2 = 250
		{activeQueries: 100, expectedMs: 360.0},               // 10 * (1 + 5)^2 = 360
	}

	for _, tc := range cases {
		lat := CalcLatency("contention", tc.activeQueries)
		expected := time.Duration(tc.expectedMs * float64(time.Millisecond))
		if lat != expected {
			t.Errorf("contention mode with %d queries: expected %v, got %v", tc.activeQueries, expected, lat)
		}
	}
}

func TestOverLimit503(t *testing.T) {
	// Create server with max 2 connections
	maxConns := int64(2)
	srv := NewDBServer(maxConns, "normal")
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	var wg sync.WaitGroup
	started := make(chan struct{}, 2)
	blockQuery := make(chan struct{})

	// Custom handler wrapper to artificially hold connections
	holdHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to acquire slot on srv
		for {
			cur := srv.active.Load()
			if cur >= srv.maxConns {
				http.Error(w, "too many connections", http.StatusServiceUnavailable)
				return
			}
			if srv.active.CompareAndSwap(cur, cur+1) {
				break
			}
		}
		defer srv.active.Add(-1)

		started <- struct{}{}
		<-blockQuery
		w.WriteHeader(http.StatusOK)
	})

	testMux := http.NewServeMux()
	testMux.Handle("/query", holdHandler)
	testMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		srv.Routes().ServeHTTP(w, r)
	})
	ts2 := httptest.NewServer(testMux)
	defer ts2.Close()

	// Launch 2 queries to saturate all connections
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts2.URL+"/query", "application/json", nil)
			if err != nil {
				t.Errorf("post error: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}

	// Wait until both queries hold slots
	<-started
	<-started

	// Now active is 2 == maxConns. Third query must receive 503 Service Unavailable immediately.
	resp, err := http.Post(ts2.URL+"/query", "application/json", nil)
	if err != nil {
		t.Fatalf("third request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 for over-limit query, got %d", resp.StatusCode)
	}

	// Release blocked queries
	close(blockQuery)
	wg.Wait()

	// After release, new query should succeed
	respAfter, err := http.Post(ts.URL+"/query", "application/json", nil)
	if err != nil {
		t.Fatalf("request after release failed: %v", err)
	}
	defer respAfter.Body.Close()

	if respAfter.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 after release, got %d", respAfter.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := NewDBServer(50, "normal")
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to scrape metrics: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read metrics body: %v", err)
	}

	text := string(body)
	for _, metricName := range []string{"db_active_connections", "db_max_connections", "db_query_duration_seconds"} {
		if !strings.Contains(text, metricName) {
			t.Errorf("metrics output missing %q:\n%s", metricName, text)
		}
	}

	if !strings.Contains(text, "db_max_connections 50") {
		t.Errorf("expected db_max_connections 50, got:\n%s", text)
	}
}
