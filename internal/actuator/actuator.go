package actuator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AnnOriginalMax stores the MaxReplicas that was in place before any cap.
	AnnOriginalMax = "prescale.io/original-max-replicas"

	// AnnCappedUntil stores the RFC3339 time when the cap expires.
	AnnCappedUntil = "prescale.io/capped-until"

	// AnnCappedBy stores the owner that applied the cap.
	AnnCappedBy = "prescale.io/capped-by"
)

// Actuator applies and reverses HPA replica caps in Kubernetes.
type Actuator struct {
	Client client.Client
	Now    func() time.Time
}

func (a *Actuator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Cap evaluates a proposed replica cap for the given HPA key and, if the plan
// is "cap", writes the new maxReplicas and all three annotations in a single
// Update. Conflicts are returned as errors and never retried.
func (a *Actuator) Cap(ctx context.Context, key types.NamespacedName, proposed, floor int32, ttl time.Duration, owner string) (Plan, error) {
	var hpa autoscalingv2.HorizontalPodAutoscaler
	if err := a.Client.Get(ctx, key, &hpa); err != nil {
		return Plan{}, err
	}

	minReplicas := int32(1)
	if hpa.Spec.MinReplicas != nil {
		minReplicas = *hpa.Spec.MinReplicas
	}

	var origMax int32
	if raw, ok := hpa.Annotations[AnnOriginalMax]; ok {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return Plan{Action: "refuse", Why: fmt.Sprintf("malformed %s annotation: %v", AnnOriginalMax, err)}, nil
		}
		origMax = int32(v)
	}

	state := HPAState{
		MinReplicas:     minReplicas,
		MaxReplicas:     hpa.Spec.MaxReplicas,
		CurrentReplicas: hpa.Status.CurrentReplicas,
		OriginalMax:     origMax,
	}
	plan := PlanCap(state, proposed, floor)

	if plan.Action == "cap" {
		if hpa.Annotations == nil {
			hpa.Annotations = make(map[string]string)
		}
		if plan.SaveOriginal > 0 {
			hpa.Annotations[AnnOriginalMax] = strconv.Itoa(int(plan.SaveOriginal))
		}
		hpa.Annotations[AnnCappedUntil] = a.now().Add(ttl).UTC().Format(time.RFC3339)
		hpa.Annotations[AnnCappedBy] = owner
		hpa.Spec.MaxReplicas = plan.NewMax
		// Never touch Spec.MinReplicas.
		if err := a.Client.Update(ctx, &hpa); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// Restore reverses a previous cap by restoring OriginalMax and removing all
// three annotations in a single Update. If the HPA is not found the call
// returns a noop plan and a nil error.
func (a *Actuator) Restore(ctx context.Context, key types.NamespacedName) (Plan, error) {
	var hpa autoscalingv2.HorizontalPodAutoscaler
	if err := a.Client.Get(ctx, key, &hpa); err != nil {
		if apierrors.IsNotFound(err) {
			return Plan{Action: "noop", Why: "HPA not found"}, nil
		}
		return Plan{}, err
	}

	minReplicas := int32(1)
	if hpa.Spec.MinReplicas != nil {
		minReplicas = *hpa.Spec.MinReplicas
	}

	var origMax int32
	if raw, ok := hpa.Annotations[AnnOriginalMax]; ok {
		v, _ := strconv.ParseInt(raw, 10, 32) // best-effort; 0 if malformed → noop
		origMax = int32(v)
	}

	state := HPAState{
		MinReplicas:     minReplicas,
		MaxReplicas:     hpa.Spec.MaxReplicas,
		CurrentReplicas: hpa.Status.CurrentReplicas,
		OriginalMax:     origMax,
	}
	plan := PlanRestore(state)

	if plan.Action == "restore" {
		hpa.Spec.MaxReplicas = plan.NewMax
		if hpa.Annotations != nil {
			delete(hpa.Annotations, AnnOriginalMax)
			delete(hpa.Annotations, AnnCappedUntil)
			delete(hpa.Annotations, AnnCappedBy)
		}
		// Never touch Spec.MinReplicas.
		if err := a.Client.Update(ctx, &hpa); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// Expired reports whether the capped-until TTL has passed. A missing or
// malformed timestamp on a capped HPA is treated as expired (fail-safe toward
// restoring).
func (a *Actuator) Expired(ctx context.Context, key types.NamespacedName) (bool, error) {
	var hpa autoscalingv2.HorizontalPodAutoscaler
	if err := a.Client.Get(ctx, key, &hpa); err != nil {
		return false, err
	}

	// Not capped at all.
	if _, capped := hpa.Annotations[AnnOriginalMax]; !capped {
		return false, nil
	}

	raw, ok := hpa.Annotations[AnnCappedUntil]
	if !ok || raw == "" {
		// Capped but timestamp missing → treat as expired.
		return true, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// Malformed timestamp → treat as expired.
		return true, nil
	}
	return !a.now().Before(t), nil
}
