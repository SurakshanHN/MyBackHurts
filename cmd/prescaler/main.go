package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
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

func main() {
	var (
		victoriaURL  = flag.String("victoria-url", envOrDefault("VICTORIA_URL", "http://localhost:8428"), "VictoriaMetrics query URL")
		predictorURL = flag.String("predictor-url", envOrDefault("PREDICTOR_URL", "http://localhost:8000"), "Prediction service URL")
		namespace    = flag.String("namespace", envOrDefault("NAMESPACE", "demo"), "Target Kubernetes namespace")
		hpaName      = flag.String("hpa", envOrDefault("HPA_NAME", "checkout"), "Target HorizontalPodAutoscaler name")
		interval     = flag.Duration("interval", envOrDefaultDuration("INTERVAL", 15*time.Second), "Evaluation interval")
		dryRun       = flag.Bool("dry-run", envOrDefaultBool("DRY_RUN", true), "Run in dry-run mode without mutating HPA")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Starting prescaler daemon",
		"victoria_url", *victoriaURL,
		"predictor_url", *predictorURL,
		"namespace", *namespace,
		"hpa", *hpaName,
		"interval", interval.String(),
		"dry_run", *dryRun,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Shutting down prescaler daemon cleanly")
			return
		case <-ticker.C:
			slog.Info("tick (not implemented)")
			// TODO(part-6): query recent traffic metrics from VictoriaMetrics
			// TODO(part-6): call predictor service for traffic forecast
			// TODO(part-6): invoke actuator to adjust HPA minReplicas before surge
		}
	}
}
