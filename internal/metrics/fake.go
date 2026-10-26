package metrics

import (
	"context"
	"sort"
	"sync"
	"time"
)

type seriesKey struct{ ns, name string }
type depKey struct{ ns, name, dep string }

// Fake is an in-memory Source for use in tests.
// Load data with Set* methods, optionally inject errors with SeriesErr/DepSeriesErr.
type Fake struct {
	mu           sync.Mutex
	series       map[seriesKey][]Sample
	depSeries    map[depKey][]DepSample
	SeriesErr    error // returned by Series if non-nil
	DepSeriesErr error // returned by DepSeries if non-nil
}

// NewFake constructs an empty Fake.
func NewFake() *Fake {
	return &Fake{
		series:    make(map[seriesKey][]Sample),
		depSeries: make(map[depKey][]DepSample),
	}
}

// SetSeries stores a copy of samples for the given target.
func (f *Fake) SetSeries(t Target, samples []Sample) {
	cp := make([]Sample, len(samples))
	copy(cp, samples)
	f.mu.Lock()
	f.series[seriesKey{t.Namespace, t.Name}] = cp
	f.mu.Unlock()
}

// SetDepSeries stores a copy of dep samples for the given target+dep.
func (f *Fake) SetDepSeries(t Target, d DepRef, samples []DepSample) {
	cp := make([]DepSample, len(samples))
	copy(cp, samples)
	f.mu.Lock()
	f.depSeries[depKey{t.Namespace, t.Name, d.Name}] = cp
	f.mu.Unlock()
}

// Series returns a copy of the samples whose T is in [from, to].
func (f *Fake) Series(ctx context.Context, t Target, from, to time.Time, step time.Duration) ([]Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SeriesErr != nil {
		return nil, f.SeriesErr
	}
	all := f.series[seriesKey{t.Namespace, t.Name}]
	var out []Sample
	for _, s := range all {
		if !s.T.Before(from) && !s.T.After(to) {
			out = append(out, s)
		}
	}
	// return a fresh copy so callers cannot mutate the store
	result := make([]Sample, len(out))
	copy(result, out)
	return result, nil
}

// DepSeries returns a copy of the dep samples whose T is in [from, to].
func (f *Fake) DepSeries(ctx context.Context, t Target, d DepRef, from, to time.Time, step time.Duration) ([]DepSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DepSeriesErr != nil {
		return nil, f.DepSeriesErr
	}
	all := f.depSeries[depKey{t.Namespace, t.Name, d.Name}]
	var out []DepSample
	for _, s := range all {
		if !s.T.Before(from) && !s.T.After(to) {
			out = append(out, s)
		}
	}
	result := make([]DepSample, len(out))
	copy(result, out)
	return result, nil
}

// ensure sort is used (for map-key iteration determinism where needed)
var _ = sort.Strings
