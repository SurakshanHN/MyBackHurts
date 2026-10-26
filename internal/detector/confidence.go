// internal/detector/confidence.go
//
// Heuristic confidence scoring for detector verdicts.
// Score returns a value in [0, 1] based on statistical evidence
// and data quality. It is NOT a calibrated probability.

package detector

import "math"

// ConfidenceInput bundles the quantities needed to compute a verdict confidence score.
type ConfidenceInput struct {
	// N is min(pre count, post-warmup count).
	N int

	// Excess is the distance the observed value is beyond the prediction interval
	// (positive means outside the interval). For Harmful this is Observed-Upper for
	// the worse metric. For Helped/Useless this is |gain - MinThroughputGain|.
	Excess float64

	// HalfWidth is the half-width of the relevant interval. For Harmful this is
	// (Upper-Lower)/2 for the worse metric. For Helped/Useless this is
	// max(MinThroughputGain, 0.05).
	HalfWidth float64

	// Signals is the count of boolean signals that support the verdict
	// (signals that are true).
	Signals int

	// Total is the total number of possible signals.
	Total int

	// MissingFraction represents data-quality problems; in [0,1].
	// Each DataQuality message contributes 0.1, capped at 1.
	MissingFraction float64
}

// Score returns a heuristic confidence in [0, 1].
//
// The score has three components:
//  1. Statistical: how many standard half-widths the excess covers; capped at 1.
//  2. Signal: fraction of supporting signals.
//  3. Size: a sample-size factor that saturates around N=30.
//
// These are combined multiplicatively and then penalised by MissingFraction.
// The result is clamped to [0, 1].
func Score(ci ConfidenceInput) float64 {
	// Statistical component: sigmoid-ish normalisation of excess/halfWidth.
	var stat float64
	if ci.HalfWidth > 0 {
		ratio := ci.Excess / ci.HalfWidth
		// map ratio to [0,1]: 0 at ratio=0, saturates toward 1.
		stat = 1 - math.Exp(-math.Max(0, ratio))
	} else if ci.Excess > 0 {
		stat = 1.0
	}

	// Signal component.
	var sig float64
	if ci.Total > 0 {
		sig = float64(ci.Signals) / float64(ci.Total)
	}

	// Sample-size component: saturates at ~1 for N >= 30.
	size := 1 - math.Exp(-float64(ci.N)/10.0)

	// Combine: geometric-ish average of the three components.
	raw := (stat + sig + size) / 3.0

	// Penalise for data quality.
	penalty := math.Max(0, 1-ci.MissingFraction)

	score := raw * penalty
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}
