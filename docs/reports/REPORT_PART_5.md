# Report Part 5: k6 Harness and Capacity Calibration

## 1. Summary
- **k6 image tag:** `grafana/k6:2.2.0` (pinned in [loadtest/K6_IMAGE](file:///home/unhealthycoder/MyBackHurts/loadtest/K6_IMAGE)).
- **Capacity:** KNEE_RPS_3PODS = **299.80 RPS** (MEASURED) vs **300 RPS** (THEORETICAL); KNEE_PER_POD = **99.93 RPS** (MEASURED) vs **100 RPS** (THEORETICAL).
- **CPU Cost:** MCPU_PER_RPS = **3.27 mCPU/RPS** (MEASURED) at knee (pod CPU avg = 327.2 mCPU).
- **Stop rule fired:** None. All stages 30–300 RPS passed SLO (p99 <= 300 ms, err <= 1%). At 300 RPS, pods hit 500m limit (p99 rose to 182 ms).
- **Scale-out point:** Current HPA scale-out triggers at 15.27 RPS/pod (~15.3% of knee), far below the pod's true capacity.

## 2. AGENTS.md Conflicts
- [AGENTS.md](file:///home/unhealthycoder/MyBackHurts/AGENTS.md) documents a removed Kubebuilder operator; current repo uses a standalone Go daemon and KEDA.

## 3. Dockerfile Fix
- In [cmd/prescaler/Dockerfile](file:///home/unhealthycoder/MyBackHurts/cmd/prescaler/Dockerfile), replaced `golang:1.27-alpine` with `golang:1.26.10-alpine`. Final-stage `alpine:3.21` exists.

## 4. Capacity Table ([capacity.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part5/capacity.csv))
| Target | Achieved | Drop | p50 (ms) | p95 (ms) | p99 (ms) | Fail % | CPU (m) | Load1 | Mem (MiB) | DB Conns | SLO |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|:---:|
| 30 | 30.01 | 0 | 17.00 | 17.56 | 18.05 | 0.00% | 30.0 | 0.46 | 3756.9 | 1 | OK |
| 60 | 60.00 | 0 | 16.52 | 16.89 | 17.16 | 0.00% | 115.3 | 0.68 | 3645.8 | 1 | OK |
| 90 | 89.98 | 0 | 16.40 | 16.84 | 17.16 | 0.00% | 133.7 | 0.97 | 3640.9 | 1 | OK |
| 120 | 119.97 | 0 | 16.54 | 17.19 | 17.59 | 0.00% | 197.3 | 2.15 | 3678.6 | 1 | OK |
| 150 | 149.96 | 0 | 16.44 | 16.88 | 17.06 | 0.00% | 234.3 | 1.63 | 3702.2 | 1 | OK |
| 180 | 179.95 | 0 | 16.39 | 17.06 | 17.63 | 0.00% | 221.3 | 1.96 | 3747.8 | 2 | OK |
| 210 | 209.93 | 0 | 16.38 | 17.27 | 18.27 | 0.00% | 290.2 | 2.30 | 3703.2 | 3 | OK |
| 240 | 239.92 | 0 | 16.31 | 17.42 | 20.76 | 0.00% | 296.5 | 2.92 | 3762.9 | 3 | OK |
| 270 | 269.91 | 0 | 16.48 | 25.67 | 33.79 | 0.00% | 325.2 | 2.70 | 3804.2 | 3 | OK |
| 300 | 299.80 | 0 | 17.10 | 120.70 | 182.18 | 0.00% | 327.2 | 2.05 | 3728.6 | 4 | OK |

## 5. Trigger Arithmetic ([trigger_math.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part5/trigger_math.txt))
- **(a)** Current HPA (50% of 100m = 50m): scales out at **15.27 RPS/pod** (45.8 RPS for 3 pods), or **15.28% of KNEE_PER_POD** (THEORETICAL).
- **(b)** 60% of knee = 59.96 RPS/pod (196.3m CPU). CPU request to place 50% target at 60% knee is **392.6m CPU** (~393m). Fits under 500m limit (THEORETICAL).
- **(c)** RPS-based trigger at 60% of knee: **59.96 RPS per pod** (~180 RPS for 3 pods) (THEORETICAL).

## 6. surge.js Summary
- 4 stages: (1) baseline 20 RPS for 1m; (2) ramp 20->120 RPS over 1m; (3) sustained 120 RPS for 2m; (4) cooldown 120->20 RPS over 1m.

## 7. Final State vs Initial State
- Initial and final states match exactly: `baseline` and `predictive` ScaledObjects min 3, max 10; HPAs min 3, max 10; checkout 3/3 Ready; prescaler 1/1 Ready. All pods Ready.

## 8. Scope Check & Verification
- `git diff --stat START_HEAD..HEAD` strictly modified allowed paths: `cmd/prescaler/Dockerfile`, `docs/reports/**`, `loadtest/**`, `scripts/load/**`. Zero drift.

## 9. Terminal Summary
- `git --no-pager status --short`: `?? q`
- `git --no-pager log --oneline START_HEAD..HEAD`:
  ```
  4352ba0 docs: part 5 verification
  1e7c19f measure: trigger arithmetic (theoretical)
  dc210c5 measure: capacity probe raw results
  9892583 loadtest: capacity probe scripts
  af27796 loadtest: pin k6 image and add smoke test
  865e314 fix(prescaler): pin existing golang base image tag
  ```
