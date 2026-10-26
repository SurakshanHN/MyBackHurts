package controller_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	autoscalingv1alpha1 "github.com/prescale/prescale/api/v1alpha1"
	"github.com/prescale/prescale/internal/actuator"
	"github.com/prescale/prescale/internal/controller"
	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/metrics"
)

// ---- helpers ---------------------------------------------------------------

func ctScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = autoscalingv2.AddToScheme(s)
	_ = autoscalingv1alpha1.AddToScheme(s)
	return s
}

func ctHPA(ns, name string, min, max, cur int32, anns map[string]string) *autoscalingv2.HorizontalPodAutoscaler {
	if anns == nil {
		anns = make(map[string]string)
	}
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ns,
			Name:        name,
			Annotations: anns,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: &min,
			MaxReplicas: max,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			CurrentReplicas: cur,
		},
	}
}

func ctHarmfulData(scaleUpAt time.Time) ([]detector.Sample, []detector.DepSample) {
	step := 10 * time.Second
	nPre := 10
	nPost := 10

	var samples []detector.Sample
	preStart := scaleUpAt.Add(-time.Duration(nPre) * step)
	for i := 0; i < nPre; i++ {
		samples = append(samples, detector.Sample{
			T:        preStart.Add(time.Duration(i) * step),
			Replicas: 2,
			Ready:    2,
			RPS:      100.0,
			P99Ms:    200.0,
			ErrRate:  0.01,
		})
	}
	for i := 0; i < nPost; i++ {
		samples = append(samples, detector.Sample{
			T:        scaleUpAt.Add(time.Duration(i) * step),
			Replicas: 6,
			Ready:    6,
			RPS:      100.0, // no throughput gain
			P99Ms:    400.0, // significant latency degradation
			ErrRate:  0.01,
		})
	}

	var depSamples []detector.DepSample
	for i := 0; i < nPre; i++ {
		depSamples = append(depSamples, detector.DepSample{
			T:          preStart.Add(time.Duration(i) * step),
			Saturation: 0.5,
			LatencyMs:  100.0,
		})
	}
	for i := 0; i < nPost; i++ {
		depSamples = append(depSamples, detector.DepSample{
			T:          scaleUpAt.Add(time.Duration(i) * step),
			Saturation: 0.5,
			LatencyMs:  200.0, // dependency latency stepped up -> culprit found
		})
	}

	return samples, depSamples
}

// ---- Tests -----------------------------------------------------------------

