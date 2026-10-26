package metrics

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNoData = errors.New("no data")
	ErrStale  = errors.New("data is stale")
)

type Point struct {
	T time.Time
	V float64
}

type Source interface {
	Range(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Point, error)
}
