// Package metrics defines how time-series data is fetched for detectors.
// It only imports detector.Sample and detector.DepSample; never detector internals.
package metrics

import (
	"context"
	"errors"
	"time"

	"github.com/prescale/prescale/internal/detector"
)

// ErrStale is returned when the most-recent point is older than 2·step before `to`.
var ErrStale = errors.New("metrics: stale data")

// ErrNoData is returned when no usable points remain after dropping NaN/Inf.
var ErrNoData = errors.New("metrics: no data")

// Target identifies a workload whose metrics we want.
type Target struct {
	Namespace string
	Name      string
}

// DepRef identifies a downstream dependency of a Target.
type DepRef struct{ Name string }

// Source is the single interface all metric back-ends implement.
type Source interface {
	// Series returns service-level samples for the given Target in [from, to]
	// sampled at `step` intervals.
	Series(ctx context.Context, t Target, from, to time.Time, step time.Duration) ([]detector.Sample, error)

	// DepSeries returns dependency-level samples for the given DepRef in [from, to].
	DepSeries(ctx context.Context, t Target, d DepRef, from, to time.Time, step time.Duration) ([]detector.DepSample, error)
}
