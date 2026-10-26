# Part 10B: Offline Evaluation of the Teammate's Model

## 1. Unknowns Answered

**Feature groups** (79 total): lag×28, rolling_mean×12, rolling_std×12, rolling_max×12, diff1×4, slope5×4, static×2, ratio×2, calendar×3.

**Replica constants** (features.py): `HORIZON_MIN=3`, `TARGET_UTIL=0.6`, `MIN_REPLICAS=1`. Per-deployment `cpu_request` comes from `bundle["deployments"][name]`. No hard `MAX_REPLICAS` in `recommend()`.

**Fallback conditions** (online.py): (1) history <35 min; (2) staleness >120s; (3) any feature NaN; (4) `rel_width=(P90-P10)/max(P50,0.05) > bundle["max_rel_width"]` (sklearn=0.213, lgbm=0.219).

**CPU unit**: Deployment total cores (`sum(rate(...))` across all pods — README.md:34, serve_app.py:26).

**Deployment check**: `serve_app.py:34: if DEPLOYMENT not in bundle["deployments"]: raise RuntimeError(...)` — fires at uvicorn startup. `predict_from_grid()` itself does NOT check name. Container test with `deployment="checkout"` (not in bundle) run directly via `predict_from_grid` on valid 60-min window: returned `mode=fallback_hpa` (confidence threshold, not name rejection). Bundle names: `["auth-svc","checkout-api","recs-worker","report-batch","search-svc"]`.

**Tracked**: Yes, 11 files in `Prescale/` fully tracked; `git status --short Prescale/` is clean.

## 2. Test-Week Metrics (testing_metrics.csv, Oct 15–21, MEASURED)

| Model | Dep | n | P10-P90 Coverage | MAE P50 | MAE Persistence |
|---|---|---|---|---|---|
| sklearn (.joblib) | auth-svc | 10047 | 0.7550 | 0.0784 | 0.0902 |
| sklearn | checkout-api | 10047 | 0.7835 | 0.0901 | 0.1010 |
| sklearn | recs-worker | 10047 | 0.7512 | 0.1024 | 0.1162 |
| sklearn | report-batch | 10047 | 0.7845 | 0.0857 | 0.0981 |
| sklearn | search-svc | 10047 | 0.7683 | 0.0913 | 0.1044 |
| **sklearn OVERALL** | all | **50235** | **0.7685** | **0.0896** | **0.1020** |
| lgbm (.pkl) | auth-svc | 10047 | 0.7505 | 0.0784 | 0.0902 |
| lgbm | checkout-api | 10047 | 0.7823 | 0.0899 | 0.1010 |
| lgbm | recs-worker | 10047 | 0.7504 | 0.1020 | 0.1162 |
| lgbm | report-batch | 10047 | 0.7790 | 0.0854 | 0.0981 |
| lgbm | search-svc | 10047 | 0.7639 | 0.0912 | 0.1044 |
| **lgbm OVERALL** | all | **50235** | **0.7652** | **0.0894** | **0.1020** |

## 3. Spike Recall and False-Alarm Rate (MEASURED)
Event: `CPU(t+3) >= 1.5×CPU(t)` AND `CPU(t+3) - CPU(t) >= 0.1`. Recall: fraction where `P90(t) >= 1.3×CPU(t)`.

| Model | Events | Recall | FA Rate |
|---|---|---|---|
| sklearn OVERALL | 314 | 0.2452 | 0.0025 |
| lgbm OVERALL | 314 | 0.2484 | 0.0024 |

Both models predict spikes only 24–25% of the time (low recall). False-alarm rate is very low (0.24–0.25%).

## 4. Corrected Spike Windows (MEASURED)
Deployment `checkout-api`, teammate constants (req=0.5, util=0.6), our constants (req=0.4, util=0.5).

| Window | CPU now | P10/P50/P90 (sklearn) | desired_t/desired_ours | P10/P50/P90 (lgbm) | desired_t/desired_ours |
|---|---|---|---|---|---|
| W0 flat baseline | 0.350 | 0.3412/0.489/0.490 | 2/3 | 0.354/0.481/0.487 | 2/3 |
| W1 end min 49 (pre-spike) | 0.350 | 0.342/0.492/0.509 | 2/3 | 0.359/0.482/0.509 | 2/3 |
| W2 end min 52 (mid-spike) | 0.350 | 0.342/0.492/0.508 | 2/3 | 0.359/0.481/0.509 | 2/3 |
| W3 end min 53 (post-spike) | 0.350 | 0.342/0.492/0.508 | 2/3 | 0.359/0.481/0.509 | 2/3 |

Note: spike at indices 50-52 leaves cpu_now at baseline at minute 53. Model does not boost replicas at any window for this synthetic 3-min spike.

## 5. UNEXPLAINED / NOT RECORDED
- UNEXPLAINED: Spike recall is ~24–25% despite the model claiming 76.8% P10-P90 coverage. The coverage metric measures how often future CPU falls inside the forecast interval; it does not measure whether the model raises P90 ahead of spikes.
- UNEXPLAINED: All synthetic spike windows return identical P10/P50/P90 across W1-W3; the 3-minute spike at indices 50-52 out of 60 minutes of history may produce insufficient `cpu_diff1`, `cpu_slope5`, and `cpu_max5` signal given flat surroundings.

## 6. Scope Check and Git Terminal Output
```
$ git diff f58041a..HEAD --name-only
docs/reports/data/part10b/eval.txt
docs/reports/data/part10b/unknowns.txt
docs/reports/data/part10b/windows.txt
$ git --no-pager status --short
(clean — appended after report commit)
$ git --no-pager log --oneline f58041a..HEAD
<final_commit> docs: part 10b report
a59a05d measure: part 10b spike windows
246dd8d measure: part 10b test-week evaluation
64c5e85 docs: part 10b unknowns
```
