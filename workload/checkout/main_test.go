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

	resp, err := http.Get(ts.URL + "/")
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

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("checkout request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected propagated status 503, got %d", resp.StatusCode)
	}
}

func TestReadinessGating(t *testing.T) {
	startTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	currentTime := startTime

	srv := NewCheckoutServerConfig("http://mock", 10, 5*time.Millisecond, 30*time.Second, startTime)
	srv.nowFn = func() time.Time { return currentTime }

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// 1. Initially (before 30s elapsed) -> should return 503
	resp1, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("failed to call /healthz: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 during startup delay, got %d", resp1.StatusCode)
	}

	// 2. Advance time to 15s (still within delay)
	currentTime = startTime.Add(15 * time.Second)
	resp2, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("failed to call /healthz: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 at 15s, got %d", resp2.StatusCode)
	}

	// 3. Advance time to 30s -> should return 200
	currentTime = startTime.Add(30 * time.Second)
	resp3, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("failed to call /healthz: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after 30s elapsed, got %d", resp3.StatusCode)
	}

	// 4. Call again after ready -> should still return 200
	currentTime = startTime.Add(35 * time.Second)
	resp4, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("failed to call /healthz: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after ready, got %d", resp4.StatusCode)
	}
}

func TestLivezHandler(t *testing.T) {
	// Liveness always returns 200 even if startup delay has not elapsed
	startTime := time.Now()
	srv := NewCheckoutServerConfig("http://mock", 10, 5*time.Millisecond, 60*time.Second, startTime)

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/livez")
	if err != nil {
		t.Fatalf("failed to call /livez: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected /livez to return 200 OK, got %d", resp.StatusCode)
	}
}

func TestParseCPUBurnMS(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"", 5 * time.Millisecond},
		{"10", 10 * time.Millisecond},
		{"1", 1 * time.Millisecond},
		{"0", 5 * time.Millisecond},
		{"-5", 5 * time.Millisecond},
		{"abc", 5 * time.Millisecond},
	}
	for _, tc := range cases {
		got := ParseCPUBurnMS(tc.input)
		if got != tc.expected {
			t.Errorf("ParseCPUBurnMS(%q) = %v, expected %v", tc.input, got, tc.expected)
		}
	}
}

func TestParseStartupDelay(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"30", 30 * time.Second},
		{"15", 15 * time.Second},
		{"-1", 0},
		{"notanumber", 0},
	}
	for _, tc := range cases {
		got := ParseStartupDelay(tc.input)
		if got != tc.expected {
			t.Errorf("ParseStartupDelay(%q) = %v, expected %v", tc.input, got, tc.expected)
		}
	}
}
