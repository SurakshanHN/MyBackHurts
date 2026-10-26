package scaledobject_test

import (
	"errors"
	"testing"
	"time"

	"github.com/prescale/prescale/internal/scaledobject"
)

func TestPlanDecision_Table(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	defaultCfg := scaledobject.Config{
		BaselineMin: 3,
		MaxLimit:    10,
		BoostTTL:    60 * time.Second,
		MaxFailures: 3,
		MaxStep:     3,
		Cooldown:    30 * time.Second,
	}

	tests := []struct {
		name                 string
		state                scaledobject.State
		cfg                  scaledobject.Config
		now                  time.Time
		desired              int32
		tickErr              error
		wantAction           string
		wantMin              int32
		wantSaveOrig         int32
		wantClearAnnotations bool
		wantFailures         int
		checkBoostedUntil    bool
		wantBoostedUntil     time.Time
	}{
		{
			name: "initial boost saves original and sets boosted-until",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
			},
			cfg:               defaultCfg,
			now:               t0,
			desired:           6,
			wantAction:        "raise",
			wantMin:           6,
			wantSaveOrig:      3,
			checkBoostedUntil: true,
			wantBoostedUntil:  t0.Add(60 * time.Second),
		},
		{
			name: "subsequent boost does NOT overwrite original",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(30 * time.Second),
			},
			cfg:               defaultCfg,
			now:               t0,
			desired:           8,
			wantAction:        "raise",
			wantMin:           8,
			wantSaveOrig:      0, // must not overwrite original
			checkBoostedUntil: true,
			wantBoostedUntil:  t0.Add(60 * time.Second),
		},
		{
			name: "hold at same boost level renews lease",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(10 * time.Second),
			},
			cfg:               defaultCfg,
			now:               t0,
			desired:           6,
			wantAction:        "hold",
			wantMin:           6,
			wantSaveOrig:      0,
			checkBoostedUntil: true,
			wantBoostedUntil:  t0.Add(60 * time.Second),
		},
		{
			name: "clamp: max-step limits rise to CurrentMin + 3",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
			},
			cfg:          defaultCfg,
			now:          t0,
			desired:      9, // 3 + 3 = 6
			wantAction:   "raise",
			wantMin:      6,
			wantSaveOrig: 3,
		},
		{
			name: "clamp: ceiling respects ScaledObject maxReplicaCount",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    8, // max is 8
				OriginalMin:        3,
				BoostedUntil:       t0.Add(30 * time.Second),
			},
			cfg:        defaultCfg,
			now:        t0,
			desired:    10, // clamped to 8
			wantAction: "raise",
			wantMin:    8,
		},
		{
			name: "clamp: ceiling respects config MaxLimit if lower",
			state: scaledobject.State{
				CurrentMinReplicas: 5,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(30 * time.Second),
			},
			cfg: scaledobject.Config{
				BaselineMin: 3,
				MaxLimit:    7, // lower than 10
				BoostTTL:    60 * time.Second,
				MaxStep:     5,
			},
			now:        t0,
			desired:    9,
			wantAction: "raise",
			wantMin:    7,
		},
		{
			name: "release trigger: desired <= original when boosted",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(30 * time.Second),
			},
			cfg:                  defaultCfg,
			now:                  t0,
			desired:              3, // back to baseline
			wantAction:           "release",
			wantMin:              3,
			wantClearAnnotations: true,
		},
		{
			name: "release trigger: lease expired when boosted",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(-1 * time.Second), // expired 1s ago
			},
			cfg:                  defaultCfg,
			now:                  t0,
			desired:              7,
			wantAction:           "release",
			wantMin:              3,
			wantClearAnnotations: true,
		},
		{
			name: "release trigger: consecutive failures reaching max-failures",
			state: scaledobject.State{
				CurrentMinReplicas:  6,
				MaxReplicaCount:     10,
				OriginalMin:         3,
				BoostedUntil:        t0.Add(30 * time.Second),
				ConsecutiveFailures: 2, // 2 prior failures + 1 = 3
			},
			cfg:                  defaultCfg,
			now:                  t0,
			desired:              0,
			tickErr:              errors.New("connection timeout"),
			wantAction:           "release",
			wantMin:              3,
			wantClearAnnotations: true,
			wantFailures:         3,
		},
		{
			name: "failures under max-failures holds lease",
			state: scaledobject.State{
				CurrentMinReplicas:  6,
				MaxReplicaCount:     10,
				OriginalMin:         3,
				BoostedUntil:        t0.Add(30 * time.Second),
				ConsecutiveFailures: 1, // 1 prior failure + 1 = 2 < 3
			},
			cfg:          defaultCfg,
			now:          t0,
			desired:      0,
			tickErr:      errors.New("503 Service Unavailable"),
			wantAction:   "hold",
			wantMin:      6,
			wantFailures: 2,
		},
		{
			name: "failure when not boosted returns error action",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
			},
			cfg:          defaultCfg,
			now:          t0,
			desired:      0,
			tickErr:      errors.New("predictor unreachable"),
			wantAction:   "error",
			wantMin:      3,
			wantFailures: 1,
		},
		{
			name: "cooldown blocks new boost if within cooldown window",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
				LastReleaseTime:    t0.Add(-10 * time.Second), // 10s ago, cooldown is 30s
			},
			cfg:        defaultCfg,
			now:        t0,
			desired:    6,
			wantAction: "noop",
			wantMin:    3,
		},
		{
			name: "cooldown allows new boost after cooldown elapsed",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
				LastReleaseTime:    t0.Add(-31 * time.Second), // 31s ago, cooldown is 30s
			},
			cfg:          defaultCfg,
			now:          t0,
			desired:      6,
			wantAction:   "raise",
			wantMin:      6,
			wantSaveOrig: 3,
		},
		{
			name: "noop when desired <= baseline and not boosted",
			state: scaledobject.State{
				CurrentMinReplicas: 3,
				MaxReplicaCount:    10,
				OriginalMin:        0,
			},
			cfg:        defaultCfg,
			now:        t0,
			desired:    2,
			wantAction: "noop",
			wantMin:    3,
		},
		{
			name: "restart: boosted with active lease continues and renews",
			state: scaledobject.State{
				CurrentMinReplicas: 6,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(20 * time.Second), // active lease from before restart
			},
			cfg:               defaultCfg,
			now:               t0,
			desired:           7,
			wantAction:        "raise",
			wantMin:           7,
			wantSaveOrig:      0,
			checkBoostedUntil: true,
			wantBoostedUntil:  t0.Add(60 * time.Second),
		},
		{
			name: "step-down while boosted above baseline",
			state: scaledobject.State{
				CurrentMinReplicas: 8,
				MaxReplicaCount:    10,
				OriginalMin:        3,
				BoostedUntil:       t0.Add(30 * time.Second),
			},
			cfg:               defaultCfg,
			now:               t0,
			desired:           5, // still above 3
			wantAction:        "hold",
			wantMin:           5,
			checkBoostedUntil: true,
			wantBoostedUntil:  t0.Add(60 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := scaledobject.PlanDecision(tt.state, tt.cfg, tt.now, tt.desired, tt.tickErr)

			if plan.Action != tt.wantAction {
				t.Errorf("Action: got %q, want %q (reason: %s)", plan.Action, tt.wantAction, plan.Reason)
			}
			if plan.NewMin != tt.wantMin {
				t.Errorf("NewMin: got %d, want %d", plan.NewMin, tt.wantMin)
			}
			if plan.SaveOriginal != tt.wantSaveOrig {
				t.Errorf("SaveOriginal: got %d, want %d", plan.SaveOriginal, tt.wantSaveOrig)
			}
			if plan.ClearAnnotations != tt.wantClearAnnotations {
				t.Errorf("ClearAnnotations: got %v, want %v", plan.ClearAnnotations, tt.wantClearAnnotations)
			}
			if tt.wantFailures != 0 && plan.NewFailures != tt.wantFailures {
				t.Errorf("NewFailures: got %d, want %d", plan.NewFailures, tt.wantFailures)
			}
			if tt.checkBoostedUntil && !plan.NewBoostedUntil.Equal(tt.wantBoostedUntil) {
				t.Errorf("NewBoostedUntil: got %v, want %v", plan.NewBoostedUntil, tt.wantBoostedUntil)
			}
		})
	}
}
