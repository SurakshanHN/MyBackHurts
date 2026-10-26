# Prediction Service Interface Contract (DRAFT)

**Status**: DRAFT  
**Protocol**: HTTP/1.1 REST JSON  
**Consumer**: `cmd/prescaler` daemon  
**Default Service Endpoint**: `http://predictor.predictive.svc.cluster.local:8000/predict`  

---

## 1. Current Implementation Specification

The prescaler currently integrates with the prediction service via a single synchronous HTTP POST endpoint.

### Request

```http
POST /predict HTTP/1.1
Host: predictor.predictive.svc.cluster.local:8000
Content-Type: application/json

{
  "window": [
    12.4,
    14.1,
    13.8,
    18.5,
    24.2,
    31.0
  ]
}
```

#### Fields

| Field | Type | Description |
| :--- | :--- | :--- |
| `window` | `[]float64` | Chronological array of historical requests-per-second (RPS) metrics evaluated over the historical window. |

### Response

#### 200 OK

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "predicted_rps": 142.5
}
```

#### Fields

| Field | Type | Constraints | Description |
| :--- | :--- | :--- | :--- |
| `predicted_rps` | `float64` | `predicted_rps >= 0.0` | Forecasted traffic load in requests per second. Negative numbers are rejected as errors by the prescaler client. |

#### Error Responses
- **Non-200 Status Codes** (e.g. `500 Internal Server Error`, `503 Service Unavailable`): Treated as an evaluation failure by the prescaler.
- **Malformed JSON / Type Mismatch**: Logged as an error and counted toward the consecutive failure release threshold (`--max-failures`, default 3).

---

## 2. Replica Derivation Formula

The Go prescaler client translates `predicted_rps` into recommended replicas via `ReplicasFor()`:

$$\text{raw} = \frac{\text{predicted\_rps} \times \text{headroom}}{\text{conns\_per\_pod}}$$

$$\text{needed} = \left\lceil \text{raw} \right\rceil$$

Subject to runtime clamps:
$$\text{clamped} = \max(\text{minReplicas}, \min(\text{maxReplicas}, \text{needed}))$$

- `headroom`: multiplicative buffer (default `1.20` = 20% safety margin).
- `conns_per_pod`: concurrency width per replica (default `20`).
- `minReplicas`: baseline minimum (default `3`).
- `maxReplicas`: ceiling read dynamically from `ScaledObject.spec.maxReplicaCount` (default `10`).

---

## 3. Required Lead Time & Lookahead Horizon

Live cluster measurements on kind (Part 4B) demonstrate the following end-to-end actuation latency chain:
1. **Prescaler Tick Latency**: 0–15s (average ~8–13s depending on tick alignment).
2. **KEDA HPA Synchronization**: < 1.0s (ScaledObject patch to HPA `minReplicas` update).
3. **Pod Startup & Readiness Gating**: ~35–42s (including 30s `STARTUP_DELAY_SECONDS` + readiness probe period).

**Total End-to-End Chain Latency**: **~50–55 seconds**.

> [!IMPORTANT]
> **Forecast Horizon Requirement**: The predictor's forecast horizon MUST exceed the measured chain time of **~55s plus safety margin**. A minimum lookahead horizon of **60s to 90s** is required for proactive scaling to bring pods to `1/1 Ready` before the surge arrives at the checkout service.

---

## 4. Questions for the Model Owner

Before finalizing the prediction service for Part 5, the model owner must clarify the following architectural and modeling questions:

1. **Input Features**:
   - Is `window` intended to carry only raw request rate (`RPS`), or does the model require multidimensional telemetry (e.g. error rate, p99 latency, active TCP connections to DB, or time-of-day features)?
2. **History Length**:
   - What sequence length was the model trained on? (The prescaler currently defaults to `--window=5m`, generating 20 points at 15s step). Does the model require fixed-length vectors (e.g. exactly $N=30$ points) or variable-length inputs?
3. **Sampling Step**:
   - What step size does the model assume between points (e.g., 5s, 10s, 15s)? Mismatched step intervals will distort temporal gradients in autoregressive models.
4. **Output Meaning**:
   - Is `predicted_rps` strictly an expected point estimate of request volume, an upper confidence bound (e.g., 90th percentile), or a surge probability $\in [0.0, 1.0]$?
5. **Forecast Horizon**:
   - What exact lookahead horizon $T_{horizon}$ is modeled? Does the forecast predict load at $t + 60\text{s}$, average load over $[t, t + 60\text{s}]$, or peak load over that interval?
6. **Python and Machine Learning Runtime**:
   - What exact versions of Python (e.g., Python 3.11, 3.12) and machine learning libraries (`scikit-learn`, `numpy`, `torch`, `onnxruntime`) are required to load and execute the saved model artifact?
7. **Training Traffic Pattern**:
   - What traffic distribution and surge profile was used during training (e.g., step surges, linear ramps, periodic sinusoidal, Poisson burst)? Are extreme tail events represented in the dataset?
