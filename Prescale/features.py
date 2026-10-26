"""
Shared logic for PreScale training (Colab) and serving (FastAPI).

Training and serving import this same file, so features are computed
identically offline and online (train/serve skew is a silent failure mode).

Rule: every feature at minute t uses only information available at t or earlier.
Inputs are restricted to signals VictoriaMetrics can provide live.
"""
import io
import math

import numpy as np
import pandas as pd

# ---- Policy constants (change deliberately; they are stored in the model bundle)
HORIZON_MIN = 3        # forecast 3 min ahead: covers 45-90s pod startup + scrape margin
TARGET_UTIL = 0.6      # desired CPU utilisation per pod
MIN_REPLICAS = 1

LAGS = [1, 2, 3, 5, 10, 15, 30]
WINDOWS = [5, 15, 30]
MIN_HISTORY = max(LAGS + WINDOWS) + 5   # minutes of history before features are defined

# Dynamic signals (time series) and static per-deployment config.
DYN_COLS = ["cpu", "rps", "mem", "pending_pods"]
STATIC_COLS = ["cpu_request", "mem_limit"]


def load_metrics(path: str) -> pd.DataFrame:
    """Load CSV tolerantly: drop malformed/corrupted lines and report them.

    The testing file contains one truncated row with stray bytes; we skip it
    instead of letting it silently shift columns.
    """
    with open(path, "rb") as fh:
        lines = fh.read().split(b"\n")
    n_fields = lines[0].count(b",")
    good, bad = [lines[0]], []
    for lineno, line in enumerate(lines[1:], start=2):
        if not line.strip():
            continue
        if line.count(b",") == n_fields and all(c < 128 for c in line):
            good.append(line)
        else:
            bad.append(lineno)
    df = pd.read_csv(io.BytesIO(b"\n".join(good)))
    print(f"{path}: {len(df)} rows kept, {len(bad)} malformed line(s) skipped {bad[:10]}")
    return df


def grid_from_arrays(index, cpu, rps, mem_bytes, pod_count, ready_pods,
                     cpu_request, mem_limit_bytes, max_ffill: int = 2) -> pd.DataFrame:
    """SINGLE place where raw signals become the 1-minute grid.

    Training (to_grid) and serving (online.build_grid) both call this, so the
    two paths cannot drift apart. Memory is stored in GiB to keep rolling-std
    arithmetic numerically stable.
    """
    dyn = pd.DataFrame(
        {
            "cpu": np.asarray(cpu, dtype=float),
            "rps": np.asarray(rps, dtype=float),
            "mem": np.asarray(mem_bytes, dtype=float) / 2**30,
            "pending_pods": np.clip(np.asarray(pod_count, float) - np.asarray(ready_pods, float), 0, None),
            "replicas": np.clip(np.asarray(pod_count, float), 1, None),
        },
        index=pd.DatetimeIndex(index),
    )
    grid = dyn.sort_index().resample("1min").mean().ffill(limit=max_ffill)
    grid["cpu_request"] = float(cpu_request)
    grid["mem_limit"] = float(mem_limit_bytes) / 2**30
    return grid


def to_grid(g: pd.DataFrame, ts_col: str = "timestamp_utc", max_ffill: int = 2) -> pd.DataFrame:
    """Training path: one deployment's raw CSV rows -> 1-minute grid."""
    idx = pd.to_datetime(g[ts_col], utc=True).dt.tz_convert(None)
    return grid_from_arrays(
        idx, g["cpu_used_cores"], g["request_rate_rps"], g["mem_used_bytes"],
        g["pod_count"], g["ready_pods"],
        float(g["cpu_request_per_pod_cores"].iloc[0]),
        float(g["mem_limit_per_pod_bytes"].iloc[0]), max_ffill)


def make_features(grid: pd.DataFrame) -> pd.DataFrame:
    """Lag, rolling, slope, utilisation and time-of-day features (past-only)."""
    f = {}
    for c in DYN_COLS:
        s = grid[c]
        for lag in LAGS:
            f[f"{c}_lag{lag}"] = s.shift(lag)
        for w in WINDOWS:
            r = s.rolling(w, min_periods=w)
            f[f"{c}_mean{w}"] = r.mean()
            f[f"{c}_std{w}"] = r.std()
            f[f"{c}_max{w}"] = r.max()
        f[f"{c}_diff1"] = s.diff(1)
        f[f"{c}_slope5"] = s.diff(5) / 5.0

    f["cpu_request"] = grid["cpu_request"]
    f["mem_limit"] = grid["mem_limit"]
    f["mem_util"] = grid["mem"] / grid["mem_limit"]
    f["cpu_util_now"] = grid["cpu"] / (grid["replicas"] * grid["cpu_request"])

    minute_of_day = grid.index.hour * 60 + grid.index.minute
    f["tod_sin"] = np.sin(2 * np.pi * minute_of_day / 1440.0)
    f["tod_cos"] = np.cos(2 * np.pi * minute_of_day / 1440.0)
    f["dow"] = grid.index.dayofweek
    # Round so tiny floating-point differences between the training path (full
    # history) and the serving path (60-min window) cannot flip a tree's bin edge.
    return pd.DataFrame(f, index=grid.index).round(8)


def build_xy(grid: pd.DataFrame, horizon: int = HORIZON_MIN):
    """Features X, delta target y = cpu(t+H) - cpu(t), and evaluation columns.

    y uses future values, so it exists only in training. It is never an input.
    """
    X = make_features(grid)
    y = grid["cpu"].shift(-horizon) - grid["cpu"]
    ev = pd.DataFrame(
        {
            "cpu_now": grid["cpu"],
            "cpu_future": grid["cpu"].shift(-horizon),
            "replicas": grid["replicas"],
        },
        index=grid.index,
    )
    return X, y, ev


def pinball(y, yhat, q):
    d = y - yhat
    return np.mean(np.maximum(q * d, (q - 1) * d))


def recommend(p10_abs, p50_abs, p90_abs, cpu_request, current_replicas,
              target_util=TARGET_UTIL, min_replicas=MIN_REPLICAS):
    """Replica policy: provision for the P90 forecast of total CPU demand.

    Returns (desired_replicas, action, reason). Scale-down is not recommended
    here; the controller applies cooldowns before shrinking.
    """
    per_pod = cpu_request * target_util
    desired = max(min_replicas, math.ceil(p90_abs / per_pod))
    if desired > current_replicas:
        action = "scale_up"
        reason = (f"P90 CPU forecast {p90_abs:.2f} cores in {HORIZON_MIN} min needs "
                  f"{desired} replicas at {target_util:.0%} target utilisation; "
                  f"currently {current_replicas}.")
    else:
        action = "hold"
        reason = (f"P90 CPU forecast {p90_abs:.2f} cores in {HORIZON_MIN} min is covered "
                  f"by {current_replicas} replicas.")
    return desired, action, reason
