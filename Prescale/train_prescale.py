# PreScale forecaster: training pipeline (Google Colab friendly).
# Cells are separated by "# %%". Upload features.py and both CSVs to Colab
# (or set TRAIN_CSV / TEST_CSV) and run top to bottom.

# %% 1. Config and imports
import os
import joblib
import numpy as np
import pandas as pd
from features import (HORIZON_MIN, TARGET_UTIL, MIN_REPLICAS, STATIC_COLS,
                      load_metrics, to_grid, build_xy, pinball)

TRAIN_CSV = os.getenv("TRAIN_CSV", "/content/training_metrics.csv")
TEST_CSV = os.getenv("TEST_CSV", "/content/testing_metrics.csv")
OUT_MODEL = os.getenv("OUT_MODEL", "prescale_model.joblib")
QUANTILES = [0.1, 0.5, 0.9]
TRAIN_FRAC = 0.8          # first 80% of the training window trains, last 20% validates

# %% 2. Model backend: LightGBM (Colab has it preinstalled).
# Fallback to scikit-learn only so this script can run in environments without LightGBM.
try:
    import lightgbm as lgb
    BACKEND = "lightgbm"
except ImportError:
    lgb = None
    from sklearn.ensemble import HistGradientBoostingRegressor
    BACKEND = "sklearn"
print("backend:", BACKEND)


def fit_quantile(q, Xtr, ytr, Xva, yva):
    if BACKEND == "lightgbm":
        m = lgb.LGBMRegressor(
            objective="quantile", alpha=q, n_estimators=2000, learning_rate=0.03,
            num_leaves=31, min_child_samples=50, subsample=0.8, subsample_freq=1,
            colsample_bytree=0.8, random_state=42, verbose=-1,
        )
        m.fit(Xtr, ytr, eval_set=[(Xva, yva)], eval_metric="quantile",
              callbacks=[lgb.early_stopping(100, verbose=False)])
    else:
        m = HistGradientBoostingRegressor(
            loss="quantile", quantile=q, max_iter=400, learning_rate=0.05,
            max_leaf_nodes=31, min_samples_leaf=50, random_state=42,
        )
        m.fit(Xtr, ytr)
    return m


def predict_quantiles(models, X):
    """Stack P10/P50/P90 predicted deltas and sort them so bounds never cross."""
    raw = np.column_stack([models[q].predict(X) for q in QUANTILES])
    return np.sort(raw, axis=1)

# %% 3. Build per-deployment datasets from raw rows (no cross-deployment lag leakage)
def build_dataset(df):
    parts = []
    for dep, g in df.groupby("deployment"):
        grid = to_grid(g)
        X, y, ev = build_xy(grid)
        d = X.join(y.rename("y")).join(ev)
        d["deployment"] = dep
        parts.append(d)
    return pd.concat(parts).sort_index()

raw_train = load_metrics(TRAIN_CSV)
raw_test = load_metrics(TEST_CSV)
D_train_all = build_dataset(raw_train)
D_test = build_dataset(raw_test)

FEATURE_COLS = [c for c in D_train_all.columns
                if c not in ("y", "cpu_now", "cpu_future", "replicas", "deployment")]
print("features:", len(FEATURE_COLS))

# %% 4. Strict time-based split with a horizon gap
# A label at time t depends on the value at t+H, so training rows must have
# t+H before the validation cut-off. Otherwise validation information leaks into training.
ts_unique = D_train_all.index.unique().sort_values()
cut = ts_unique[int(len(ts_unique) * TRAIN_FRAC)]
gap = pd.Timedelta(minutes=HORIZON_MIN)

D_train = D_train_all[D_train_all.index < cut - gap].dropna(subset=FEATURE_COLS + ["y"])
D_val = D_train_all[D_train_all.index >= cut].dropna(subset=FEATURE_COLS + ["y"])
D_test = D_test.dropna(subset=FEATURE_COLS + ["y"])

print(f"train {len(D_train)} rows [{D_train.index.min()} .. {D_train.index.max()}]")
print(f"val   {len(D_val)} rows [{D_val.index.min()} .. {D_val.index.max()}]")
print(f"test  {len(D_test)} rows [{D_test.index.min()} .. {D_test.index.max()}] (held-out period)")

# %% 5. Train P10 / P50 / P90 models
models = {}
for q in QUANTILES:
    models[q] = fit_quantile(q, D_train[FEATURE_COLS], D_train["y"],
                             D_val[FEATURE_COLS], D_val["y"])
    print("trained quantile", q)

