package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/prescale/prescale/internal/detector"
)

func evArms() []Arm {
	return []Arm{
		armPlainHPA{},
		armFixedCap{},
		armThresholdGuard{},
		armSimpleRule{},
		armPreScale{},
	}
}

func TestEval_SameSeedGivesIdenticalTable(t *testing.T) {
	seed := int64(42)

	scenarios1 := evGenerateScenarios(seed)
	scenarios2 := evGenerateScenarios(seed)

	if len(scenarios1) != len(scenarios2) {
		t.Fatalf("scenario count mismatch: %d vs %d", len(scenarios1), len(scenarios2))
	}

	arms1 := evArms()
	arms2 := evArms()

	perScen1, totals1 := evEvaluate(scenarios1, arms1)
	perScen2, totals2 := evEvaluate(scenarios2, arms2)

	if !reflect.DeepEqual(perScen1, perScen2) {
		t.Fatalf("per-scenario table not identical between runs with same seed:\nrun1: %+v\nrun2: %+v", perScen1, perScen2)
	}

	if !reflect.DeepEqual(totals1, totals2) {
		t.Fatalf("totals table not identical between runs with same seed:\nrun1: %+v\nrun2: %+v", totals1, totals2)
	}
}

func TestEval_NoArmReadsGroundTruth(t *testing.T) {
	arms := evArms()

	// Verify Arm interface method signature: Decide accepts only detector.Input, never ground truth.
	armInterfaceType := reflect.TypeOf((*Arm)(nil)).Elem()
	method, found := armInterfaceType.MethodByName("Decide")
	if !found {
		t.Fatal("Arm interface missing Decide method")
	}
	// Decide should take (in detector.Input) and return (cap bool, unknown bool)
	if method.Type.NumIn() != 1 || method.Type.In(0) != reflect.TypeOf(detector.Input{}) {
		t.Fatalf("Decide signature must only accept detector.Input, got inputs: %v", method.Type)
	}
	if method.Type.NumOut() != 2 || method.Type.Out(0).Kind() != reflect.Bool || method.Type.Out(1).Kind() != reflect.Bool {
		t.Fatalf("Decide signature must return (bool, bool), got: %v", method.Type)
	}

	// Verify on scenarios that modifying GroundTruth in test harness does not affect arm decisions
	scenarios := evGenerateScenarios(123)
	for i, sc := range scenarios {
		for _, arm := range arms {
			cap1, unk1 := arm.Decide(sc.Input)

			// Flip the ground truth label
			tampered := sc
			tampered.Harmful = !sc.Harmful

			cap2, unk2 := arm.Decide(tampered.Input)

			if cap1 != cap2 || unk1 != unk2 {
				t.Fatalf("scenario %d arm %s decision changed when ground truth label changed: (%v,%v) vs (%v,%v)",
					i, arm.Name(), cap1, unk1, cap2, unk2)
			}
		}
	}
}

func TestEval_DistinctP99Series(t *testing.T) {
	scenarios := evGenerateScenarios(42)
	byType := make(map[ScenarioType][]Scenario)
	for _, sc := range scenarios {
		byType[sc.Type] = append(byType[sc.Type], sc)
	}

	for sType, list := range byType {
		distinct := make(map[string]bool)
		for _, sc := range list {
			var p99s []string
			for _, s := range sc.Input.Series {
				p99s = append(p99s, fmt.Sprintf("%.2f", s.P99Ms))
			}
			distinct[fmt.Sprint(p99s)] = true
		}
		t.Logf("Scenario %s has %d distinct p99 series out of %d", sType, len(distinct), len(list))
		if len(distinct) < 150 {
			t.Fatalf("Scenario %s has only %d distinct p99 series; expected >= 150", sType, len(distinct))
		}
	}
}
