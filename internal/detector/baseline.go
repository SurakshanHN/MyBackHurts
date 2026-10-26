// internal/detector/baseline.go
//
// This file provides a very small, pure‑Go implementation of ordinary
// least‑squares linear regression.  The implementation is intentionally
// trivial – no external packages are imported – and it is sufficient for
// the needs of the detector package.  The core goal is to allow the
// detector to project what a metric (P99Ms or ErrRate) *might have* been in
// the “counterfactual” world had the scale‑up not happened.
//
// The public helpers are:
//   * `fitLinear` – returns `slope`, `intercept` and a boolean indicating
//     success.  A fit is successful only if there are at least two
//     sample points.  It never panics.
//   * `projectMetric` – given a slice of `Sample`, a function that extracts
//     the metric of interest, a start time, and a window, returns the
//     estimated metric at evenly spaced future times.
//
// The implementation uses basic arithmetic from the standard library.
// All time arithmetic is performed in seconds using a `float64`.

package detector

import (
    "math"
    "time"
)

// fitLinear performs an ordinary least‑squares fit of y = m*x + b using
// the supplied x and y slices.  The two slices must be the same length.
// The function returns (m, b, ok).  If `ok` is false, the input was too
// small for a meaningful fit and both coefficients are zero.
//
// The implementation follows the textbook formulas for slope and
// intercept.  It does not attempt to guard against pathological numeric
// behaviour beyond the smallest required checks.
func fitLinear(x []float64, y []float64) (slope, intercept float64, ok bool) {
    n := len(x)
    if n < 2 || len(y) != n {
        return 0, 0, false
    }
    // Compute means
    var sumX, sumY float64
    for i := 0; i < n; i++ {
        sumX += x[i]
        sumY += y[i]
    }
    meanX := sumX / float64(n)
    meanY := sumY / float64(n)

    var num, denom float64
    for i := 0; i < n; i++ {
        dx := x[i] - meanX
        dy := y[i] - meanY
        num += dx * dy
        denom += dx * dx
    }
    if denom == 0 {
        // All x values identical – slope undefined.
        return 0, meanY, false
    }
    slope = num / denom
    intercept = meanY - slope*meanX
    return slope, intercept, true
}

// projectMetric returns `points` predicted values for the metric extracted
// from each sample.  The predictions are spaced evenly from `now` to
// `now+window`.  The coefficient of the fit is computed based on the
// timestamps of the samples relative to the first sample.
//
// If the regression could not be performed (less than two samples) or if
// `points` <= 0, a slice of the appropriate length containing zeros is
// returned.
func projectMetric(samples []Sample, metricExtractor func(Sample) float64, now time.Time, window time.Duration, points int) []float64 {
    if points <= 0 {
        return make([]float64, 0)
    }
    // Translate timestamps to seconds from the first sample.
    if len(samples) == 0 {
        return make([]float64, points)
    }
    t0 := samples[0].T
    x := make([]float64, len(samples))
    y := make([]float64, len(samples))
    for i, s := range samples {
        x[i] = t0.Sub(s.T).Seconds() * -1 // elapsed positive
        y[i] = metricExtractor(s)
    }
    slope, intercept, ok := fitLinear(x, y)
    step := window.Seconds() / float64(points-1)
    preds := make([]float64, points)
    for i := 0; i < points; i++ {
        if !ok {
            preds[i] = 0
            continue
        }
        elapsed := step * float64(i)
        preds[i] = slope*elapsed + intercept
        if preds[i] < 0 {
            preds[i] = 0
        }
    }
    return preds
}

// ProjectP99Ms projects the expected p99 latency over the given post‑scale‑up window
// based on pre‑scale‑up samples.
func ProjectP99Ms(samples []Sample, now time.Time, window time.Duration, points int) []float64 {
    return projectMetric(samples, func(s Sample) float64 { return s.P99Ms }, now, window, points)
}

// ProjectErrRate projects the expected error rate over the given window.
func ProjectErrRate(samples []Sample, now time.Time, window time.Duration, points int) []float64 {
    return projectMetric(samples, func(s Sample) float64 { return s.ErrRate }, now, window, points)
}

// Fit captures the parameters of a linear regression fit of a metric
// over time.  The slice of Sample points used in the fit are restricted
// to those with T < before.  T0 is the time of the first used sample and
// X is measured in seconds since T0.
//
// The fit is only considered successful (ok==true) if there are at least
// three samples, the design matrix has non‑zero variance in x, all
// numeric values are finite, and the regression algorithm succeeds.
//
// `Fit` is deliberately small; it only stores the quantities that are
// needed for confidence interval prediction.
type Fit struct {
    Slope, Intercept, ResidStd, XMean, Sxx float64
    N  int
    T0 time.Time // x = seconds since T0
}

// FitMetric performs a linear regression of the metric produced by
// `extract` over the subset of `samples` that occur before `before`.
// The function returns a `Fit` containing the regression parameters
// and a boolean indicating whether the fit was successful.
func FitMetric(samples []Sample, extract func(Sample) float64, before time.Time) (Fit, bool) {
    // Determine the sample window and collect x,y.
    var xs, ys []float64
    var t0 time.Time
    for _, s := range samples {
        if !s.T.Before(before) {
            continue
        }
        if t0.IsZero() {
            t0 = s.T
        }
        // compute x in seconds since t0
        x := s.T.Sub(t0).Seconds()
        y := extract(s)
        if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
            return Fit{}, false
        }
        xs = append(xs, x)
        ys = append(ys, y)
    }
    n := len(xs)
    if n < 3 {
        return Fit{}, false
    }
    // Compute Sxx and meanX for later use.
    var sumX float64
    for _, x := range xs { sumX += x }
    meanX := sumX / float64(n)
    var sxx float64
    for _, x := range xs { d := x - meanX; sxx += d * d }
    if sxx == 0 {
        return Fit{}, false
    }
    slope, intercept, ok := fitLinear(xs, ys)
    if !ok {
        return Fit{}, false
    }
    // Compute residual standard deviation.
    var rsq float64
    for i := 0; i < n; i++ {
        pred := intercept + slope*xs[i]
        d := ys[i] - pred
        rsq += d * d
    }
    residStd := math.Sqrt(rsq / float64(n-2))
    f := Fit{Slope: slope, Intercept: intercept, ResidStd: residStd,
        XMean: meanX, Sxx: sxx, N: n, T0: t0}
    return f, true
}

// Predict returns the mean, lower and upper prediction bounds for the
// metric at time `t`.  `z` specifies the number of standard deviations
// for the interval.  If z <= 0, a default of 1.96 (95% confidence) is
// used.  If the underlying fit had fewer than three points (f.N < 3) the
// function returns NaN for all three outputs.
func (f Fit) Predict(t time.Time, z float64) (mean, lower, upper float64) {
    if f.N < 3 {
        return math.NaN(), math.NaN(), math.NaN()
    }
    if z <= 0 {
        z = 1.96
    }
    // x is seconds since T0.
    x := t.Sub(f.T0).Seconds()
    mean = f.Intercept + f.Slope*x
    // half width of the prediction interval
    var half float64
    if f.Sxx != 0 {
        half = z * f.ResidStd * math.Sqrt(1+1/float64(f.N)+(x-f.XMean)*(x-f.XMean)/f.Sxx)
    } else {
        half = 0
    }
    lower = mean - half
    upper = mean + half
    return
}
