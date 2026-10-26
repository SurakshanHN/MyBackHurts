package detector

import "time"

type Verdict string

const (
	Helped  Verdict = "helped"
	Useless Verdict = "useless"
	Harmful Verdict = "harmful"
	Unknown Verdict = "unknown"
)

type Sample struct {
	T        time.Time
	Replicas int
	Ready    int
	RPS      float64
	P99Ms    float64
	ErrRate  float64 // 0-1
}

type DepSample struct {
	T          time.Time
	Saturation float64
}

type Config struct {
	MinSamples    int     // below this -> Unknown
	HarmMargin    float64 // e.g. 0.15 = 15% worse than counterfactual
	SaturationPct float64 // e.g. 90
	MinConfidence float64 // below this -> Unknown
	WarmupSeconds int
}

type Input struct {
	ScaleUpAt    time.Time
	FromReplicas int
	ToReplicas   int
	Series       []Sample               // sorted by time
	Deps         map[string][]DepSample // name -> sorted series
	Cfg          Config
}

type Result struct {
	Verdict    Verdict
	Confidence float64  // 0-1
	Culprit    string   // "" if none
	Reasons    []string // plain facts: "p99 rose 82% vs counterfactual"
}
