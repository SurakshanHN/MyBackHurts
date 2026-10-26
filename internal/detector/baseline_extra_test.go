package detector

import (
	"testing"
	"time"
)

// bxSeries builds n samples starting at index startIdx, 10s apart, with a
// small deterministic wobble so the fit has non-zero residual std.
func bxSeries(n, startIdx int, base time.Time, scale float64) []Sample {
	out := make([]Sample, 0, n)
	for i := startIdx; i < startIdx+n; i++ {
		p := 100 + 0.1*float64(i) + float64(i%2)*2
		out = append(out, Sample{
			T:        base.Add(time.Duration(i) * 10 * time.Second),
			Replicas: 3, Ready: 3, RPS: 100, P99Ms: p * scale,
		})
	}
	return out
}

func TestFitMetric_IgnoresSamplesAtOrAfterBefore(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pre := bxSeries(30, 0, base, 1)
	post := bxSeries(30, 30, base, 100) // first post sample sits exactly at `before`
	all := append(append([]Sample{}, pre...), post...)
	before := base.Add(300 * time.Second)
	ex := func(s Sample) float64 { return s.P99Ms }

	fPre, ok1 := FitMetric(pre, ex, before)
	fAll, ok2 := FitMetric(all, ex, before)
	if !ok1 || !ok2 {
		t.Fatalf("fits should succeed: ok1=%v ok2=%v", ok1, ok2)
	}
	if fPre != fAll {
		t.Fatalf("post-cutoff samples leaked into the fit:\npre=%+v\nall=%+v", fPre, fAll)
	}
}

func TestPredict_IntervalWidensAwayFromMean(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pre := bxSeries(30, 0, base, 1)
	before := base.Add(300 * time.Second)
	f, ok := FitMetric(pre, func(s Sample) float64 { return s.P99Ms }, before)
	if !ok {
		t.Fatal("fit should succeed")
	}
	tMean := f.T0.Add(time.Duration(f.XMean * float64(time.Second)))
	tFar := base.Add(3000 * time.Second)

	_, loM, upM := f.Predict(tMean, 1.96)
	_, loF, upF := f.Predict(tFar, 1.96)
	wMean, wFar := upM-loM, upF-loF
	if !(wMean > 0) {
		t.Fatalf("interval at the mean should have positive width, got %v", wMean)
	}
	if !(wFar > wMean) {
		t.Fatalf("interval must widen away from the mean: mean=%v far=%v", wMean, wFar)
	}
}
