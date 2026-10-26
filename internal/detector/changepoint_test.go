package detector

import (
	"math"
	"math/rand"
	"testing"
)

func cpFlat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func cpNoisy(seed int64, n int, mean, std float64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + std*r.NormFloat64()
	}
	return out
}

func TestFindShift_DetectsStep(t *testing.T) {
	up := append(cpFlat(20, 10), cpFlat(20, 15)...)
	down := append(cpFlat(20, 10), cpFlat(20, 5)...)
	noisyUp := cpNoisy(1, 100, 10, 1)
	for i := 50; i < 100; i++ {
		noisyUp[i] += 5
	}
	cases := []struct {
		name string
		data []float64
		step int
	}{
		{"clean step up", up, 20},
		{"clean step down", down, 20},
		{"noisy step up", noisyUp, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx, ok := FindShift(c.data, 0, 0)
			if !ok || idx < c.step || idx > c.step+3 {
				t.Fatalf("want shift in [%d,%d], got idx=%d ok=%v", c.step, c.step+3, idx, ok)
			}
		})
	}
}

func TestFindShift_SlowDriftDetected(t *testing.T) {
	ramp := make([]float64, 100)
	for i := range ramp {
		ramp[i] = 10 + 0.1*float64(i)
	}
	if _, ok := FindShift(ramp, 0, 0); !ok {
		t.Fatal("slow drift should be detected")
	}
}

func TestFindShift_NoShiftCases(t *testing.T) {
	withNaN := cpFlat(20, 10)
	withNaN[7] = math.NaN()
	cases := []struct {
		name string
		data []float64
	}{
		{"constant series", cpFlat(40, 10)},
		{"too short", cpFlat(9, 10)},
		{"empty", nil},
		{"contains NaN", withNaN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if idx, ok := FindShift(c.data, 0, 0); ok {
				t.Fatalf("expected no shift, got index %d", idx)
			}
		})
	}
}

func TestFindShift_DoesNotMutateInput(t *testing.T) {
	data := append(cpFlat(20, 10), cpFlat(20, 15)...)
	orig := append([]float64(nil), data...)
	FindShift(data, 0, 0)
	for i := range data {
		if data[i] != orig[i] {
			t.Fatalf("input modified at %d", i)
		}
	}
}

func TestFindShift_PureNoiseFalseAlarmRate(t *testing.T) {
	const runs = 1000
	for _, h := range []float64{8, 10, 12} {
		alarms := 0
		for seed := int64(1); seed <= runs; seed++ {
			if _, ok := FindShift(cpNoisy(seed, 100, 10, 1), 0.5, h); ok {
				alarms++
			}
		}
		t.Logf("h=%.0f false alarms: %d/%d (%.1f%%)", h, alarms, runs, 100*float64(alarms)/runs)
	}
	alarms := 0
	for seed := int64(1); seed <= runs; seed++ {
		if _, ok := FindShift(cpNoisy(seed, 100, 10, 1), 0, 0); ok {
			alarms++
		}
	}
	if rate := float64(alarms) / runs; rate > 0.02 {
		t.Fatalf("default false-alarm rate %.1f%% exceeds 2%%", 100*rate)
	}
}
