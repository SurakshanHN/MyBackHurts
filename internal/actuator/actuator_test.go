package actuator

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// ---- helpers ---------------------------------------------------------------

func acScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = autoscalingv2.AddToScheme(s)
	return s
}

func acHPA(ns, name string, min, max, cur int32, anns map[string]string) *autoscalingv2.HorizontalPodAutoscaler {
	minPtr := &min
	h := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ns,
			Name:        name,
			Annotations: anns,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: minPtr,
			MaxReplicas: max,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			CurrentReplicas: cur,
		},
	}
	return h
}

func acActuator(c client.Client, now time.Time) *Actuator {
	return &Actuator{Client: c, Now: func() time.Time { return now }}
}

func acFetch(t *testing.T, c client.Client, ns, name string) *autoscalingv2.HorizontalPodAutoscaler {
	t.Helper()
	var h autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &h); err != nil {
		t.Fatalf("fetch HPA: %v", err)
	}
	return &h
}

// ---- tests -----------------------------------------------------------------

func TestActuator_CapRestoreRoundTrip(t *testing.T) {
	h := acHPA("ns", "svc", 2, 20, 6, nil)
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	act := acActuator(c, now)

	key := types.NamespacedName{Namespace: "ns", Name: "svc"}

	// Cap
	plan, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "test")
	if err != nil || plan.Action != "cap" || plan.NewMax != 10 {
		t.Fatalf("cap failed: plan=%+v err=%v", plan, err)
	}

	got := acFetch(t, c, "ns", "svc")
	if got.Spec.MaxReplicas != 10 {
		t.Errorf("MaxReplicas not written: want 10 got %d", got.Spec.MaxReplicas)
	}
	if got.Annotations[AnnOriginalMax] != "20" {
		t.Errorf("OriginalMax annotation wrong: %q", got.Annotations[AnnOriginalMax])
	}
	expectedUntil := now.Add(time.Hour).UTC().Format(time.RFC3339)
	if got.Annotations[AnnCappedUntil] != expectedUntil {
		t.Errorf("CappedUntil wrong: want %q got %q", expectedUntil, got.Annotations[AnnCappedUntil])
	}
	if got.Annotations[AnnCappedBy] != "test" {
		t.Errorf("CappedBy wrong: %q", got.Annotations[AnnCappedBy])
	}
	// minReplicas untouched
	if *got.Spec.MinReplicas != 2 {
		t.Errorf("MinReplicas touched: %d", *got.Spec.MinReplicas)
	}

	// Restore
	rp, err := act.Restore(context.Background(), key)
	if err != nil || rp.Action != "restore" || rp.NewMax != 20 {
		t.Fatalf("restore failed: plan=%+v err=%v", rp, err)
	}
	got2 := acFetch(t, c, "ns", "svc")
	if got2.Spec.MaxReplicas != 20 {
		t.Errorf("after restore MaxReplicas want 20 got %d", got2.Spec.MaxReplicas)
	}
	for _, ann := range []string{AnnOriginalMax, AnnCappedUntil, AnnCappedBy} {
		if _, ok := got2.Annotations[ann]; ok {
			t.Errorf("annotation %s not removed", ann)
		}
	}
}

func TestActuator_CapIdempotent(t *testing.T) {
	h := acHPA("ns", "svc", 2, 20, 5, nil)
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}

	p1, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	if err != nil || p1.Action != "cap" {
		t.Fatalf("first cap: %+v %v", p1, err)
	}
	p2, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	if err != nil || p2.Action != "noop" {
		t.Fatalf("second cap should be noop: %+v %v", p2, err)
	}
}

func TestActuator_NeverBelowCurrentReplicas(t *testing.T) {
	// current=8, proposed=3 → must clamp NewMax to at least 8
	h := acHPA("ns", "svc", 2, 20, 8, nil)
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}
	p, err := act.Cap(context.Background(), key, 3, 1, time.Hour, "owner")
	if err != nil {
		t.Fatalf("cap error: %v", err)
	}
	if p.NewMax < 8 {
		t.Errorf("NewMax %d below CurrentReplicas 8", p.NewMax)
	}
	got := acFetch(t, c, "ns", "svc")
	if got.Spec.MaxReplicas < 8 {
		t.Errorf("written MaxReplicas %d below CurrentReplicas 8", got.Spec.MaxReplicas)
	}
}

func TestActuator_MinReplicasUntouched(t *testing.T) {
	h := acHPA("ns", "svc", 3, 20, 5, nil)
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}
	_, _ = act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	got := acFetch(t, c, "ns", "svc")
	if got.Spec.MinReplicas == nil || *got.Spec.MinReplicas != 3 {
		t.Errorf("MinReplicas changed: %v", got.Spec.MinReplicas)
	}
}

