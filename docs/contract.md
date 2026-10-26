# Prediction Service Interface Contract

> **Status:** Final DRAFT (Invented by prescaler harness; teammate has not yet agreed)  
> **Protocol:** HTTP/1.1 REST JSON  
> **Service Port:** 8000  
> **Contract Version:** `draft-1`  

---

## 1. Specification

### `GET /healthz`
Health check endpoint.
- **Response:** `200 OK`, `Content-Type: text/plain`, body: `"ok\n"`

### `GET /predict?horizon=<seconds>`
Forecast traffic load `horizon` seconds into the future.
- **Query Parameters:**
  - `horizon` (*integer*, optional, default: `90`): Lookahead horizon in seconds.
- **Response `200 OK`:**
  - `Content-Type: application/json`
  - **Schema:**
    ```json
    {
      "predicted_rps": 490.0,
      "horizon_seconds": 90,
      "generated_at": 1793048333.92,
      "model": "standin-schedule",
      "contract": "draft-1"
    }
    ```
- **Fields & Units:**
  - `predicted_rps` (*float*): Forecasted load in requests per second (RPS) at target time $(t_{\text{now}} + \text{horizon})$. Non-negative.
  - `horizon_seconds` (*integer*): Evaluated lookahead window in seconds.
  - `generated_at` (*float*): Unix epoch timestamp of evaluation in seconds.
  - `model` (*string*): Model identifier (e.g. `"standin-schedule"`).
  - `contract` (*string*): Contract version string (`"draft-1"`).

---

## 2. Failure Behaviors & Resilience Contract

The service implements testable failure modes (`FAIL_MODE` or `POST /fail`):
1. **Error (`500 Internal Server Error`):**
   - Returns HTTP 500 with JSON error payload.
   - Client contract: Prescaler increments consecutive failure counter and releases lease after reaching `--max-failures` (default 3), reverting control to baseline HPA.
2. **Slow (`sleep 10s`):**
   - Service stalls response for 10 seconds.
   - Client contract: Client HTTP timeout (5s) trips `context deadline exceeded` and treats it as an evaluation failure.
3. **Garbage (Non-JSON Body):**
   - Returns `200 OK` with non-JSON string body (`Content-Type: text/plain`).
   - Client contract: Client JSON decoder fails, logged as parse error, releasing lease safely.

---

## 3. Horizon Sizing & Timing Floor

- **Default Horizon:** **90 s**
- **Horizon Floor:** **~75 s** (THEORETICAL)

### Actuation Chain Breakdown:
1. **Prescaler Tick Alignment:** up to 15 s (average ~8–12 s)
2. **KEDA ScaledObject -> HPA Update:** ~2–5 s
3. **Pod Provisioning, Startup Delay & Readiness Probe:** ~45–49 s (30 s `STARTUP_DELAY_SECONDS` + container start + probe)
4. **Metrics Ingestion Lag (VictoriaMetrics):** ~5–7 s

$$\text{Total Actuation Floor} \approx 15\text{s} + 5\text{s} + 49\text{s} + 6\text{s} \approx 75\text{ s (THEORETICAL)}$$

A minimum forecast horizon floor of **~75 s** is required for proactive scale-out to bring additional replicas to `1/1 Ready` before incoming traffic escalates. The default horizon of **90 s** provides a comfortable safety buffer.

---

## 4. Seven Questions for the Model Owner

Before substituting the stand-in service with the teammate's `.pkl` artifact, the model owner must clarify:

1. **Features:** What specific telemetry features does the model take as input? (Raw checkout RPS only, or multidimensional signals such as p99 latency, DB active connection count, or error rates?)
2. **History Length:** What sequence or window length of historical observations is required during inference? (e.g., 5-minute sliding window, fixed $N$ points, or variable?)
3. **Time Step:** What fixed sampling interval or step size does the model expect between consecutive historical points (e.g., 5 s, 10 s, 15 s)?
4. **Output Meaning:** What is the precise semantic meaning of `predicted_rps`? (Point estimate of mean RPS, upper quantile/confidence bound, or surge probability $\in [0, 1]$?)
5. **Horizon:** What exact lookahead horizon $T_{\text{horizon}}$ was modeled during training? (Point forecast at $t + T$, average expected traffic over $[t, t + T]$, or peak rate over the interval?)
6. **Python & Library Versions:** What exact Python version (e.g., 3.12.x) and `scikit-learn` / `numpy` / `joblib` versions were used to serialize the `.pkl` artifact?
7. **Training Pattern & Dataset:** What traffic profiles and dataset distributions were used for training? (Step surges, linear ramps, diurnal patterns, synthetic vs. production traces?)
