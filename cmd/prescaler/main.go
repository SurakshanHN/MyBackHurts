package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/prescale/prescale/internal/metrics"
	"github.com/prescale/prescale/internal/predictor"
	"github.com/prescale/prescale/internal/scaledobject"
)

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envOrDefaultBool(key string, defaultValue bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return defaultValue
}

func envOrDefaultDuration(key string, defaultValue time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultValue
}

func envOrDefaultInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

func envOrDefaultFloat(key string, defaultValue float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

type loopState struct {
	consecutiveFailures int
	lastReleaseTime     time.Time
}

func main() {
	var (
		victoriaURL      = flag.String("victoria-url", envOrDefault("VICTORIA_URL", "http://vmsingle.monitoring.svc.cluster.local:8428"), "VictoriaMetrics query URL")
		predictorURL     = flag.String("predictor-url", envOrDefault("PREDICTOR_URL", "http://predictor.predictive.svc.cluster.local:8000"), "Prediction service URL")
		namespace        = flag.String("namespace", envOrDefault("NAMESPACE", "predictive"), "Target Kubernetes namespace")
		scaledObjectName = flag.String("scaled-object", envOrDefault("SCALED_OBJECT", "checkout"), "Target ScaledObject name")
		interval         = flag.Duration("interval", envOrDefaultDuration("INTERVAL", 15*time.Second), "Evaluation interval")
		windowDuration   = flag.Duration("window", envOrDefaultDuration("WINDOW", 5*time.Minute), "Historical window to feed predictor")
		minReplicas      = flag.Int("min-replicas", envOrDefaultInt("MIN_REPLICAS", 3), "Hard floor for minReplicaCount")
		maxReplicas      = flag.Int("max-replicas", envOrDefaultInt("MAX_REPLICAS", 10), "Hard ceiling for minReplicaCount")
		connsPerPod      = flag.Int("conns-per-pod", envOrDefaultInt("CONNS_PER_POD", 20), "Checkout semaphore width per pod")
		headroom         = flag.Float64("headroom", envOrDefaultFloat("HEADROOM", 1.20), "Headroom multiplier (1.20 = 20%% safety buffer)")
		boostTTL         = flag.Duration("boost-ttl", envOrDefaultDuration("BOOST_TTL", 60*time.Second), "Boost lease TTL renewed on each boost")
		maxFailures      = flag.Int("max-failures", envOrDefaultInt("MAX_FAILURES", 3), "Consecutive failure threshold before releasing boost")
		maxStep          = flag.Int("max-step", envOrDefaultInt("MAX_STEP", 3), "Maximum replica count increase per tick")
		cooldown         = flag.Duration("cooldown", envOrDefaultDuration("COOLDOWN", 30*time.Second), "Cooldown period after release before new boost allowed")
		dryRun           = flag.Bool("dry-run", envOrDefaultBool("DRY_RUN", false), "Log actions without patching ScaledObject")
		kubeconfig       = flag.String("kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig (empty = in-cluster)")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("Starting prescaler daemon",
		"victoria_url", *victoriaURL,
		"predictor_url", *predictorURL,
		"namespace", *namespace,
		"scaled_object", *scaledObjectName,
		"interval", interval.String(),
		"window", windowDuration.String(),
		"min_replicas", *minReplicas,
		"max_replicas", *maxReplicas,
		"conns_per_pod", *connsPerPod,
		"headroom", *headroom,
		"boost_ttl", boostTTL.String(),
		"max_failures", *maxFailures,
		"max_step", *maxStep,
		"cooldown", cooldown.String(),
		"dry_run", *dryRun,
	)

	// --- Kubernetes client setup ---

	var cfg *rest.Config
	var err error
	if *kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		slog.Error("Could not build Kubernetes config", "err", err)
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		slog.Error("Could not create dynamic client", "err", err)
		os.Exit(1)
	}

	// --- Dependencies ---

	soPatcher := &scaledobject.Patcher{
		Client:           dynClient,
		AllowedNamespace: *namespace,
		AllowedName:      *scaledObjectName,
	}

	predClient := predictor.New(*predictorURL)
	predClient.ConnsPerPod = *connsPerPod
	predClient.Headroom = *headroom

	vmSource := metrics.NewPrometheus(metrics.Config{
		BaseURL: *victoriaURL,
		// PromQL templates – checkout publishes these labels via the workload Deployment.
		RPS:        `sum(rate(checkout_requests_total{namespace="{{namespace}}",deployment="{{name}}"}[{{step}}]))`,
		P99Ms:      `histogram_quantile(0.99, sum by (le) (rate(checkout_request_duration_seconds_bucket{namespace="{{namespace}}",deployment="{{name}}"}[{{step}}]))) * 1000`,
		ErrRate:    `sum(rate(checkout_requests_total{namespace="{{namespace}}",deployment="{{name}}",status=~"5.."}[{{step}}])) / sum(rate(checkout_requests_total{namespace="{{namespace}}",deployment="{{name}}"}[{{step}}]))`,
		Replicas:   `kube_deployment_spec_replicas{namespace="{{namespace}}",deployment="{{name}}"}`,
		Ready:      `kube_deployment_status_replicas_ready{namespace="{{namespace}}",deployment="{{name}}"}`,
		Saturation: `kube_deployment_status_replicas_ready{namespace="{{namespace}}",deployment="{{dep}}"}`,
		LatencyMs:  `histogram_quantile(0.99, sum by (le) (rate(fakedb_request_duration_seconds_bucket{namespace="{{namespace}}"}[{{step}}]))) * 1000`,
	})

	target := metrics.Target{Namespace: *namespace, Name: *scaledObjectName}

	planCfg := scaledobject.Config{
		BaselineMin: int32(*minReplicas),
		MaxLimit:    int32(*maxReplicas),
		BoostTTL:    *boostTTL,
		MaxFailures: *maxFailures,
		MaxStep:     int32(*maxStep),
		Cooldown:    *cooldown,
	}

	// --- Main control loop ---

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	ls := &loopState{}

	// Run first evaluation immediately on startup to check for active/expired leases from prior run
	runTick(ctx, time.Now(), target, vmSource, predClient, soPatcher, *scaledObjectName, *namespace,
		planCfg, *windowDuration, *interval, *dryRun, ls)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down prescaler daemon cleanly")
			return
		case t := <-ticker.C:
			runTick(ctx, t, target, vmSource, predClient, soPatcher, *scaledObjectName, *namespace,
				planCfg, *windowDuration, *interval, *dryRun, ls)
		}
	}
}

