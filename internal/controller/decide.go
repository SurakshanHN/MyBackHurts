package controller

import (
	"fmt"

	"github.com/prescale/prescale/internal/detector"
)

// Action represents the outcome of Decide.
type Action interface {
	Name() string
	String() string
}

type actionNone struct{}

func (actionNone) Name() string   { return "None" }
func (actionNone) String() string { return "None" }

// Cap specifies capping the HPA to Proposed replicas.
type Cap struct {
	Proposed int32
}

func (c Cap) Name() string   { return "Cap" }
func (c Cap) String() string { return fmt.Sprintf("Cap{%d}", c.Proposed) }

type actionRestore struct{}

func (actionRestore) Name() string   { return "Restore" }
func (actionRestore) String() string { return "Restore" }

type actionRecordOnly struct{}

func (actionRecordOnly) Name() string   { return "RecordOnly" }
func (actionRecordOnly) String() string { return "RecordOnly" }

var (
	// None indicates no action should be taken.
	None Action = actionNone{}

	// Restore indicates any existing cap should be restored.
	Restore Action = actionRestore{}

	// RecordOnly indicates the observation should be recorded without modifying the HPA.
	RecordOnly Action = actionRecordOnly{}
)

// Decide evaluates the current state and detector result to determine what action to take.
// Rules are evaluated in strict priority order:
func Decide(mode string, res detector.Result, capped bool, expired bool, metricsErr error, floor int32, proposed int32) Action {
	_ = floor

	// Rule 1: missing data or unknown verdict never causes action; if an existing
	// cap has expired, restore it, otherwise do nothing.
	if metricsErr != nil || res.Verdict == detector.Unknown {
		if capped && expired {
			return Restore
		}
		return None
	}

	// Rule 2: observe mode never caps; if an existing cap is active, restore it,
	// otherwise record the observation only.
	if mode == "observe" {
		if capped {
			return Restore
		}
		return RecordOnly
	}

	// Rule 3: any active cap that has exceeded its TTL must be restored.
	if capped && expired {
		return Restore
	}

	// Rule 4: in enforce mode, harmful scale-ups that are not already capped
	// must be capped with the proposed replica limit.
	if mode == "enforce" && res.Verdict == detector.Harmful && !capped {
		return Cap{Proposed: proposed}
	}

	// Rule 5: default to no action when no other rule applies.
	return None
}