func TestScaleGuard_ObserveNeverPatchesHPA(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scaleUpAt := now.Add(-5 * time.Minute)

	hpa := ctHPA("default", "svc-hpa", 2, 10, 6, nil)
	zeroSec := 0
	fiveSamples := 5
	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc-guard",
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA:     "svc-hpa",
			Mode:          "observe",
			WarmupSeconds: &zeroSec,
			MinSamples:    &fiveSamples,
			Dependencies:  []string{"db"},
		},
		Status: autoscalingv1alpha1.ScaleGuardStatus{
			LastScaleUpTime:      &metav1.Time{Time: scaleUpAt},
			ScaleUpFrom:          2,
			ScaleUpTo:            6,
			LastObservedReplicas: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	fakeMetrics := metrics.NewFake()
	tgt := metrics.Target{Namespace: "default", Name: "svc-hpa"}
	dep := metrics.DepRef{Name: "db"}
	svcSamples, depSamples := ctHarmfulData(scaleUpAt)
	fakeMetrics.SetSeries(tgt, svcSamples)
	fakeMetrics.SetDepSeries(tgt, dep, depSamples)

	r := &controller.ScaleGuardReconciler{
		Client:  c,
		Scheme:  s,
		Metrics: fakeMetrics,
		Now:     func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Assert HPA was NEVER patched
	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 10 {
		t.Errorf("expected MaxReplicas to remain 10 in observe mode, got %d", fetchedHPA.Spec.MaxReplicas)
	}
	if fetchedHPA.Annotations != nil && fetchedHPA.Annotations[actuator.AnnOriginalMax] != "" {
		t.Errorf("observe mode set AnnOriginalMax: %v", fetchedHPA.Annotations)
	}

	// Assert ScaleDecision was written with RecordOnly
	var decisions autoscalingv1alpha1.ScaleDecisionList
	if err := c.List(context.Background(), &decisions, client.InNamespace("default")); err != nil {
		t.Fatalf("list decisions failed: %v", err)
	}
	if len(decisions.Items) == 0 {
		t.Fatal("expected a ScaleDecision to be created")
	}
	dec := decisions.Items[0]
	if dec.Spec.Action != "RecordOnly" && dec.Spec.AppliedAction != "RecordOnly" {
		t.Errorf("expected ScaleDecision action RecordOnly, got Action=%q AppliedAction=%q", dec.Spec.Action, dec.Spec.AppliedAction)
	}
}

func TestScaleGuard_EnforceHarmfulCapsOnce_SecondReconcileNoop(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scaleUpAt := now.Add(-5 * time.Minute)

	hpa := ctHPA("default", "svc-hpa", 2, 10, 6, nil)
	zeroSec := 0
	fiveSamples := 5
	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc-guard",
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA:     "svc-hpa",
			Mode:          "enforce",
			WarmupSeconds: &zeroSec,
			MinSamples:    &fiveSamples,
			Dependencies:  []string{"db"},
		},
		Status: autoscalingv1alpha1.ScaleGuardStatus{
			LastScaleUpTime:      &metav1.Time{Time: scaleUpAt},
			ScaleUpFrom:          2,
			ScaleUpTo:            6,
			LastObservedReplicas: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	fakeMetrics := metrics.NewFake()
	tgt := metrics.Target{Namespace: "default", Name: "svc-hpa"}
	dep := metrics.DepRef{Name: "db"}
	svcSamples, depSamples := ctHarmfulData(scaleUpAt)
	fakeMetrics.SetSeries(tgt, svcSamples)
	fakeMetrics.SetDepSeries(tgt, dep, depSamples)

	r := &controller.ScaleGuardReconciler{
		Client:  c,
		Scheme:  s,
		Metrics: fakeMetrics,
		Now:     func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}

	// First Reconcile: should cap HPA
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 6 {
		// NewMax clamped to currentReplicas (6) as effFloor
		if fetchedHPA.Spec.MaxReplicas >= 10 {
			t.Errorf("expected MaxReplicas capped below 10, got %d", fetchedHPA.Spec.MaxReplicas)
		}
	}
	if fetchedHPA.Annotations[actuator.AnnOriginalMax] != "10" {
		t.Errorf("expected AnnOriginalMax=10, got %s", fetchedHPA.Annotations[actuator.AnnOriginalMax])
	}

	firstRV := fetchedHPA.ResourceVersion

	// Second Reconcile: should be a complete NO-OP
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	var fetchedHPA2 autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA2); err != nil {
		t.Fatalf("fetch HPA2 failed: %v", err)
	}
	if fetchedHPA2.ResourceVersion != firstRV {
		t.Errorf("second Reconcile modified HPA: RV changed from %s to %s", firstRV, fetchedHPA2.ResourceVersion)
	}
}

