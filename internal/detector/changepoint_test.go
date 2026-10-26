package detector

import (
    "math"
    "testing"
)

func TestFindShift(t *testing.T) {
    const steps = 10
    base := 100.0
    // step up: value jumps +50 after 5 points
    up := make([]float64, steps)
    for i := range up {
        if i < 5 {
            up[i] = base
        } else {
            up[i] = base + 50
        }
    }
    idx, ok := FindShift(up, 0, 0)
    if !ok || idx != 5 {
        t.Fatalf("expected shift at 5 got %d ok=%v", idx, ok)
    }
    // step down
    down := make([]float64, steps)
    for i := range down {
        if i < 5 {
            down[i] = base + 50
        } else {
            down[i] = base
        }
    }
    idx, ok = FindShift(down, 0, 0)
    if !ok || idx != 5 {
        t.Fatalf("expected shift at 5 down got %d ok=%v", idx, ok)
    }
    // gradual drift beyond threshold
    drift := make([]float64, steps)
    for i := range drift {
        drift[i] = base + float64(i)
    }
    idx, ok = FindShift(drift, 0, 0)
    if !ok {
        t.Fatalf("expected drift detection")
    }
    // pure noise - should not find
    noise := make([]float64, steps)
    for i := range noise {
        noise[i] = base + 0.1*float64(i)
    }
    _, ok = FindShift(noise, 0, 0)
    if ok {
        t.Fatalf("noise should not trigger")
    }
    // constant series - false
    constSeries := make([]float64, steps)
    for i := range constSeries {
        constSeries[i] = base
    }
    _, ok = FindShift(constSeries, 0, 0)
    if ok {
        t.Fatalf("constant should not trigger")
    }
    // too short
    short := []float64{base, base + 10}
    _, ok = FindShift(short, 0, 0)
    if ok {
        t.Fatalf("short slice should not trigger")
    }
    // NaN value
    nanVals := []float64{base, math.NaN(), base}
    _, ok = FindShift(nanVals, 0, 0)
    if ok {
        t.Fatalf("NaN should fail")
    }
    // input immutability check
    orig := make([]float64, steps)
    copy(orig, up)
    _, _ = FindShift(up, 0, 0)
    for i := range up {
        if up[i] != orig[i] {
            t.Fatalf("input slice modified at %d", i)
        }
    }
}
