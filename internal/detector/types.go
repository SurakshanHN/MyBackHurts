package detector

import "time"

type Verdict string

const (
	Helped  Verdict = "helped"
	Useless Verdict = "useless"
	Harmful Verdict = "harmful"
	Unknown Verdict = "unknown"
)

const (
	AltRuledOut    = "ruled_out"
	AltNotRuledOut = "not_ruled_out"
)

type Sample struct {
	T                   time.Time
	Replicas, Ready     int
	RPS, P99Ms, ErrRate float64
}

type DepSample struct {
	T          time.Time
	Saturation float64
	LatencyMs  float64
}

type Config struct {
	MinSamples        int
	HarmMargin        float64
	SaturationPct     float64
	MinConfidence     float64
	WarmupSeconds     int
	MinThroughputGain float64
}

type Input struct {
	ScaleUpAt    time.Time
	FromReplicas int
	ToReplicas   int
	Series       []Sample
	Deps         map[string][]DepSample
	Cfg          Config
}

// Finding is one measured quantity with its expected range.
type Finding struct {
	Name     string
	Observed float64
	Expected float64
	Lower    float64
	Upper    float64
}

// Alternative is a competing explanation and whether the data rules it out.
type Alternative struct {
	Hypothesis string
	Status     string // AltRuledOut or AltNotRuledOut
	Why        string
}

type Result struct {
	Verdict      Verdict
	Confidence   float64
	Culprit      string
	Reasons      []string
	Findings     []Finding
	Alternatives []Alternative
	DataQuality  []string
}
