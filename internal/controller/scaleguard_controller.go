package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	autoscalingv1alpha1 "github.com/prescale/prescale/api/v1alpha1"
	"github.com/prescale/prescale/internal/actuator"
	"github.com/prescale/prescale/internal/detector"
	"github.com/prescale/prescale/internal/metrics"
	"github.com/prescale/prescale/internal/report"
)

// FinalizerName is added to ScaleGuard instances to ensure HPA caps are restored on delete.
const FinalizerName = "prescale.io/finalizer"

// ScaleGuardReconciler reconciles a ScaleGuard object.
type ScaleGuardReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Actuator *actuator.Actuator
	Metrics  metrics.Source
	Now      func() time.Time
}

// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaleguards,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaleguards/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaleguards/finalizers,verbs=update
// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaledecisions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaledecisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling.prescale.io,resources=scaledecisions/finalizers,verbs=update
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=get;list;watch;create;update;patch

// Reconcile manages the protection loop for ScaleGuard.
func (r *ScaleGuardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	nowFn := r.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	act := r.Actuator
	if act == nil {
		act = &actuator.Actuator{Client: r.Client, Now: nowFn}
	}

	// 1. Load ScaleGuard
	var sg autoscalingv1alpha1.ScaleGuard
	if err := r.Get(ctx, req.NamespacedName, &sg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	hpaName := sg.GetTargetHPAName()
	hpaKey := types.NamespacedName{Namespace: sg.Namespace, Name: hpaName}

	// 2. Finalizer and Deletion handling
	if !sg.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&sg, FinalizerName) {
			log.Info("Restoring target HPA before finalizer removal", "hpa", hpaKey)
			if _, err := act.Restore(ctx, hpaKey); err != nil {
				log.Error(err, "Failed to restore HPA on delete", "hpa", hpaKey)
			}
			controllerutil.RemoveFinalizer(&sg, FinalizerName)
			if err := r.Update(ctx, &sg); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&sg, FinalizerName) {
		controllerutil.AddFinalizer(&sg, FinalizerName)
		if err := r.Update(ctx, &sg); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 3. Load Target HPA
	var hpa autoscalingv2.HorizontalPodAutoscaler
	if err := r.Get(ctx, hpaKey, &hpa); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 4. Inspect current HPA cap status
	capped := false
	if hpa.Annotations != nil && hpa.Annotations[actuator.AnnOriginalMax] != "" {
		capped = true
	}
	expired := false
	if capped {
		exp, err := act.Expired(ctx, hpaKey)
		if err == nil {
			expired = exp
		}
	}

	mode := sg.GetMode()

	// 5. If mode switched to observe mid-cap or cap has expired, restore immediately
	if (mode == "observe" && capped) || (capped && expired) {
		log.Info("Restoring capped HPA due to mode switch or expiration", "hpa", hpaKey, "mode", mode, "expired", expired)
		if _, err := act.Restore(ctx, hpaKey); err != nil {
			return ctrl.Result{}, err
		}
		sg.Status.Capped = false
		sg.Status.LastAction = "Restore"
		_ = r.updateStatus(ctx, &sg)
		return ctrl.Result{}, nil
	}

	// 6. Detect a new scale-up
	currentReplicas := hpa.Status.CurrentReplicas
	lastRecorded := sg.Status.LastObservedReplicas

	if lastRecorded == 0 {
		var decisionList autoscalingv1alpha1.ScaleDecisionList
		if err := r.List(ctx, &decisionList, client.InNamespace(sg.Namespace)); err == nil {
			for i := range decisionList.Items {
				d := &decisionList.Items[i]
				if (d.Spec.ScaleGuard == sg.Name || d.Spec.TargetHPA == hpaName) && d.Spec.ToReplicas > lastRecorded {
					lastRecorded = d.Spec.ToReplicas
				}
			}
		}
	}

	if lastRecorded == 0 {
		if hpa.Spec.MinReplicas != nil {
			lastRecorded = *hpa.Spec.MinReplicas
		} else {
			lastRecorded = 1
		}
	}

	scaleUpActive := false
	if currentReplicas > lastRecorded {
		scaleUpActive = true
	} else if sg.Status.LastScaleUpTime != nil && sg.Status.ScaleUpTo > 0 {
		scaleUpActive = true
	}

	if !scaleUpActive {
		if sg.Status.LastObservedReplicas != currentReplicas {
			sg.Status.LastObservedReplicas = currentReplicas
			_ = r.updateStatus(ctx, &sg)
		}
		if capped && !expired {
			if untilStr, ok := hpa.Annotations[actuator.AnnCappedUntil]; ok {
				if untilSec, err := strconv.ParseInt(untilStr, 10, 64); err == nil {
					until := time.Unix(untilSec, 0)
					if rem := until.Sub(nowFn()); rem > 0 {
						return ctrl.Result{RequeueAfter: rem}, nil
					}
				}
			}
		}
		return ctrl.Result{}, nil
	}

	// 7. Track the scale-up event
	var scaleUpAt time.Time
	var fromReplicas, toReplicas int32

	if sg.Status.LastScaleUpTime != nil && sg.Status.ScaleUpTo > 0 {
		scaleUpAt = sg.Status.LastScaleUpTime.Time
		fromReplicas = sg.Status.ScaleUpFrom
		toReplicas = sg.Status.ScaleUpTo
	} else {
		scaleUpAt = nowFn()
		fromReplicas = lastRecorded
		toReplicas = currentReplicas
		sg.Status.LastScaleUpTime = &metav1.Time{Time: scaleUpAt}
		sg.Status.ScaleUpFrom = fromReplicas
		sg.Status.ScaleUpTo = toReplicas
		sg.Status.LastObservedReplicas = toReplicas
	}

	// 8. Wait for WarmupSeconds + post-samples duration
	warmupSec := sg.GetWarmupSeconds()
	minSamples := sg.GetMinSamples()
	step := sg.GetStep()
	evalTime := scaleUpAt.Add(time.Duration(warmupSec)*time.Second + time.Duration(minSamples)*step)
	now := nowFn()

	if now.Before(evalTime) {
		rem := evalTime.Sub(now)
		_ = r.updateStatus(ctx, &sg)
		return ctrl.Result{RequeueAfter: rem}, nil
	}

	// 9. Fetch metrics via metrics.Source
	var metricsErr error
	var samples []detector.Sample
	deps := make(map[string][]detector.DepSample)

	if r.Metrics == nil {
		metricsErr = errors.New("metrics: no metrics source configured")
	} else {
		target := metrics.Target{Namespace: sg.Namespace, Name: hpaName}
		from := scaleUpAt.Add(-time.Duration(minSamples*2) * step)
		samples, metricsErr = r.Metrics.Series(ctx, target, from, now, step)

		if metricsErr == nil {
			depList := sg.Spec.Dependencies
			if len(depList) == 0 {
				depList = []string{"dep", "db", "redis"}
			}
			for _, depName := range depList {
				dRef := metrics.DepRef{Name: depName}
				if dSamples, err := r.Metrics.DepSeries(ctx, target, dRef, from, now, step); err == nil && len(dSamples) > 0 {
					deps[depName] = dSamples
				}
			}
		}
	}

	// 10. Call Judge
	cfg := r.buildDetectorConfig(&sg)
	in := detector.Input{
		ScaleUpAt:    scaleUpAt,
		FromReplicas: int(fromReplicas),
		ToReplicas:   int(toReplicas),
		Series:       samples,
		Deps:         deps,
		Cfg:          cfg,
	}

	var res detector.Result
	if metricsErr == nil {
		res = detector.Judge(in)
	} else {
		res = detector.Result{Verdict: detector.Unknown}
	}

	// 11. Call Decide
	floor := sg.GetFloor()
	proposed := fromReplicas
	if proposed <= 0 {
		proposed = floor
	}

	action := Decide(mode, res, capped, expired, metricsErr, floor, proposed)

	// 12. Execute via Actuator
	appliedAction := action.Name()
	ttl := sg.GetTTL()

	switch a := action.(type) {
	case Cap:
		plan, err := act.Cap(ctx, hpaKey, a.Proposed, floor, ttl, "ScaleGuard/"+sg.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		appliedAction = plan.Action
	case actionRestore:
		plan, err := act.Restore(ctx, hpaKey)
		if err != nil {
			return ctrl.Result{}, err
		}
		appliedAction = plan.Action
	case actionRecordOnly:
		appliedAction = "RecordOnly"
	case actionNone:
		appliedAction = "None"
	}

	// 13. Write ScaleDecision
	if res.Verdict != "" || metricsErr != nil {
		decName := fmt.Sprintf("%s-decision-%d", sg.Name, scaleUpAt.Unix())
		decision := &autoscalingv1alpha1.ScaleDecision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      decName,
				Namespace: sg.Namespace,
			},
			Spec: autoscalingv1alpha1.ScaleDecisionSpec{
				TargetHPA:     hpaName,
				ScaleGuard:    sg.Name,
				Verdict:       string(res.Verdict),
				Confidence:    fmt.Sprintf("%.2f", res.Confidence),
				Report:        report.Render(res),
				Action:        action.Name(),
				AppliedAction: appliedAction,
				ScaleUpAt:     &metav1.Time{Time: scaleUpAt},
				FromReplicas:  fromReplicas,
				ToReplicas:    toReplicas,
				ProposedCap:   proposed,
			},
		}
		_ = controllerutil.SetControllerReference(&sg, decision, r.Scheme)
		if err := r.Create(ctx, decision); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "Failed to create ScaleDecision", "name", decName)
		}
	}

	// 14. Update ScaleGuard Status
	sg.Status.LastVerdict = string(res.Verdict)
	sg.Status.LastAction = appliedAction
	sg.Status.LastScaleUpTime = nil
	sg.Status.ScaleUpFrom = 0
	sg.Status.ScaleUpTo = 0
	sg.Status.LastObservedReplicas = toReplicas
	sg.Status.Capped = (action.Name() == "Cap")

	if err := r.updateStatus(ctx, &sg); err != nil {
		return ctrl.Result{}, err
	}

	if action.Name() == "Cap" {
		return ctrl.Result{RequeueAfter: ttl}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ScaleGuardReconciler) updateStatus(ctx context.Context, sg *autoscalingv1alpha1.ScaleGuard) error {
	if err := r.Status().Update(ctx, sg); err != nil {
		return r.Update(ctx, sg)
	}
	return nil
}

