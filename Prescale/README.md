# PreScale forecaster: pre-scaling replica recommendations

## Files
| File | Purpose |
|---|---|
| `PreScale_training.ipynb` | Colab notebook (49 cells, 19 code): data audit, leakage tests, training, calibration, plots, quality gate, train/serve consistency test, export |
| `train_prescale.py` | Same pipeline as a script (the notebook is generated from it) |
| `features.py` | Shared feature code. Training and serving both import it |
| `online.py` | Live inference logic: VictoriaMetrics series -> grid -> forecast -> recommendation or HPA fallback |
| `serve_app.py` | FastAPI wrapper exposing `GET /predict` and `GET /health` |
| `prescale_model.joblib` | Model trained on your CSVs (passed the quality gate) |

## 1. Train in Colab
1. Upload `features.py`, `online.py`, `training_metrics.csv`, `testing_metrics.csv` to the Colab Files panel.
2. Open `PreScale_training.ipynb` and run all cells.
3. Cell 9 writes `prescale_model.joblib`. Download it. The model is not saved if the gate fails.

## 2. Run online
```
pip install -r requirements.txt
export DEPLOYMENT=checkout-api          # one service per instance; repeat per deployment
export VM_URL=http://<victoriametrics>:8428
export MODEL_PATH=prescale_model.joblib
uvicorn serve_app:app --port 8080
curl localhost:8080/predict
```
Deploy one instance per deployment inside the cluster (a Deployment + Service), pointing `VM_URL` at the VictoriaMetrics service.

## 3. Verify the PromQL before going live (important)
Each signal in `serve_app.py` must measure the same thing as its CSV column:

| Model input | CSV column | Must be |
|---|---|---|
| `cpu` | `cpu_used_cores` | total cores used by all pods of the deployment |
| `rps` | `request_rate_rps` | total requests/s for the deployment |
| `mem` | `mem_used_bytes` | total working-set bytes |
| `pod_count` | `pod_count` | pods that exist |
| `ready_pods` | `ready_pods` | pods that are Ready |

The label names (`deployment=`, `container=`, `condition=`) are examples. Confirm them in your cluster. Use `Q_CPU`, `Q_RPS`, etc. to override.

## 4. Output
```json
{
  "mode": "prescale",
  "action": "scale_up",
  "current_replicas": 3,
  "desired_replicas": 5,
  "forecast_horizon_min": 3,
  "cpu_now_cores": 1.31,
  "cpu_forecast_cores": {"p10": 1.10, "p50": 1.42, "p90": 2.61},
  "reason": "P90 CPU forecast 2.61 cores in 3 min needs 5 replicas at 60% target utilisation; currently 3.",
  "data_staleness_s": 42.0
}
```
When confidence is low, metrics are stale, or history is incomplete, `mode` is `fallback_hpa` and the reason says why. Let HPA decide in that case.

## Held-out results (unseen week, Oct 15–21)
Model only, all minutes:
- P50 MAE 0.0896 vs persistence 0.1020 (about 12% better)
- P10–P90 coverage 76.9% (target 80%; bounds are slightly narrow)
- Minutes under-provisioned: PreScale 2.2% vs reactive HPA 12.5%
- Average extra pods: PreScale 0.40 vs HPA 0.16 (the cost of provisioning for P90)

Deployed mix (HPA handles the 10.7% of low-confidence minutes):
- Under-provisioned: 3.5% vs HPA 12.5%
- Extra pods: 0.36 vs HPA 0.16

## Known limitations
- Forecast error is modest. The model is a meaningful improvement over persistence, not a perfect predictor. Spikes not visible in recent history will be missed.
- `scheduled_workload_flag` is not used because it is not a live VictoriaMetrics signal. Scheduled batch jobs (report-batch) can surprise the model.
- Target utilisation (0.6) and horizon (3 min) are policy choices. Tune them with the cost/SLA trade-off.
- Retrain periodically. The model was trained on one week and tested on another, so drift is untested beyond that.
- The model recommends replicas only. It does not change memory limits or do OOM-risk estimation; those are separate components.
- `recommended_replicas` and `prewarm` in the CSVs look derived from future CPU, so they are excluded to avoid leakage.
- `arrival_rate_rps` duplicates `request_rate_rps`, so only one is used.

## What was verified
- Both CSVs load. One malformed row in the testing file is skipped and logged.
- Time-based split with a horizon gap. No shared timestamps across the train, validation and test periods.
- Online path reproduces the offline features and predictions exactly on 100% of sampled minutes across all five deployments.
- The `fallback_hpa` behaviour works for stale data and short history.

## What was not verified here
- The LightGBM backend. The sandbox could not install LightGBM, so the local run used scikit-learn's quantile gradient boosting with the same interface. Run the notebook in Colab, where LightGBM is preinstalled, and compare the metrics.
- The running FastAPI service and a live VictoriaMetrics connection. These need your cluster.