func TestScaleGuard_MetricsErrorNoChange(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scaleUpAt := now.Add(-5 * time.Minute)

	hpa := ctHPA("default", "svc-hpa", 2, 10, 6, nil)
	zeroSec := 0
	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc-guard",
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA:     "svc-hpa",
			Mode:          "enforce",
			WarmupSeconds: &zeroSec,
		},
		Status: autoscalingv1alpha1.ScaleGuardStatus{
			LastScaleUpTime:      &metav1.Time{Time: scaleUpAt},
			ScaleUpFrom:          2,
			ScaleUpTo:            6,
			LastObservedReplicas: 2,
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	fakeMetrics := metrics.NewFake()
	fakeMetrics.SeriesErr = errors.New("prometheus connection refused")

	r := &controller.ScaleGuardReconciler{
		Client:  c,
		Scheme:  s,
		Metrics: fakeMetrics,
		Now:     func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 10 {
		t.Errorf("metrics error should cause no change, but MaxReplicas is %d", fetchedHPA.Spec.MaxReplicas)
	}
	if fetchedHPA.Annotations[actuator.AnnOriginalMax] != "" {
		t.Errorf("metrics error should not cap HPA, but AnnOriginalMax is set")
	}
}

func TestScaleGuard_DeleteMidCapRestores(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)

	anns := map[string]string{
		actuator.AnnOriginalMax: "10",
		actuator.AnnCappedUntil: strconv.FormatInt(future.Unix(), 10),
		actuator.AnnCappedBy:    "ScaleGuard/svc-guard",
	}
	hpa := ctHPA("default", "svc-hpa", 2, 5, 5, anns)

	delTime := metav1.NewTime(now)
	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         "default",
			Name:              "svc-guard",
			Finalizers:        []string{controller.FinalizerName},
			DeletionTimestamp: &delTime,
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA: "svc-hpa",
			Mode:      "enforce",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	r := &controller.ScaleGuardReconciler{
		Client: c,
		Scheme: s,
		Now:    func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile on delete failed: %v", err)
	}

	// Assert HPA was restored to 10 and annotations cleared
	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 10 {
		t.Errorf("expected MaxReplicas restored to 10 on delete, got %d", fetchedHPA.Spec.MaxReplicas)
	}
	if fetchedHPA.Annotations[actuator.AnnOriginalMax] != "" {
		t.Errorf("expected AnnOriginalMax cleared on delete")
	}

	// Assert Finalizer was removed from ScaleGuard
	var fetchedSG autoscalingv1alpha1.ScaleGuard
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-guard"}, &fetchedSG); err == nil {
		for _, f := range fetchedSG.Finalizers {
			if f == controller.FinalizerName {
				t.Error("expected finalizer to be removed from ScaleGuard")
			}
		}
	}
}

func TestScaleGuard_ModeSwitchedToObserveMidCapRestores(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)

	anns := map[string]string{
		actuator.AnnOriginalMax: "10",
		actuator.AnnCappedUntil: strconv.FormatInt(future.Unix(), 10),
		actuator.AnnCappedBy:    "ScaleGuard/svc-guard",
	}
	hpa := ctHPA("default", "svc-hpa", 2, 5, 5, anns)

	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc-guard",
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA: "svc-hpa",
			Mode:      "observe", // switched to observe mid-cap
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	r := &controller.ScaleGuardReconciler{
		Client: c,
		Scheme: s,
		Now:    func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 10 {
		t.Errorf("expected MaxReplicas restored to 10 when switched to observe, got %d", fetchedHPA.Spec.MaxReplicas)
	}
	if fetchedHPA.Annotations[actuator.AnnOriginalMax] != "" {
		t.Errorf("expected AnnOriginalMax cleared when switched to observe")
	}
}

func TestScaleGuard_ExpiredCapRestores(t *testing.T) {
	s := ctScheme()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-5 * time.Minute) // expired in the past

	anns := map[string]string{
		actuator.AnnOriginalMax: "10",
		actuator.AnnCappedUntil: strconv.FormatInt(past.Unix(), 10),
		actuator.AnnCappedBy:    "ScaleGuard/svc-guard",
	}
	hpa := ctHPA("default", "svc-hpa", 2, 5, 5, anns)

	sg := &autoscalingv1alpha1.ScaleGuard{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc-guard",
		},
		Spec: autoscalingv1alpha1.ScaleGuardSpec{
			TargetHPA: "svc-hpa",
			Mode:      "enforce",
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(hpa, sg).WithStatusSubresource(&autoscalingv1alpha1.ScaleGuard{}, &autoscalingv2.HorizontalPodAutoscaler{}).Build()

	r := &controller.ScaleGuardReconciler{
		Client: c,
		Scheme: s,
		Now:    func() time.Time { return now },
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "svc-guard"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	var fetchedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "svc-hpa"}, &fetchedHPA); err != nil {
		t.Fatalf("fetch HPA failed: %v", err)
	}
	if fetchedHPA.Spec.MaxReplicas != 10 {
		t.Errorf("expected MaxReplicas restored to 10 when cap expired, got %d", fetchedHPA.Spec.MaxReplicas)
	}
	if fetchedHPA.Annotations[actuator.AnnOriginalMax] != "" {
		t.Errorf("expected AnnOriginalMax cleared when cap expired")
	}
}