func (r *ScaleGuardReconciler) buildDetectorConfig(sg *autoscalingv1alpha1.ScaleGuard) detector.Config {
	cfg := detector.Config{
		MinSamples:        sg.GetMinSamples(),
		HarmMargin:        0.1,
		SaturationPct:     90.0,
		MinConfidence:     0.1,
		WarmupSeconds:     sg.GetWarmupSeconds(),
		MinThroughputGain: 0.05,
	}
	if sg.Spec.HarmMargin != "" {
		if f, err := strconv.ParseFloat(sg.Spec.HarmMargin, 64); err == nil {
			cfg.HarmMargin = f
		}
	}
	if sg.Spec.SaturationPct != "" {
		if f, err := strconv.ParseFloat(sg.Spec.SaturationPct, 64); err == nil {
			cfg.SaturationPct = f
		}
	}
	if sg.Spec.MinConfidence != "" {
		if f, err := strconv.ParseFloat(sg.Spec.MinConfidence, 64); err == nil {
			cfg.MinConfidence = f
		}
	}
	if sg.Spec.MinThroughputGain != "" {
		if f, err := strconv.ParseFloat(sg.Spec.MinThroughputGain, 64); err == nil {
			cfg.MinThroughputGain = f
		}
	}
	return cfg
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScaleGuardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&autoscalingv1alpha1.ScaleGuard{}).
		Owns(&autoscalingv1alpha1.ScaleDecision{}).
		Named("scaleguard").
		Complete(r)
}
