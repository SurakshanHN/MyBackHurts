package controller

import (
	"errors"
	"math/rand"
	"testing"

	"github.com/prescale/prescale/internal/detector"
)

func TestDecide_Table(t *testing.T) {
	errMock := errors.New("prometheus error")

	tests := []struct {
		name       string
		mode       string
		verdict    detector.Verdict
		capped     bool
		expired    bool
		metricsErr error
		floor      int32
		proposed   int32
		want       Action
	}{
		// Rule 1: metricsErr or Unknown
		{
			name:       "Rule1_metricsErr_not_capped",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     false,
			expired:    false,
			metricsErr: errMock,
			proposed:   5,
			want:       None,
		},
		{
			name:       "Rule1_metricsErr_capped_not_expired",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    false,
			metricsErr: errMock,
			proposed:   5,
			want:       None,
		},
		{
			name:       "Rule1_metricsErr_capped_and_expired",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    true,
			metricsErr: errMock,
			proposed:   5,
			want:       Restore,
		},
		{
			name:       "Rule1_verdict_unknown_not_capped",
			mode:       "enforce",
			verdict:    detector.Unknown,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   5,
			want:       None,
		},
		{
			name:       "Rule1_verdict_unknown_capped_and_expired",
			mode:       "enforce",
			verdict:    detector.Unknown,
			capped:     true,
			expired:    true,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},
		{
			name:       "Rule1_verdict_unknown_capped_not_expired",
			mode:       "enforce",
			verdict:    detector.Unknown,
			capped:     true,
			expired:    false,
			metricsErr: nil,
			proposed:   5,
			want:       None,
		},

		// Rule 2: mode == "observe"
		{
			name:       "Rule2_observe_not_capped_harmful",
			mode:       "observe",
			verdict:    detector.Harmful,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   5,
			want:       RecordOnly,
		},
		{
			name:       "Rule2_observe_not_capped_helped",
			mode:       "observe",
			verdict:    detector.Helped,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   5,
			want:       RecordOnly,
		},
		{
			name:       "Rule2_observe_capped_restores",
			mode:       "observe",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    false,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},
		{
			name:       "Rule2_observe_capped_and_expired_restores",
			mode:       "observe",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    true,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},

		// Rule 3: capped && expired
		{
			name:       "Rule3_enforce_helped_capped_and_expired",
			mode:       "enforce",
			verdict:    detector.Helped,
			capped:     true,
			expired:    true,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},
		{
			name:       "Rule3_enforce_useless_capped_and_expired",
			mode:       "enforce",
			verdict:    detector.Useless,
			capped:     true,
			expired:    true,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},
		{
			name:       "Rule3_enforce_harmful_capped_and_expired",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    true,
			metricsErr: nil,
			proposed:   5,
			want:       Restore,
		},

		// Rule 4: mode == "enforce" && res.Verdict == Harmful && !capped
		{
			name:       "Rule4_enforce_harmful_uncapped",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			floor:      2,
			proposed:   6,
			want:       Cap{Proposed: 6},
		},

		// Rule 5: else None
		{
			name:       "Rule5_enforce_harmful_already_capped",
			mode:       "enforce",
			verdict:    detector.Harmful,
			capped:     true,
			expired:    false,
			metricsErr: nil,
			proposed:   6,
			want:       None,
		},
		{
			name:       "Rule5_enforce_helped_uncapped",
			mode:       "enforce",
			verdict:    detector.Helped,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   6,
			want:       None,
		},
		{
			name:       "Rule5_enforce_useless_uncapped",
			mode:       "enforce",
			verdict:    detector.Useless,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   6,
			want:       None,
		},
		{
			name:       "Rule5_other_mode_uncapped",
			mode:       "dry-run",
			verdict:    detector.Harmful,
			capped:     false,
			expired:    false,
			metricsErr: nil,
			proposed:   6,
			want:       None,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := detector.Result{Verdict: tc.verdict}
			got := Decide(tc.mode, res, tc.capped, tc.expired, tc.metricsErr, tc.floor, tc.proposed)
			if got != tc.want {
				t.Fatalf("Decide(%s, %v, capped=%v, exp=%v, err=%v, floor=%d, prop=%d) = %v; want %v",
					tc.mode, tc.verdict, tc.capped, tc.expired, tc.metricsErr, tc.floor, tc.proposed, got, tc.want)
			}
		})
	}
}

func TestDecide_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	modes := []string{"observe", "enforce", "other", ""}
	verdicts := []detector.Verdict{detector.Harmful, detector.Helped, detector.Useless, detector.Unknown}

	for i := 0; i < 5000; i++ {
		mode := modes[rng.Intn(len(modes))]
		verdict := verdicts[rng.Intn(len(verdicts))]
		capped := rng.Intn(2) == 1
		expired := rng.Intn(2) == 1
		var metricsErr error
		if rng.Intn(2) == 1 {
			metricsErr = errors.New("err")
		}
		floor := int32(rng.Intn(10) + 1)
		proposed := int32(rng.Intn(20) + 1)

		res := detector.Result{Verdict: verdict}
		act := Decide(mode, res, capped, expired, metricsErr, floor, proposed)

		_, isCap := act.(Cap)

		// Assertion 1: observe mode NEVER returns Cap
		if mode == "observe" && isCap {
			t.Fatalf("iteration %d: observe mode returned Cap: %v", i, act)
		}

		// Assertion 2: metricsErr NEVER returns Cap
		if metricsErr != nil && isCap {
			t.Fatalf("iteration %d: metricsErr returned Cap: %v", i, act)
		}

		// Assertion 3: Unknown NEVER returns Cap
		if verdict == detector.Unknown && isCap {
			t.Fatalf("iteration %d: Unknown verdict returned Cap: %v", i, act)
		}
	}
}
