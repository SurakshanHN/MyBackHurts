package scaledobject

import (
	"fmt"
	"time"
)

const (
	// AnnotationOriginalMin stores the baseline minReplicaCount before prescaler boosting.
	AnnotationOriginalMin = "prescale.io/original-min-replicas"

	// AnnotationBoostedUntil stores the RFC3339 timestamp when the current boost lease expires.
	AnnotationBoostedUntil = "prescale.io/boosted-until"

	DefaultBoostTTL    = 60 * time.Second
	DefaultMaxFailures = 3
	DefaultMaxStep     = 3
	DefaultCooldown    = 30 * time.Second
	DefaultBaselineMin = 3
	DefaultMaxLimit    = 10
)

// State holds the current state of the ScaledObject and the prescaler loop.
type State struct {
	CurrentMinReplicas  int32
	MaxReplicaCount     int32
	OriginalMin         int32     // 0 if not currently boosted
	BoostedUntil        time.Time // zero if not currently boosted
	ConsecutiveFailures int
	LastReleaseTime     time.Time
}

// Config holds runtime configuration and safety bounds for the prescaler.
type Config struct {
	BaselineMin int32
	MaxLimit    int32
	BoostTTL    time.Duration
	MaxFailures int
	MaxStep     int32
	Cooldown    time.Duration
}

// Plan is the deterministic decision outcome of evaluating State against desired replicas.
type Plan struct {
	Action           string // "raise", "hold", "release", "noop", "error"
	NewMin           int32
	SaveOriginal     int32     // non-zero on initial boost to store baseline
	NewBoostedUntil  time.Time // RFC3339 lease renewal time
	ClearAnnotations bool      // true on release to remove prescale annotations
	NewFailures      int       // updated consecutive failures count
	NewReleaseTime   time.Time // updated last release timestamp
	Reason           string
}

