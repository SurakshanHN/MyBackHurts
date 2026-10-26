// Package scaledobject provides helpers to manage KEDA ScaledObject
// minReplicaCount and lease annotations via the Kubernetes dynamic client.
//
// Design rationale: KEDA owns its HPA. If we patch HPA.spec.minReplicas KEDA
// will reconcile it back on the next poll cycle (≤5 s). Instead we patch
// ScaledObject.spec.minReplicaCount which KEDA then mirrors into the HPA it
// manages – a single source of truth.
//
// We use a strategic JSON merge patch on the KEDA v1alpha1 CRD rather than
// importing KEDA's Go types to avoid pulling the entire KEDA module graph into
// this binary.
package scaledobject

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

var scaledObjectGVR = schema.GroupVersionResource{
	Group:    "keda.sh",
	Version:  "v1alpha1",
	Resource: "scaledobjects",
}

// Patcher patches ScaledObject minReplicaCount and lease annotations using the dynamic client.
type Patcher struct {
	Client           dynamic.Interface
	AllowedNamespace string
	AllowedName      string
}

// GetInfo retrieves the current ScaledObject state, including min/max replica counts
// and any prescale lease annotations.
func (p *Patcher) GetInfo(ctx context.Context, namespace, name string) (State, error) {
	if p.AllowedNamespace != "" && namespace != p.AllowedNamespace {
		return State{}, fmt.Errorf("scaledobject: refusal: target namespace %q does not match allowed %q", namespace, p.AllowedNamespace)
	}
	if p.AllowedName != "" && name != p.AllowedName {
		return State{}, fmt.Errorf("scaledobject: refusal: target name %q does not match allowed %q", name, p.AllowedName)
	}

	obj, err := p.Client.Resource(scaledObjectGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return State{}, fmt.Errorf("scaledobject: get %s/%s: %w", namespace, name, err)
	}

	state := State{}

	// Safely navigate spec fields
	if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
		if cur, ok := spec["minReplicaCount"]; ok {
			state.CurrentMinReplicas = toInt32(cur)
		}
		if max, ok := spec["maxReplicaCount"]; ok {
			state.MaxReplicaCount = toInt32(max)
		}
	}

	// Safely navigate metadata annotations
	annotations := obj.GetAnnotations()
	if annotations != nil {
		if origStr, ok := annotations[AnnotationOriginalMin]; ok && origStr != "" {
			if v, err := strconv.Atoi(origStr); err == nil && v > 0 {
				state.OriginalMin = int32(v)
			}
		}
		if untilStr, ok := annotations[AnnotationBoostedUntil]; ok && untilStr != "" {
			if t, err := time.Parse(time.RFC3339, untilStr); err == nil {
				state.BoostedUntil = t
			}
		}
	}

	return state, nil
}

func toInt32(val interface{}) int32 {
	switch v := val.(type) {
	case int64:
		return int32(v)
	case int32:
		return v
	case int:
		return int32(v)
	case float64:
		return int32(v)
	default:
		return 0
	}
}

// BuildPatch constructs the JSON merge-patch document for the given Plan.
func BuildPatch(plan Plan) ([]byte, error) {
	if plan.Action == "noop" || plan.Action == "error" {
		return nil, nil
	}

	specPatch := map[string]interface{}{
		"minReplicaCount": plan.NewMin,
	}

	if plan.ClearAnnotations {
		patch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					AnnotationOriginalMin:  nil,
					AnnotationBoostedUntil: nil,
				},
			},
			"spec": specPatch,
		}
		return json.Marshal(patch)
	}

	annos := map[string]interface{}{
		AnnotationBoostedUntil: plan.NewBoostedUntil.UTC().Format(time.RFC3339),
	}
	if plan.SaveOriginal > 0 {
		annos[AnnotationOriginalMin] = strconv.Itoa(int(plan.SaveOriginal))
	}

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": annos,
		},
		"spec": specPatch,
	}
	return json.Marshal(patch)
}

// Execute validates namespace guards, generates the merge patch, and optionally applies it.
// If dryRun is true, the patch is returned without calling the Kubernetes API.
func (p *Patcher) Execute(ctx context.Context, namespace, name string, plan Plan, dryRun bool) ([]byte, error) {
	if p.AllowedNamespace != "" && namespace != p.AllowedNamespace {
		return nil, fmt.Errorf("scaledobject: refusal: target namespace %q does not match allowed %q", namespace, p.AllowedNamespace)
	}
	if p.AllowedName != "" && name != p.AllowedName {
		return nil, fmt.Errorf("scaledobject: refusal: target name %q does not match allowed %q", name, p.AllowedName)
	}

	patchBytes, err := BuildPatch(plan)
	if err != nil {
		return nil, fmt.Errorf("scaledobject: build patch: %w", err)
	}
	if patchBytes == nil {
		return nil, nil // no patch required (noop / error)
	}

	if dryRun {
		return patchBytes, nil
	}

	_, err = p.Client.Resource(scaledObjectGVR).Namespace(namespace).
		Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return patchBytes, fmt.Errorf("scaledobject: patch %s/%s: %w", namespace, name, err)
	}
	return patchBytes, nil
}

// SetMinReplicas provides a convenience helper for directly setting minReplicaCount.
func (p *Patcher) SetMinReplicas(ctx context.Context, namespace, name string, min int32) error {
	if min < 0 {
		return fmt.Errorf("scaledobject: minReplicaCount must be non-negative, got %d", min)
	}

	plan := Plan{
		Action: "raise",
		NewMin: min,
	}
	_, err := p.Execute(ctx, namespace, name, plan, false)
	return err
}
