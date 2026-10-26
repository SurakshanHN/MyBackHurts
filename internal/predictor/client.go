// Package predictor is an HTTP client for the Python prediction service.
//
// The predictor service (Part 5) exposes a single endpoint:
//
//	POST /predict
//	Content-Type: application/json
//	{"window": [...float64 rps values...]}
//
//	→ 200 OK
//	{"predicted_rps": 142.5}
//
// The client converts a predicted RPS value into a recommended replica count
// using a configurable concurrency-per-pod constant (CONNS_PER_POD from the
// checkout service, default 20) and applies a safety headroom factor.
package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

const (
	// DefaultConnsPerPod is the per-pod concurrency limit baked into the
	// checkout service. Replicas = ceil(predictedRPS / ConnsPerPod).
	DefaultConnsPerPod = 20

	// DefaultHeadroom is the multiplicative safety factor applied to the raw
	// replica estimate. 1.20 → 20% buffer above the bare minimum.
	DefaultHeadroom = 1.20
)

// Client calls the prediction service and translates the result to a replica count.
type Client struct {
	BaseURL     string
	ConnsPerPod int
	Headroom    float64
	HTTP        *http.Client
}

// New constructs a Client with sensible defaults.
func New(baseURL string) *Client {
	return &Client{
		BaseURL:     baseURL,
		ConnsPerPod: DefaultConnsPerPod,
		Headroom:    DefaultHeadroom,
		HTTP:        &http.Client{Timeout: 5 * time.Second},
	}
}

// predictRequest is the JSON body sent to /predict.
type predictRequest struct {
	Window []float64 `json:"window"`
}

// predictResponse is the JSON body returned by /predict.
type predictResponse struct {
	PredictedRPS float64 `json:"predicted_rps"`
}

// Predict calls the predictor service with the given recent-RPS window and
// returns the raw predicted RPS value.
func (c *Client) Predict(ctx context.Context, window []float64) (float64, error) {
	body, err := json.Marshal(predictRequest{Window: window})
	if err != nil {
		return 0, fmt.Errorf("predictor: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/predict", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("predictor: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("predictor: POST /predict: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("predictor: unexpected status %d", resp.StatusCode)
	}

	var pr predictResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, fmt.Errorf("predictor: decode response: %w", err)
	}
	if pr.PredictedRPS < 0 {
		return 0, fmt.Errorf("predictor: negative predicted_rps %f", pr.PredictedRPS)
	}
	return pr.PredictedRPS, nil
}

// ReplicasFor converts a predicted RPS into a replica count, applying headroom
// and clamping to [min, max]. Never returns fewer than minReplicas.
func (c *Client) ReplicasFor(predictedRPS float64, minReplicas, maxReplicas int32) int32 {
	conns := c.ConnsPerPod
	if conns <= 0 {
		conns = DefaultConnsPerPod
	}
	headroom := c.Headroom
	if headroom <= 0 {
		headroom = DefaultHeadroom
	}

	raw := predictedRPS * headroom / float64(conns)
	needed := int32(math.Ceil(raw))

	if needed < minReplicas {
		needed = minReplicas
	}
	if needed > maxReplicas {
		needed = maxReplicas
	}
	return needed
}