# %% 6. Validation-based confidence threshold (computed BEFORE looking at test)
val_pred = predict_quantiles(models, D_val[FEATURE_COLS])
val_p10, val_p50, val_p90 = val_pred.T
val_now = D_val["cpu_now"].to_numpy()
val_width = (val_p90 - val_p10)
val_rel = val_width / np.maximum(val_now + val_p50, 0.05)
MAX_REL_WIDTH = float(np.quantile(val_rel, 0.90))  # ~10% of val points fall back to HPA
print(f"confidence threshold (val 90th pct relative width): {MAX_REL_WIDTH:.3f}")

# %% 7. Held-out evaluation (report everything, including where we do not win)
def evaluate(D, label):
    pred = predict_quantiles(models, D[FEATURE_COLS])
    p10, p50, p90 = pred.T
    y = D["y"].to_numpy()
    now = D["cpu_now"].to_numpy()
    per_pod = D["cpu_request"].to_numpy() * TARGET_UTIL

    need = np.maximum(MIN_REPLICAS, np.ceil(D["cpu_future"].to_numpy() / per_pod))
    rec_prescale = np.maximum(MIN_REPLICAS, np.ceil((now + p90) / per_pod))
    rec_hpa = np.maximum(MIN_REPLICAS, np.ceil(now / per_pod))  # reactive: uses current CPU only

    res = {
        "rows": len(D),
        "mae_p50_model": np.mean(np.abs(y - p50)),
        "mae_persistence": np.mean(np.abs(y)),
        "coverage_p10_p90": np.mean((y >= p10) & (y <= p90)),
        "pinball_avg": np.mean([pinball(y, pred[:, i], q) for i, q in enumerate(QUANTILES)]),
        "underprov_prescale": np.mean(rec_prescale < need),
        "underprov_hpa": np.mean(rec_hpa < need),
        "waste_pods_prescale": np.mean(np.maximum(0, rec_prescale - need)),
        "waste_pods_hpa": np.mean(np.maximum(0, rec_hpa - need)),
    }
    print(f"\n== {label}")
    for k, v in res.items():
        print(f"  {k:22s} {v:.4f}" if isinstance(v, float) else f"  {k:22s} {v}")
    per_dep = []
    for dep, g in D.groupby("deployment"):
        idx = g.index
        m = D.index.isin(idx) & (D["deployment"] == dep).to_numpy()
        per_dep.append({
            "deployment": dep,
            "mae_model": np.mean(np.abs(y[m] - p50[m])),
            "mae_persistence": np.mean(np.abs(y[m])),
            "coverage": np.mean((y[m] >= p10[m]) & (y[m] <= p90[m])),
            "underprov_prescale": np.mean(rec_prescale[m] < need[m]),
            "underprov_hpa": np.mean(rec_hpa[m] < need[m]),
        })
    print(pd.DataFrame(per_dep).round(3).to_string(index=False))
    return res

val_res = evaluate(D_val, "VALIDATION (Oct 1-7 tail)")
test_res = evaluate(D_test, "TEST (Oct 15-21, unseen period)")

# %% 8. Quality gate: refuse to save a model that does not beat simple baselines
checks = {
    "beats persistence MAE": test_res["mae_p50_model"] < test_res["mae_persistence"],
    "coverage in [0.70, 0.90]": 0.70 <= test_res["coverage_p10_p90"] <= 0.90,
    "fewer under-provisioned minutes than reactive HPA":
        test_res["underprov_prescale"] < test_res["underprov_hpa"],
}
for name, ok in checks.items():
    print(("PASS " if ok else "FAIL ") + name)
if not all(checks.values()):
    raise SystemExit("Gate failed: model NOT saved. Review the report above.")

# %% 9. Save the bundle. Serving loads exactly this.
deployments = (raw_train.groupby("deployment")
               .agg(cpu_request=("cpu_request_per_pod_cores", "first"),
                    mem_limit=("mem_limit_per_pod_bytes", "first"))
               .to_dict(orient="index"))
bundle = {
    "schema_version": 1,
    "backend": BACKEND,
    "models": models,
    "quantiles": QUANTILES,
    "feature_cols": FEATURE_COLS,
    "horizon_min": HORIZON_MIN,
    "target_util": TARGET_UTIL,
    "min_replicas": MIN_REPLICAS,
    "max_rel_width": MAX_REL_WIDTH,
    "deployments": deployments,
    "metrics_test": test_res,
    "metrics_validation": val_res,
}
joblib.dump(bundle, OUT_MODEL)
print("saved", OUT_MODEL)
