package actuator

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
)

const (
	AnnoOriginalMax = "prescale.io/original-max-replicas"
	AnnoCappedBy    = "prescale.io/capped-by"
)

type HPAActuator interface {
	Cap(ctx context.Context, hpa types.NamespacedName, newMax, floor int32, by string) error
	Restore(ctx context.Context, hpa types.NamespacedName) error
	IsCapped(ctx context.Context, hpa types.NamespacedName) (bool, error)
}
