# Part 10A Report: Teammate Model Inspection & Offline Evaluation

## 1. Inventory Summary
- Files: `prescale_model.joblib` (3.3MB, sha256: `b5ee6a6c...`), `prescale_model.pkl` (5.2MB, sha256: `451b6c8c...`).
- Features: 79 total (18 dynamic lags/rolling/diffs across 4 signals + 7 static/ratio/calendar features).
- Inputs: VM PromQL range queries (cpu, rps, mem, pod_count, ready_pods) + static cpu_request, mem_limit.
- Outputs: mode (`prescale`|`fallback_hpa`), P10/P50/P90 total CPU cores, desired_replicas (ceil(p90/(req*util))).
- Loaders: `serve_app.py` loads `prescale_model.joblib` (sklearn); `prescale_model.pkl` (lightgbm) is unreferenced.

## 2. Code-Review Grep Hits
- `joblib.load`: `serve_app.py:33:bundle = joblib.load(MODEL_PATH)`, `PreScale_training.ipynb:764`
- `joblib.dump`: `train_prescale.py:180:joblib.dump(bundle, OUT_MODEL)`, `PreScale_training.ipynb:761`
- `os.system` / `subprocess` / `eval` / `exec` / raw `open(..., 'w')`: 0 hits across all files.
- Network: `serve_app.py:53` calls configurable `VM_URL` (`/api/v1/query_range`), no other hosts.

## 3. Isolated Load Check (prescale-model-test:0.1.0)
- Image: `public.ecr.aws/docker/library/python:3.12-slim` + sklearn 1.8.0, numpy 2.4.4, pandas 3.0.2, joblib 1.5.3, lightgbm 4.6.0.
- `prescale_model.pkl`: LOADED via joblib & pickle (backend=`lightgbm`, class=`LGBMRegressor`, 79 features, 0 warnings).
- `prescale_model.joblib`: LOADED via joblib (backend=`sklearn`, class=`HistGradientBoostingRegressor`, 79 features). Pickle failed: `AttributeError: Can't get attribute 'dtype' on <module 'sklearn'>`.

## 4. Offline Predictions (MEASURED)
| Window | sklearn P10/P50/P90 (cores) | sklearn desired | sklearn ms | LightGBM P10/P50/P90 (cores) | LGB desired | LGB ms |
|---|---|---|---|---|---|---|
| (a) flat idle (0.05c) | 0.0428 / 0.1844 / 0.2027 | 1 (hold) | 27.88 | 0.0563 / 0.1770 / 0.1897 | 1 (hold) | 3.41 |
| (b) flat 60 rps (0.35c) | 0.3412 / 0.4890 / 0.4903 | 2 (hold) | 20.07 | 0.3543 / 0.4805 / 0.4871 | 2 (hold) | 3.07 |
| (c) 8x spike (min 50-53) | 0.3312 / 0.4626 / 0.5350 | 2 (hold) | 19.31 | 0.3254 / 0.4531 / 0.5092 | 2 (hold) | 2.32 |
- Standalone test evaluation code path without retraining: none.

## 5. Gap vs Contract Summary
- Metric units: model forecasts deployment CPU cores vs Go prescaler predicting incoming RPS.
- Time scale: 60s step + 180s horizon misses rapid 150s surge dynamics (15s tick).
- Cold start: requires >=35 min contiguous history (returns `fallback_hpa` if shorter).
- Hardcoded deployments: trained on 5 synthetic services, rejects our cluster's `checkout`.

## 6. UNEXPLAINED / NOT RECORDED
- UNEXPLAINED: Origin/creator of `prescale_model.pkl` vs `prescale_model.joblib` (untracked in repo scripts).
- UNEXPLAINED: `prescale_model.pkl` pickle load outputs warning `unclosed file <_io.BufferedReader>`.

## 7. AGENTS.md Conflicts and Scope Check
- AGENTS.md conflicts: None (AGENTS.md updated in Part 9 to reflect actual Go daemon).
- Scope check: `git diff 28a13bf..HEAD` modified only `docs/**`. `Prescale/` is untouched.

## 8. Terminal Output
```
$ git --no-pager status --short
(clean)
$ git --no-pager log --oneline 28a13bf..HEAD
c48145e docs: part 10a report
22b2900 docs: model gap
40ad6c6 measure: part 10a offline predictions
2be2bf0 measure: part 10a model load check
b74771f docs: part 10a inventory
```