// PlanDecision evaluates State and desired replicas against safety bounds and lease rules.
// It is a pure function that performs no I/O.
func PlanDecision(s State, cfg Config, now time.Time, desired int32, tickErr error) Plan {
	baseline := cfg.BaselineMin
	if baseline <= 0 {
		baseline = DefaultBaselineMin
	}

	original := s.OriginalMin
	if original <= 0 {
		original = s.CurrentMinReplicas
		if original <= 0 {
			original = baseline
		}
	}

	isBoosted := s.OriginalMin > 0

	ceiling := s.MaxReplicaCount
	if cfg.MaxLimit > 0 && (ceiling <= 0 || cfg.MaxLimit < ceiling) {
		ceiling = cfg.MaxLimit
	}
	if ceiling <= 0 {
		ceiling = DefaultMaxLimit
	}

	maxStep := cfg.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxStep
	}

	boostTTL := cfg.BoostTTL
	if boostTTL <= 0 {
		boostTTL = DefaultBoostTTL
	}

	maxFailures := cfg.MaxFailures
	if maxFailures <= 0 {
		maxFailures = DefaultMaxFailures
	}

	cooldown := cfg.Cooldown
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}

	// 1. Handle tick failure (metrics or predictor failure)
	if tickErr != nil {
		newFailures := s.ConsecutiveFailures + 1
		if isBoosted {
			if newFailures >= maxFailures {
				return Plan{
					Action:           "release",
					NewMin:           original,
					ClearAnnotations: true,
					NewFailures:      newFailures,
					NewReleaseTime:   now,
					Reason:           fmt.Sprintf("consecutive failures (%d) reached max-failures (%d)", newFailures, maxFailures),
				}
			}
			if !s.BoostedUntil.IsZero() && !now.Before(s.BoostedUntil) {
				return Plan{
					Action:           "release",
					NewMin:           original,
					ClearAnnotations: true,
					NewFailures:      newFailures,
					NewReleaseTime:   now,
					Reason:           "boost lease expired during failure",
				}
			}
			return Plan{
				Action:         "hold",
				NewMin:         s.CurrentMinReplicas,
				NewFailures:    newFailures,
				NewReleaseTime: s.LastReleaseTime,
				Reason:         fmt.Sprintf("failure %d/%d (holding lease): %v", newFailures, maxFailures, tickErr),
			}
		}
		return Plan{
			Action:         "error",
			NewMin:         s.CurrentMinReplicas,
			NewFailures:    newFailures,
			NewReleaseTime: s.LastReleaseTime,
			Reason:         fmt.Sprintf("tick error: %v", tickErr),
		}
	}

	// Tick succeeded: reset consecutive failures counter
	newFailures := 0

	// 2. Check lease expiry for existing boost
	if isBoosted && !s.BoostedUntil.IsZero() && !now.Before(s.BoostedUntil) {
		return Plan{
			Action:           "release",
			NewMin:           original,
			ClearAnnotations: true,
			NewFailures:      newFailures,
			NewReleaseTime:   now,
			Reason:           "boost lease expired",
		}
	}

	// 3. Desired at or below original baseline
	if desired <= original {
		if isBoosted {
			return Plan{
				Action:           "release",
				NewMin:           original,
				ClearAnnotations: true,
				NewFailures:      0,
				NewReleaseTime:   now,
				Reason:           fmt.Sprintf("desired (%d) <= original (%d)", desired, original),
			}
		}
		return Plan{
			Action:         "noop",
			NewMin:         s.CurrentMinReplicas,
			NewFailures:    0,
			NewReleaseTime: s.LastReleaseTime,
			Reason:         fmt.Sprintf("desired (%d) <= baseline (%d)", desired, original),
		}
	}

	// 4. Desired > original: check cooldown before initial boost
	if !isBoosted && !s.LastReleaseTime.IsZero() && now.Sub(s.LastReleaseTime) < cooldown {
		return Plan{
			Action:         "noop",
			NewMin:         s.CurrentMinReplicas,
			NewFailures:    0,
			NewReleaseTime: s.LastReleaseTime,
			Reason:         fmt.Sprintf("in cooldown (%v remaining)", cooldown-now.Sub(s.LastReleaseTime)),
		}
	}

	// 5. Clamps:
	// - never below original
	target := desired
	if target < original {
		target = original
	}
	// - never above ceiling (ScaledObject maxReplicaCount)
	if target > ceiling {
		target = ceiling
	}
	// - never raise by more than maxStep per tick
	if target > s.CurrentMinReplicas+maxStep {
		target = s.CurrentMinReplicas + maxStep
	}

	// 6. Decide action based on clamped target vs current
	if target > s.CurrentMinReplicas {
		saveOrig := int32(0)
		if !isBoosted {
			saveOrig = original
		}
		return Plan{
			Action:          "raise",
			NewMin:          target,
			SaveOriginal:    saveOrig,
			NewBoostedUntil: now.Add(boostTTL),
			NewFailures:     0,
			NewReleaseTime:  s.LastReleaseTime,
			Reason:          fmt.Sprintf("raising minReplicaCount from %d to %d (desired %d, max %d)", s.CurrentMinReplicas, target, desired, ceiling),
		}
	}

	if target == s.CurrentMinReplicas {
		if isBoosted {
			return Plan{
				Action:          "hold",
				NewMin:          target,
				NewBoostedUntil: now.Add(boostTTL),
				NewFailures:     0,
				NewReleaseTime:  s.LastReleaseTime,
				Reason:          fmt.Sprintf("holding minReplicaCount at %d and renewing lease", target),
			}
		}
		return Plan{
			Action:         "noop",
			NewMin:         target,
			NewFailures:    0,
			NewReleaseTime: s.LastReleaseTime,
			Reason:         fmt.Sprintf("at baseline %d", target),
		}
	}

	// target < s.CurrentMinReplicas (stepping down while still boosted above baseline)
	return Plan{
		Action:          "hold",
		NewMin:          target,
		NewBoostedUntil: now.Add(boostTTL),
		NewFailures:     0,
		NewReleaseTime:  s.LastReleaseTime,
		Reason:          fmt.Sprintf("adjusting minReplicaCount from %d to %d and renewing lease", s.CurrentMinReplicas, target),
	}
}
