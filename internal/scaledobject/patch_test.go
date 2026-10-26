package scaledobject_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/prescale/prescale/internal/scaledobject"
)

var soGVR = schema.GroupVersionResource{Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects"}

func newFakeScaledObject(ns, name string, minReplicas int64, annotations map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObject",
	})
	obj.SetName(name)
	obj.SetNamespace(ns)
	if annotations != nil {
		obj.SetAnnotations(annotations)
	}
	obj.Object["spec"] = map[string]interface{}{
		"minReplicaCount": minReplicas,
		"maxReplicaCount": int64(10),
	}
	return obj
}

func newFakeClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		soGVR: "ScaledObjectList",
	}, objs...)
}

func TestGetInfo_ExtractsStateAndAnnotations(t *testing.T) {
	annos := map[string]string{
		scaledobject.AnnotationOriginalMin:  "3",
		scaledobject.AnnotationBoostedUntil: "2026-10-26T12:00:00Z",
	}
	so := newFakeScaledObject("predictive", "checkout", 6, annos)
	fc := newFakeClient(so)

	p := &scaledobject.Patcher{
		Client:           fc,
		AllowedNamespace: "predictive",
		AllowedName:      "checkout",
	}

	st, err := p.GetInfo(context.Background(), "predictive", "checkout")
	if err != nil {
		t.Fatalf("GetInfo failed: %v", err)
	}

	if st.CurrentMinReplicas != 6 {
		t.Errorf("CurrentMinReplicas: got %d, want 6", st.CurrentMinReplicas)
	}
	if st.MaxReplicaCount != 10 {
		t.Errorf("MaxReplicaCount: got %d, want 10", st.MaxReplicaCount)
	}
	if st.OriginalMin != 3 {
		t.Errorf("OriginalMin: got %d, want 3", st.OriginalMin)
	}
	expectedTime, _ := time.Parse(time.RFC3339, "2026-10-26T12:00:00Z")
	if !st.BoostedUntil.Equal(expectedTime) {
		t.Errorf("BoostedUntil: got %v, want %v", st.BoostedUntil, expectedTime)
	}
}

func TestBuildPatch_InitialBoost(t *testing.T) {
	until := time.Date(2026, 10, 26, 12, 1, 0, 0, time.UTC)
	plan := scaledobject.Plan{
		Action:          "raise",
		NewMin:          6,
		SaveOriginal:    3,
		NewBoostedUntil: until,
	}

	raw, err := scaledobject.BuildPatch(plan)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}

	meta := parsed["metadata"].(map[string]interface{})
	annos := meta["annotations"].(map[string]interface{})
	if annos[scaledobject.AnnotationOriginalMin] != "3" {
		t.Errorf("AnnotationOriginalMin: got %v, want 3", annos[scaledobject.AnnotationOriginalMin])
	}
	if annos[scaledobject.AnnotationBoostedUntil] != "2026-10-26T12:01:00Z" {
		t.Errorf("AnnotationBoostedUntil: got %v, want 2026-10-26T12:01:00Z", annos[scaledobject.AnnotationBoostedUntil])
	}

	spec := parsed["spec"].(map[string]interface{})
	if int32(spec["minReplicaCount"].(float64)) != 6 {
		t.Errorf("minReplicaCount: got %v, want 6", spec["minReplicaCount"])
	}
}

func TestBuildPatch_ReleaseSetsNullAnnotations(t *testing.T) {
	plan := scaledobject.Plan{
		Action:           "release",
		NewMin:           3,
		ClearAnnotations: true,
	}

	raw, err := scaledobject.BuildPatch(plan)
	if err != nil {
		t.Fatalf("BuildPatch: %v", err)
	}

	rawStr := string(raw)
	if !strings.Contains(rawStr, `"prescale.io/original-min-replicas":null`) {
		t.Errorf("expected null original-min-replicas, got %s", rawStr)
	}
	if !strings.Contains(rawStr, `"prescale.io/boosted-until":null`) {
		t.Errorf("expected null boosted-until, got %s", rawStr)
	}
}

func TestPatcher_NamespaceGuard(t *testing.T) {
	fc := newFakeClient()
	p := &scaledobject.Patcher{
		Client:           fc,
		AllowedNamespace: "predictive",
		AllowedName:      "checkout",
	}

	// Target outside allowed namespace (e.g. baseline)
	_, err := p.Execute(context.Background(), "baseline", "checkout", scaledobject.Plan{Action: "raise", NewMin: 5}, false)
	if err == nil {
		t.Fatal("expected error when targeting forbidden namespace")
	}

	// Target outside allowed name
	_, err = p.Execute(context.Background(), "predictive", "other", scaledobject.Plan{Action: "raise", NewMin: 5}, false)
	if err == nil {
		t.Fatal("expected error when targeting forbidden name")
	}

	// GetInfo outside allowed namespace
	_, err = p.GetInfo(context.Background(), "baseline", "checkout")
	if err == nil {
		t.Fatal("expected error from GetInfo on forbidden namespace")
	}
}

func TestPatcher_DryRunDoesNotSendPatch(t *testing.T) {
	so := newFakeScaledObject("predictive", "checkout", 3, nil)
	fc := newFakeClient(so)
	p := &scaledobject.Patcher{
		Client:           fc,
		AllowedNamespace: "predictive",
		AllowedName:      "checkout",
	}

	plan := scaledobject.Plan{Action: "raise", NewMin: 6, SaveOriginal: 3, NewBoostedUntil: time.Now().Add(60 * time.Second)}
	patchBytes, err := p.Execute(context.Background(), "predictive", "checkout", plan, true)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if patchBytes == nil {
		t.Fatal("expected non-nil patch bytes in dry-run")
	}

	// Verify no Patch actions were recorded in fake client
	for _, action := range fc.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatalf("unexpected patch action sent during dry-run: %v", action)
		}
	}
}
