package actuator

import (
	"math/rand"
	"testing"
)

// ---- helpers ---------------------------------------------------------------

func acValidState() HPAState {
	return HPAState{MinReplicas: 2, MaxReplicas: 20, CurrentReplicas: 5, OriginalMax: 0}
}

// acApplyPlan simulates what the Actuator would write to Kubernetes, then
// returns a new HPAState that reflects the written values.
func acApplyPlan(s HPAState, p Plan) HPAState {
	if p.Action != "cap" {
		return s
	}
	s2 := s
	s2.MaxReplicas = p.NewMax
	if p.SaveOriginal != 0 {
		s2.OriginalMax = p.SaveOriginal
	}
	return s2
}

// ---- table tests for PlanCap -----------------------------------------------

func TestPlanCap_Refuse(t *testing.T) {
	valid := acValidState()
	cases := []struct {
		name     string
		s        HPAState
		proposed int32
		floor    int32
	}{
		{"MinReplicas zero", HPAState{MinReplicas: 0, MaxReplicas: 10, CurrentReplicas: 2}, 5, 1},
		{"MinReplicas negative", HPAState{MinReplicas: -1, MaxReplicas: 10, CurrentReplicas: 2}, 5, 1},
		{"MaxReplicas zero", HPAState{MinReplicas: 1, MaxReplicas: 0, CurrentReplicas: 2}, 5, 1},
		{"MaxReplicas negative", HPAState{MinReplicas: 1, MaxReplicas: -1, CurrentReplicas: 2}, 5, 1},
		{"CurrentReplicas zero", HPAState{MinReplicas: 1, MaxReplicas: 10, CurrentReplicas: 0}, 5, 1},
		{"CurrentReplicas negative", HPAState{MinReplicas: 1, MaxReplicas: 10, CurrentReplicas: -1}, 5, 1},
		{"proposed zero", valid, 0, 1},
		{"proposed negative", valid, -3, 1},
		{"floor zero", valid, 5, 0},
		{"floor negative", valid, 5, -1},
		{"Min > Max", HPAState{MinReplicas: 10, MaxReplicas: 5, CurrentReplicas: 5}, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PlanCap(tc.s, tc.proposed, tc.floor)
			if p.Action != "refuse" {
				t.Errorf("expected refuse, got %q (%s)", p.Action, p.Why)
			}
		})
	}
}

func TestPlanCap_Noop(t *testing.T) {
	// proposed == MaxReplicas → noop
	s := acValidState()
	p := PlanCap(s, s.MaxReplicas, 1)
	if p.Action != "noop" {
		t.Errorf("expected noop when proposed == MaxReplicas, got %q", p.Action)
	}

	// proposed > MaxReplicas → clamped to base, still noop
	p2 := PlanCap(s, s.MaxReplicas+100, 1)
	if p2.Action != "noop" {
		t.Errorf("expected noop when proposed > MaxReplicas, got %q", p2.Action)
	}
}

func TestPlanCap_Cap_BasicLower(t *testing.T) {
	s := HPAState{MinReplicas: 2, MaxReplicas: 20, CurrentReplicas: 5, OriginalMax: 0}
	p := PlanCap(s, 10, 1)
	if p.Action != "cap" {
		t.Fatalf("expected cap, got %q", p.Action)
	}
	if p.NewMax != 10 {
		t.Errorf("expected NewMax 10, got %d", p.NewMax)
	}
	if p.SaveOriginal != 20 {
		t.Errorf("expected SaveOriginal 20 (no prior saved), got %d", p.SaveOriginal)
	}
}

func TestPlanCap_Rule2_EffFloorClampsUp(t *testing.T) {
	// CurrentReplicas > proposed floor → effFloor = CurrentReplicas
	s := HPAState{MinReplicas: 2, MaxReplicas: 20, CurrentReplicas: 8, OriginalMax: 0}
	p := PlanCap(s, 5, 1) // proposed 5, but current is 8 → must clamp to 8
	if p.Action != "cap" {
		t.Fatalf("expected cap, got %q", p.Action)
	}
	if p.NewMax != 8 {
		t.Errorf("expected NewMax clamped to CurrentReplicas 8, got %d", p.NewMax)
	}
}

func TestPlanCap_Rule2_MinReplicasClampsUp(t *testing.T) {
	s := HPAState{MinReplicas: 6, MaxReplicas: 20, CurrentReplicas: 3, OriginalMax: 0}
	p := PlanCap(s, 4, 1)
	if p.NewMax < 6 {
		t.Errorf("NewMax %d below MinReplicas 6", p.NewMax)
	}
}