func runTick(
	ctx context.Context,
	now time.Time,
	target metrics.Target,
	src metrics.Source,
	pred *predictor.Client,
	patcher *scaledobject.Patcher,
	soName, namespace string,
	cfg scaledobject.Config,
	window, step time.Duration,
	dryRun bool,
	ls *loopState,
) {
	// 1. Fetch current ScaledObject state from Kubernetes
	soState, err := patcher.GetInfo(ctx, namespace, soName)
	if err != nil {
		slog.Error("Could not get ScaledObject info", "err", err, "namespace", namespace, "scaled_object", soName)
		return
	}
	soState.ConsecutiveFailures = ls.consecutiveFailures
	soState.LastReleaseTime = ls.lastReleaseTime

	original := soState.OriginalMin
	if original <= 0 {
		original = soState.CurrentMinReplicas
		if original <= 0 {
			original = cfg.BaselineMin
		}
	}

	from := now.Add(-window)

	// 2. Fetch recent RPS series from VictoriaMetrics
	var tickErr error
	var forecastRPS float64
	var desired int32
	var rpsWindow []float64

	samples, err := src.Series(ctx, target, from, now, step)
	if err != nil {
		if errors.Is(err, metrics.ErrNoData) {
			// No traffic recorded yet: pass zero RPS window to predictor
			rpsWindow = []float64{0.0}
		} else {
			tickErr = err
		}
	} else {
		// Build window of RPS values in chronological order
		rpsWindow = make([]float64, len(samples))
		for i, s := range samples {
			rpsWindow[i] = s.RPS
		}
	}

	if tickErr == nil {
		// 3. Call prediction service
		forecastRPS, err = pred.Predict(ctx, rpsWindow)
		if err != nil {
			tickErr = err
		} else {
			// 4. Calculate desired replicas
			desired = pred.ReplicasFor(forecastRPS, cfg.BaselineMin, cfg.MaxLimit)
		}
	}

	// 5. Evaluate pure plan decision
	plan := scaledobject.PlanDecision(soState, cfg, now, desired, tickErr)

	// Update in-memory loop state
	ls.consecutiveFailures = plan.NewFailures
	if !plan.NewReleaseTime.IsZero() {
		ls.lastReleaseTime = plan.NewReleaseTime
	}

	// 6. Log one structured line per tick
	slog.Info("Prescaler tick",
		"forecast", forecastRPS,
		"desired", desired,
		"current", soState.CurrentMinReplicas,
		"original", original,
		"action", plan.Action,
		"reason", plan.Reason,
	)

	// 7. Execute patch (or dry-run log)
	patchBytes, patchErr := patcher.Execute(ctx, namespace, soName, plan, dryRun)
	if dryRun && patchBytes != nil {
		slog.Info("Dry-run: would send patch", "patch", string(patchBytes))
	}
	if patchErr != nil {
		slog.Error("Could not execute ScaledObject patch", "err", patchErr, "namespace", namespace, "scaled_object", soName)
	}
}
