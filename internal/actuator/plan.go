// Package actuator provides pure planning logic and Kubernetes HPA actuation.
package actuator

import "fmt"

// HPAState holds the relevant replica counts from an HPA.
// OriginalMax == 0 means no value has been saved yet.
type HPAState struct {
	MinReplicas     int32
	MaxReplicas     int32
	CurrentReplicas int32
	OriginalMax     int32 // 0 = none saved
}

// Plan is the result of evaluating an HPAState against a proposed replica cap.
type Plan struct {
	Action       string // "cap" | "noop" | "refuse" | "restore"
	NewMax       int32
	SaveOriginal int32
	Why          string
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// PlanCap evaluates a proposed replica cap against s and a minimum floor and
// returns the appropriate Plan. Rules are applied in order:
func PlanCap(s HPAState, proposed, floor int32) Plan {
	// Rule 1: reject degenerate inputs – zero or negative counters mean the
	// HPA or the request is in an unknown state that we must not act on.
	if s.MinReplicas <= 0 || s.MaxReplicas <= 0 || s.CurrentReplicas <= 0 ||
		proposed <= 0 || floor < 1 || s.MinReplicas > s.MaxReplicas {
		return Plan{Action: "refuse", Why: "invalid state or parameters (non-positive value or Min > Max)"}
	}

	// Rule 2: effective floor must be at least the current replica count and
	// the HPA minimum, so we never schedule fewer pods than are already running.
	effFloor := max32(floor, max32(s.CurrentReplicas, s.MinReplicas))

	// Rule 3: base is the true ceiling we respect (saved original or current
	// max). NewMax is the proposed cap, clamped to never exceed that base.
	base := s.MaxReplicas
	if s.OriginalMax > 0 {
		base = s.OriginalMax
	}
	newMax := min32(proposed, base)

	// Rule 4: NewMax must be at least the effective floor so we don't
	// accidentally scale below what the cluster currently needs.
	newMax = max32(newMax, effFloor)

	// Rule 5: if the computed NewMax is already >= the current maximum there
	// is nothing to change, so a no-op is the right answer.
	if newMax >= s.MaxReplicas {
		return Plan{Action: "noop", NewMax: s.MaxReplicas, Why: "proposed cap does not lower the current maximum"}
	}

	// Rule 6: we will lower MaxReplicas. Persist the base only if we haven't
	// already saved an OriginalMax, to avoid overwriting the true original.
	saveOriginal := int32(0)
	if s.OriginalMax == 0 {
		saveOriginal = base
	}
	return Plan{
		Action:       "cap",
		NewMax:       newMax,
		SaveOriginal: saveOriginal,
		Why:          fmt.Sprintf("lowering MaxReplicas from %d to %d", s.MaxReplicas, newMax),
	}
}

// PlanRestore returns a Plan to undo a previous cap.
// If OriginalMax > 0 the action is "restore" targeting that value; otherwise noop.
func PlanRestore(s HPAState) Plan {
	if s.OriginalMax > 0 {
		return Plan{
			Action: "restore",
			NewMax: s.OriginalMax,
			Why:    fmt.Sprintf("restoring MaxReplicas to original %d", s.OriginalMax),
		}
	}
	return Plan{Action: "noop", NewMax: s.MaxReplicas, Why: "no saved OriginalMax to restore"}
}
