# Report Part 6: Predictor Service (Stand-in) & /predict Contract

## 1. Image & Base Tag Verified
- Image built: `prescale-predictor:0.1.0` (pinned, no `:latest`).
- Base image: `public.ecr.aws/docker/library/python:3.12-slim` (Python 3.12.15).
- Manifest digest: `sha256:e8a38829209aea82774882e5157e1f7c3e8c8393ff53a697b3580896e57f345c` (linux/amd64).

## 2. Curl Results (MEASURED)
- Port bind note: `127.0.0.1:8000` is bound by kind control-plane (`extraPortMappings` 30800); container bound to loopback `127.0.0.2:8000`.
- [01_healthz.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/01_healthz.txt): `HTTP/1.0 200 OK`, body: `ok` (MEASURED).
- [02_predict_noschedule.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/02_predict_noschedule.txt): `predicted_rps: 60.0`, `horizon_seconds: 90` (MEASURED).
- [03_post_schedule.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/03_post_schedule.txt): set `surge_start_epoch = now+30` (MEASURED).
- [04_predict_horizon90.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/04_predict_horizon90.txt): `predicted_rps: 490.0`, `horizon_seconds: 90` (MEASURED).
- [05_predict_horizon10.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/05_predict_horizon10.txt): `predicted_rps: 60.0`, `horizon_seconds: 10` (MEASURED).
- [06_fail_error.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/06_fail_error.txt): `HTTP/1.0 500 Internal Server Error` (MEASURED).
- [07_fail_slow.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/07_fail_slow.txt): `time_total: 10.001s`, HTTP 200 (MEASURED).
- [08_fail_garbage.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/08_fail_garbage.txt): `HTTP/1.0 200 OK`, body `GARBAGE_NON_JSON_BODY_<<<>>>` (MEASURED).
- [09_fail_clear.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part6/09_fail_clear.txt): `predicted_rps: 490.0`, HTTP 200 (MEASURED). Container stopped & removed.

## 3. Go Client vs Contract & Tests
- Mismatch differences:
  1. Method: Go client expects `POST /predict`, contract specifies `GET /predict?horizon=<sec>`.
  2. Request payload: Go client sends JSON `{"window": []float64}`, contract sends query param `horizon`.
  3. Response payload: Go client expects `{"predicted_rps": float}`, contract adds `horizon_seconds`, `generated_at`, `model`, `contract`.
- Go test suite: 54 passed, 0 failed across all packages (MEASURED). Go code kept unchanged.

## 4. AGENTS.md Conflicts
- [AGENTS.md](file:///home/unhealthycoder/MyBackHurts/AGENTS.md) documents Kubebuilder multi-group scaffold removed in Part 2; active repo uses standalone daemon & KEDA.

## 5. Scope Check
- `git diff` touches only `predictor/**` and `docs/**` (no drift). Prescaler replicas: 0 (verified read-only).

## 6. Terminal Output
```text
$ git --no-pager status --short
# clean
$ git --no-pager log --oneline 45823feab8646efc16f813513e1c93f5f120af43..HEAD
8653c96 docs: part 6 report
95c2a53 docs: predict contract draft
a3e27e4 predictor: image and verification
f202a20 predictor: standin service
```