func TestActuator_HPANotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(acScheme()).Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "missing"}

	// Cap on missing HPA → not-found error (propagated)
	_, err := act.Cap(context.Background(), key, 5, 1, time.Hour, "owner")
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound from Cap, got %v", err)
	}

	// Restore on missing HPA → noop, nil error
	p, err := act.Restore(context.Background(), key)
	if err != nil || p.Action != "noop" {
		t.Errorf("expected noop on missing HPA restore: plan=%+v err=%v", p, err)
	}
}

func TestActuator_MalformedAnnotation(t *testing.T) {
	h := acHPA("ns", "svc", 2, 20, 5, map[string]string{
		AnnOriginalMax: "not-a-number",
	})
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}

	p, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Action != "refuse" {
		t.Errorf("expected refuse on malformed annotation, got %q", p.Action)
	}
}

func TestActuator_Expired_PastFuture(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute).UTC().Format(time.RFC3339)
	future := now.Add(time.Hour).UTC().Format(time.RFC3339)

	for _, tc := range []struct {
		name  string
		until string
		want  bool
	}{
		{"past", past, true},
		{"future", future, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := acHPA("ns", "svc", 2, 10, 3, map[string]string{
				AnnOriginalMax: "20",
				AnnCappedUntil: tc.until,
			})
			c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
			act := acActuator(c, now)
			exp, err := act.Expired(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if exp != tc.want {
				t.Errorf("want %v got %v", tc.want, exp)
			}
		})
	}
}

func TestActuator_Expired_MalformedTimestamp(t *testing.T) {
	h := acHPA("ns", "svc", 2, 10, 3, map[string]string{
		AnnOriginalMax: "20",
		AnnCappedUntil: "garbage",
	})
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	exp, err := act.Expired(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exp {
		t.Error("malformed timestamp should count as expired")
	}
}

func TestActuator_Expired_MissingTimestamp(t *testing.T) {
	h := acHPA("ns", "svc", 2, 10, 3, map[string]string{
		AnnOriginalMax: "20",
		// no AnnCappedUntil
	})
	c := fake.NewClientBuilder().WithScheme(acScheme()).WithObjects(h).Build()
	act := acActuator(c, time.Now())
	exp, err := act.Expired(context.Background(), types.NamespacedName{Namespace: "ns", Name: "svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exp {
		t.Error("missing timestamp should count as expired")
	}
}

func TestActuator_ConflictPropagates(t *testing.T) {
	h := acHPA("ns", "svc", 2, 20, 5, nil)
	conflictErr := apierrors.NewConflict(
		schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"},
		"svc", errors.New("conflict"),
	)
	c := fake.NewClientBuilder().
		WithScheme(acScheme()).
		WithObjects(h).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, cli client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				return conflictErr
			},
		}).
		Build()
	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}

	_, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	if !apierrors.IsConflict(err) {
		t.Errorf("expected conflict error, got %v", err)
	}
}

func TestActuator_AnnotationsWrittenWithMaxReplicas(t *testing.T) {
	// Verify all three annotations are written in the same update as MaxReplicas.
	h := acHPA("ns", "svc", 2, 20, 5, nil)
	updateCalled := 0
	var capturedMax int32
	var capturedAnns map[string]string

	c := fake.NewClientBuilder().
		WithScheme(acScheme()).
		WithObjects(h).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, cli client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalled++
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				capturedMax = hpa.Spec.MaxReplicas
				capturedAnns = make(map[string]string)
				for k, v := range hpa.Annotations {
					capturedAnns[k] = v
				}
				// Let the real fake client do the write
				return cli.Update(ctx, obj, opts...)
			},
		}).
		Build()

	act := acActuator(c, time.Now())
	key := types.NamespacedName{Namespace: "ns", Name: "svc"}
	_, err := act.Cap(context.Background(), key, 10, 1, time.Hour, "owner")
	if err != nil {
		t.Fatalf("cap error: %v", err)
	}
	if updateCalled != 1 {
		t.Errorf("expected exactly 1 Update call, got %d", updateCalled)
	}
	if capturedMax != 10 {
		t.Errorf("MaxReplicas not set in update: %d", capturedMax)
	}
	if capturedAnns[AnnOriginalMax] != strconv.Itoa(20) {
		t.Errorf("AnnOriginalMax missing or wrong in update")
	}
	if capturedAnns[AnnCappedBy] != "owner" {
		t.Errorf("AnnCappedBy missing in update")
	}
	if capturedAnns[AnnCappedUntil] == "" {
		t.Errorf("AnnCappedUntil missing in update")
	}
}
