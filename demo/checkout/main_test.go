package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBurnCPU(t *testing.T) {
	start := time.Now()
	BurnCPU(5 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 5*time.Millisecond {
		t.Errorf("expected BurnCPU to take at least 5ms, took %v", elapsed)
	}
}

func TestCheckoutHandler_Success(t *testing.T) {
	// Mock fakedb server returning 200 OK
	fakeDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" || r.Method != http.MethodPost {
			t.Errorf("unexpected db request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer fakeDB.Close()

	srv := NewCheckoutServer(fakeDB.URL, 10)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/checkout")
	if err != nil {
		t.Fatalf("checkout request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify metrics
	mResp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics request failed: %v", err)
	}
	defer mResp.Body.Close()

	body, _ := io.ReadAll(mResp.Body)
	text := string(body)
	if !strings.Contains(text, `http_requests_total{code="200"} 1`) {
		t.Errorf("expected http_requests_total with code 200, got:\n%s", text)
	}
	if !strings.Contains(text, "http_request_duration_seconds") {
		t.Errorf("expected http_request_duration_seconds in metrics")
	}
}

func TestCheckoutHandler_Downstream503(t *testing.T) {
	// Mock fakedb returning 503
	fakeDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
	}))
	defer fakeDB.Close()

	srv := NewCheckoutServer(fakeDB.URL, 10)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/checkout")
	if err != nil {
		t.Fatalf("checkout request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected propagated status 503, got %d", resp.StatusCode)
	}
}
