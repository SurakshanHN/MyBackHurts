# Stand-in Prediction Service

> **Status:** DRAFT (Invented by prescaler team; teammate has not yet agreed)
> **Runtime:** Python 3.12 (packaged in Docker)
> **Dependencies:** Python standard library only (`http.server`, `json`, `time`, `os`, `urllib.parse`)

## Purpose
Prescaler requires a forecast service on port 8000. Because the teammate's serialized model (`.pkl`) is not yet available, this stand-in service implements the exact draft-1 HTTP contract using a deterministic synthetic forecast generated from the demo surge schedule.

## Contract Endpoints

- `GET /healthz`
  Returns `200 OK` with body `ok`.

- `GET /predict?horizon=<seconds>`
  Returns forecasted traffic at `now + horizon_seconds` (default horizon: 90s).
  Response payload:
  ```json
  {
    "predicted_rps": 490.0,
    "horizon_seconds": 90,
    "generated_at": 1793047200.0,
    "model": "standin-schedule",
    "contract": "draft-1"
  }
  ```

- `POST /schedule`
  Configures surge timing at runtime:
  ```json
  {
    "surge_start_epoch": 1793047230.0
  }
  ```

- `POST /fail`
  Simulates failure behaviors for testing prescaler release-on-failure:
  - `error`: Returns HTTP 500
  - `slow`: Sleeps 10 seconds before responding
  - `garbage`: Returns HTTP 200 with non-JSON body
  - `none`: Clears failure mode

## Model Integration
Model loading is isolated in `load_model()` in `server.py`. When the teammate's `.pkl` artifact is delivered, it can be loaded in place without changing the HTTP routing or failure harness.