func TestPlanCap_Rule3_RespectsOriginalMax(t *testing.T) {
	// OriginalMax = 15, MaxReplicas already 10 (cap already applied)
	// A second call with proposed=12 must use base=15, clamp to 12 → still below current max 10 → cap
	s := HPAState{MinReplicas: 2, MaxReplicas: 10, CurrentReplicas: 4, OriginalMax: 15}
	p := PlanCap(s, 8, 1)
	// newMax = min(8, 15) = 8; effFloor=max(1,4,2)=4; newMax=8 < MaxReplicas=10 → cap
	if p.Action != "cap" || p.NewMax != 8 {
		t.Errorf("unexpected plan: %+v", p)
	}
	// SaveOriginal must be 0 because OriginalMax already set
	if p.SaveOriginal != 0 {
		t.Errorf("SaveOriginal must not overwrite existing OriginalMax; got %d", p.SaveOriginal)
	}
}

func TestPlanCap_Rule6_SaveOriginalNotOverwritten(t *testing.T) {
	s := HPAState{MinReplicas: 2, MaxReplicas: 10, CurrentReplicas: 3, OriginalMax: 20}
	p := PlanCap(s, 6, 1)
	if p.SaveOriginal != 0 {
		t.Errorf("SaveOriginal should be 0 when OriginalMax already saved, got %d", p.SaveOriginal)
	}
}

func TestPlanCap_Idempotent(t *testing.T) {
	s := HPAState{MinReplicas: 2, MaxReplicas: 20, CurrentReplicas: 5, OriginalMax: 0}
	proposed, floor := int32(10), int32(1)
	p := PlanCap(s, proposed, floor)
	if p.Action != "cap" {
		t.Fatalf("first call should cap, got %q", p.Action)
	}
	s2 := acApplyPlan(s, p)
	p2 := PlanCap(s2, proposed, floor)
	if p2.Action != "noop" {
		t.Errorf("second call should be noop after cap applied, got %q (%s)", p2.Action, p2.Why)
	}
}

// ---- PlanRestore -----------------------------------------------------------

func TestPlanRestore_WithOriginalMax(t *testing.T) {
	s := HPAState{MinReplicas: 2, MaxReplicas: 10, CurrentReplicas: 4, OriginalMax: 20}
	p := PlanRestore(s)
	if p.Action != "restore" || p.NewMax != 20 {
		t.Errorf("expected restore to 20, got %+v", p)
	}
}

func TestPlanRestore_NoOriginalMax(t *testing.T) {
	s := HPAState{MinReplicas: 2, MaxReplicas: 10, CurrentReplicas: 4, OriginalMax: 0}
	p := PlanRestore(s)
	if p.Action != "noop" {
		t.Errorf("expected noop, got %q", p.Action)
	}
}

// ---- property loop ---------------------------------------------------------

func TestPlanCap_Properties(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 5000; i++ {
		minR := int32(rng.Intn(5) + 1)                    // 1..5
		maxR := minR + int32(rng.Intn(50))                // min..min+49
		currR := minR + int32(rng.Intn(int(maxR-minR+1))) // min..max
		var origMax int32
		if rng.Intn(2) == 0 {
			origMax = maxR + int32(rng.Intn(20)) // previously saved, >= maxR
		}
		s := HPAState{
			MinReplicas:     minR,
			MaxReplicas:     maxR,
			CurrentReplicas: currR,
			OriginalMax:     origMax,
		}
		proposed := int32(rng.Intn(60) + 1) // 1..60
		floor := int32(rng.Intn(10) + 1)    // 1..10

		p := PlanCap(s, proposed, floor)

		if p.Action == "refuse" {
			t.Fatalf("iteration %d: got refuse on valid state %+v proposed=%d floor=%d", i, s, proposed, floor)
		}

		// base for invariant checks
		base := s.MaxReplicas
		if s.OriginalMax > 0 {
			base = s.OriginalMax
		}

		// Invariant: NewMax >= CurrentReplicas
		if p.NewMax < s.CurrentReplicas {
			t.Fatalf("iter %d: NewMax %d < CurrentReplicas %d", i, p.NewMax, s.CurrentReplicas)
		}
		// Invariant: NewMax >= MinReplicas
		if p.NewMax < s.MinReplicas {
			t.Fatalf("iter %d: NewMax %d < MinReplicas %d", i, p.NewMax, s.MinReplicas)
		}
		// Invariant: NewMax <= base
		if p.NewMax > base {
			t.Fatalf("iter %d: NewMax %d > base %d", i, p.NewMax, base)
		}
		// Invariant: SaveOriginal never overwrites existing OriginalMax
		if s.OriginalMax != 0 && p.SaveOriginal != 0 {
			t.Fatalf("iter %d: SaveOriginal %d overwrites existing OriginalMax %d", i, p.SaveOriginal, s.OriginalMax)
		}
		// Invariant: applying cap then calling again → noop
		if p.Action == "cap" {
			s2 := acApplyPlan(s, p)
			p2 := PlanCap(s2, proposed, floor)
			if p2.Action != "noop" {
				t.Fatalf("iter %d: expected noop after applying cap, got %q", i, p2.Action)
			}
		}
	}
}
