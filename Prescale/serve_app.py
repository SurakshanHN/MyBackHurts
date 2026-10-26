"""
FastAPI wrapper around online.py.

Run locally:
  DEPLOYMENT=checkout-api VM_URL=http://localhost:8428 MODEL_PATH=prescale_model.joblib \
  uvicorn serve_app:app --port 8080
Then: curl localhost:8080/predict
"""
import os
import time

import httpx
import joblib
import pandas as pd
from fastapi import FastAPI

from online import (LOOKBACK_MIN, build_grid, fallback, predict_from_grid, vm_series)

VM_URL = os.getenv("VM_URL", "http://victoriametrics:8428")
MODEL_PATH = os.getenv("MODEL_PATH", "prescale_model.joblib")
DEPLOYMENT = os.environ["DEPLOYMENT"]

# PromQL must measure the SAME quantity as the training CSV columns.
# Label names below are examples; verify them against your cluster's metrics.
QUERIES = {
    "cpu": os.getenv("Q_CPU", 'sum(rate(container_cpu_usage_seconds_total{deployment="{dep}",container!=""}[2m]))'),
    "rps": os.getenv("Q_RPS", 'sum(rate(http_requests_total{deployment="{dep}"}[1m]))'),
    "mem": os.getenv("Q_MEM", 'sum(container_memory_working_set_bytes{deployment="{dep}",container!=""})'),
    "pod_count": os.getenv("Q_PODS", 'count(kube_pod_info{deployment="{dep}"})'),
    "ready_pods": os.getenv("Q_READY", 'sum(kube_pod_status_ready{deployment="{dep}",condition="true"})'),
}

bundle = joblib.load(MODEL_PATH)
if DEPLOYMENT not in bundle["deployments"]:
    raise RuntimeError(f"{DEPLOYMENT} not in model; trained on {list(bundle['deployments'])}")
STATIC = bundle["deployments"][DEPLOYMENT]

app = FastAPI(title="PreScale")


@app.get("/health")
def health():
    return {"status": "ok", "deployment": DEPLOYMENT, "backend": bundle["backend"],
            "horizon_min": bundle["horizon_min"]}


@app.get("/predict")
async def predict():
    end = int(time.time())
    start = end - LOOKBACK_MIN * 60
    series = {}
    try:
        async with httpx.AsyncClient(timeout=5) as client:
            for name, tmpl in QUERIES.items():
                r = await client.get(f"{VM_URL}/api/v1/query_range", params={
                    "query": tmpl.replace("{dep}", DEPLOYMENT),
                    "start": start, "end": end, "step": "60s"})
                r.raise_for_status()
                series[name] = vm_series(r.json())
    except (httpx.HTTPError, ValueError) as exc:
        return fallback(f"metrics unavailable: {exc}")

    if any(s.empty for s in series.values()):
        return fallback("one or more metrics returned no data")

    grid = build_grid(series, STATIC)
    now = pd.Timestamp.now(tz="UTC").tz_convert(None)
    result = predict_from_grid(grid, bundle, now)
    result["deployment"] = DEPLOYMENT
    result["generated_at"] = now.isoformat()
    return result
