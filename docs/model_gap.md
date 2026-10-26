# PreScale Model vs Prescaler Contract Gap Analysis

## 1. Contract vs Model Comparison
| Dimension | Prescaler Daemon Contract (docs/contract.md) | Teammate PreScale Model (Prescale/) |
|---|---|---|
| **Input** | `POST /predict {"window": [rps, ...]}` | VM query: 5 dynamic series + 2 static settings |
| **Output** | `{"predicted_rps": float, "horizon_seconds": int}` | `{"cpu_forecast_cores": {p10,p50,p90}, "desired_replicas": int}` |
| **Horizon** | 90 s (default) | 180 s (3 minutes fixed) |
| **Step** | 15 s evaluation tick | 60 s resampled grid |
| **History** | 5 minutes lookback | 35 min minimum history (60 min lookback) |
| **Units** | Requests per second (RPS) | Deployment total CPU cores |

## 2. THEORETICAL Workload Mapping (CPU -> Replicas)
- Workload specs: CPU request = 400m (`0.400` cores), HPA target = 50% utilization (`0.200` cores/pod), max = 10 pods (`2.000` cores ceiling).
- Formula: `desired = max(min_replicas, ceil(P90_cores / (cpu_request * target_util)))` [THEORETICAL].
- At `0.200` cores/pod:
  - P90 <= 0.60 cores => 3 pods (baseline min) [THEORETICAL]
  - P90 = 1.00 cores => 5 pods [THEORETICAL]
  - P90 = 1.40 cores => 7 pods (Part 7-9 surge boost target) [THEORETICAL]
  - P90 >= 2.00 cores => 10 pods (safety ceiling) [THEORETICAL]

## 3. Core Architectural Mismatches
1. **Time resolution vs Surge**: Demo surge is 150 s peak; model's 60 s step + 180 s horizon cannot resolve sub-minute surges.
2. **Interface gap**: Go client pushes `{"window":[...]}` via HTTP POST; teammate `serve_app.py` accepts `GET /predict` and pulls directly from VictoriaMetrics.
3. **Cold-start requirement**: Model aborts with `fallback_hpa` if <35 min history exists (vs 5m daemon window).
4. **Deployment coupling**: Model bundle strictly hardcodes 5 deployment names; our `checkout` deployment is rejected.
5. **Synthetic training**: Model trained on synthetic data from Oct 1-7 with different request/limit ratios (0.5 cores vs 0.4 cores).

## 4. Open Questions for Teammate
1. **Model artifacts**: `prescale_model.joblib` is sklearn (HistGradientBoosting); `prescale_model.pkl` is LightGBM. Which is production?
2. **Omitted signals**: Why are p99 latency, DB connection saturation, and 5xx error rate NOT used in features?
3. **CPU unit reconciliation**: Workload uses 400m request; model was trained with 500m for `checkout-api`. Can per-pod CPU scale?
4. **Replica policy**: Model never recommends scale-down (`action="hold"`). How should cooldown/release cooperate with prescaler?
5. **Quantile coverage**: Test week achieved ~76.8% coverage vs 80% target; why does interval widen on synthetic spikes?
6. **Serving architecture**: Is `online.py` intended as an in-cluster sidecar to Prometheus or an adapter for the Go prescaler?
