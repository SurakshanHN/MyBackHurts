"""
Online inference logic (pure functions, no web framework required).

Flow: VictoriaMetrics query_range -> minute grid -> same features as training
-> P10/P50/P90 CPU forecast -> replica recommendation, or fallback to HPA.
"""
import math

import numpy as np
import pandas as pd

from features import (MIN_HISTORY, make_features, recommend, grid_from_arrays)

MAX_STALENESS_S = 120      # newest sample must be within 2 min, else fall back
LOOKBACK_MIN = 60          # minutes pulled per query (>= MIN_HISTORY)
MIN_P50_CORES = 0.05       # floor used in the confidence ratio (same as training)


def vm_series(payload: dict) -> pd.Series:
    """Parse a VictoriaMetrics /api/v1/query_range JSON response into a Series.

    Expects an aggregated PromQL query (sum/count) so there is one series.
    """
    if payload.get("status") != "success":
        raise ValueError(f"VictoriaMetrics error: {payload}")
    result = payload["data"]["result"]
    if not result:
        return pd.Series(dtype=float)
    values = result[0]["values"]  # [[unix_ts, "value"], ...]
    idx = pd.to_datetime([float(t) for t, _ in values], unit="s")
    return pd.Series([float(v) for _, v in values], index=idx).groupby(level=0).mean()


def build_grid(series: dict, static: dict) -> pd.DataFrame:
    """Serving path: VictoriaMetrics series -> the SAME grid training uses."""
    idx = series["cpu"].index
    aligned = {k: series[k].reindex(idx) for k in ("cpu", "rps", "mem", "pod_count", "ready_pods")}
    return grid_from_arrays(
        idx, aligned["cpu"], aligned["rps"], aligned["mem"], aligned["pod_count"],
        aligned["ready_pods"], static["cpu_request"], static["mem_limit"])


def fallback(reason: str, staleness_s=None) -> dict:
    return {
        "mode": "fallback_hpa",
        "action": "defer_to_hpa",
        "desired_replicas": None,
        "reason": reason,
        "data_staleness_s": staleness_s,
    }


def predict_from_grid(grid: pd.DataFrame, bundle: dict, now: pd.Timestamp) -> dict:
    """Produce a recommendation from a 1-minute grid ending at the latest sample."""
    if len(grid) < MIN_HISTORY:
        return fallback(f"insufficient history: {len(grid)} minutes, need {MIN_HISTORY}")

    staleness = (now - grid.index[-1]).total_seconds()
    if staleness > MAX_STALENESS_S:
        return fallback(f"stale metrics: newest sample {staleness:.0f}s old", staleness)

    feats = make_features(grid)
    x = feats.iloc[[-1]][bundle["feature_cols"]]
    if x.isna().any(axis=None):
        return fallback("feature gap in recent metrics (missing >2 min)", staleness)

    cpu_now = float(grid["cpu"].iloc[-1])
    models = bundle["models"]
    deltas = np.sort(np.array([[models[q].predict(x)[0] for q in bundle["quantiles"]]]), axis=1)[0]
    p10, p50, p90 = cpu_now + deltas

    rel_width = (p90 - p10) / max(p50, MIN_P50_CORES)
    if rel_width > bundle["max_rel_width"]:
        return fallback(
            f"low confidence: forecast interval too wide (relative width {rel_width:.2f} "
            f"> limit {bundle['max_rel_width']:.2f})", staleness)

    cpu_request = float(grid["cpu_request"].iloc[-1])
    current = int(grid["replicas"].iloc[-1])
    desired, action, reason = recommend(p10, p50, p90, cpu_request, current,
                                        target_util=bundle["target_util"],
                                        min_replicas=bundle["min_replicas"])
    return {
        "mode": "prescale",
        "action": action,
        "current_replicas": current,
        "desired_replicas": int(desired),
        "forecast_horizon_min": bundle["horizon_min"],
        "cpu_now_cores": round(cpu_now, 4),
        "cpu_forecast_cores": {"p10": round(p10, 4), "p50": round(p50, 4), "p90": round(p90, 4)},
        "reason": reason,
        "data_staleness_s": round(staleness, 1),
    }
