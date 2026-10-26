package detector

import "time"

// Sample holds a point in time with basic metrics.
type Sample struct {
    T        time.Time
    Replicas int
    Ready    int
    RPS      float64
    P99Ms    float64
    ErrRate  float64 // 0-1
}

// Verdict represents the outcome of a scale assessment.
type Verdict string

const (
    Helped  Verdict = "helped"
    Useless Verdict = "useless"
    Harmful Verdict = "harmful"
    Unknown Verdict = "unknown"
)

// DepSample is a simple packaging for optional dependency metrics.
type DepSample struct {
    T          time.Time
    Saturation float64
}

// Config contains thresholds used by the detector.
type Config struct {
    MinSamples    int     // below this -> Unknown
    HarmMargin    float64 // e.g. 0.15 = 15% worse than counterfactual
    SaturationPct float64 // e.g. 90
    MinConfidence float64 // below this -> Unknown
    WarmupSeconds int
}

// Input holds all context needed for detection.
type Input struct {
    ScaleUpAt    time.Time
    FromReplicas int
    ToReplicas   int
    Series       []Sample              // sorted by time
    DepS        map[string][]DepSample // name -> sorted series
    Cfg          Config
}

// Result contains the detector's conclusion.
type Result struct {
    Verdict    Verdict
    Confidence float64  // 0-1
    Culprit    string   // "" if none
    Reasons    []string // plain facts: "p99 rose 82% vs counterfactual"
}
