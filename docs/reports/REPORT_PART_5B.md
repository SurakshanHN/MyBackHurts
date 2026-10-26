# Report Part 5B: True Knee, CPU Request Fix, Surge Profile, A/A Replay

## 1. Summary
- **KNEE_3 & BRACKETED:** KNEE_3 = **330 RPS** (329.14 RPS achieved) (MEASURED); BRACKETED = **yes** (360 RPS breached).
- **CPU Source & m/RPS:** `container_cpu_usage_seconds_total` (cadvisor); **3.28 to 5.06 mCPU/RPS** per pod (MEASURED).
- **HPA Scale-Out Point:** **39.53 to 60.98 RPS/pod** (118.6 to 182.9 RPS total), **35.9% to 55.6%** of KNEE_3/3 (THEORETICAL).
- **PEAK_RPS:** **490 RPS** (THEORETICAL: `min(500, floor(1.5 * 330 / 10) * 10) = 490`). Gate did not fire (proceeded to 2–5).

## 2. AGENTS.md Conflicts
- [AGENTS.md](file:///home/unhealthycoder/MyBackHurts/AGENTS.md) specifies Kubebuilder controller/scaffold removed in Part 2; active repo uses standalone daemon & KEDA.

## 3. Knee Table ([capacity_v2.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part5b/capacity_v2.csv))
| Stage | Achieved | Drop | p50 (ms) | p95 (ms) | p99 (ms) | Fail % | CPU (m) | Pod RPS (min/max) | m/RPS/pod | SLO |
|---:|---:|---:|---:|---:|---:|---:|---:|:---:|---:|:---:|
| 330 | 329.14 | 0 | 67.67 | 166.30 | 185.34 | 0.00% | 359.51 | 77.75 / 86.25 | 3.28 | OK |
| 360 | 339.16 | 363 | 707.78 | 1293.86 | 1412.72 | 0.00% | 572.31 | 89.30 / 106.80 | 5.06 | BREACH |
*Rule fired:* First breached stage: 360 RPS (p99 1412.72ms > 300.0ms; dropped 363 > 1% target (144)).

## 4. Request Change ([hpa_scaleout_math.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part5b/hpa_scaleout_math.txt))
- Rollout: both `baseline` and `predictive` checkouts rolled out successfully with 400m CPU request (3/3 Ready).
- Overlay diff: identical except namespace, labels (`prescale.io/side`), and nodePorts (`30081`/`30083` vs `30082`/`30084`).
- HPA status: both namespaces show `cpu: 0%/50%` target, min=3, max=10, replicas=3.

## 5. A/A Replay Table ([aa_summary.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part5b/aa_summary.csv))
| Run | Side | ReUse | Reqs | Achieved | Fail% | Drop | Cl p95/p99 (ms) | Srv Breach | Srv p99 | Non2xx | First/Max Scale | Max | Pod*s | DB |
|:---|:---|:---:|---:|---:|---:|---:|:---:|---:|---:|---:|:---:|---:|---:|---:|
| R1 | base | 1 | 67304 | 192.30 | 0.01% | 22495 | 1594 / 1685 | 180s | 2547ms | 0 | 60s / 90s | 8 | 1910 | 3 |
| R1 | pred | 1 | 68175 | 194.78 | 0.00% | 21624 | 1627 / 1697 | 180s | 2544ms | 0 | 75s / 75s | 7 | 1780 | 17 |
| R2 | base | 0 | 81787 | 233.67 | 0.02% | 8012 | 1918 / 2173 | 80s | 2540ms | 1 | 70s / 145s | 10 | 2175 | 7 |
| R2 | pred | 0 | 82149 | 234.71 | 0.00% | 7650 | 2148 / 2528 | 90s | 4339ms | 0 | 70s / 145s | 10 | 2080 | 7 |
| R3 | base | 1 | 66405 | 189.73 | 0.00% | 23394 | 1613 / 1699 | 180s | 2547ms | 0 | 65s / 80s | 8 | 1965 | 9 |
| R3 | pred | 1 | 68038 | 194.39 | 0.00% | 21761 | 1601 / 1693 | 180s | 2545ms | 0 | 65s / 80s | 7 | 1780 | 20 |

## 6. Per-Pod RPS Spread & Connection Behavior
- +110s max/min ratio: R1 (1.04–1.06), R2 (1.08–1.09), R3 (1.02–1.09).
- +140s max/min ratio: R1 (1.01–1.05), R2 (1.07–1.09), R3 (1.02–1.06). Keep-alive (R1/R3) showed tighter spread.
- Connect errors: 0 across all runs (`cannot assign address`: 0, `connection refused`: 0, `i/o timeout`: 0).
- TIME-WAIT count at end: R1: 28, R2: 6534 (high connection churn with NO_REUSE=1), R3: 2174.

## 7. Final vs Initial State
- Initial: checkout CPU request 100m, prescaler replicas 1, both ScaledObjects min=3 max=10.
- Final: checkout CPU request 400m, prescaler 1/1 Ready, both ScaledObjects min=3 max=10, all pods Ready.

## 8. Scope & Errors
- Diff scope verified: strictly within `loadtest/**`, `scripts/load/**`, `workload/k8s/**`, `docs/reports/**`.
- No unhandled errors; all gate and verification checks passed.

## 9. Terminal Summary
```text
$ git --no-pager status --short
# clean
$ git --no-pager log --oneline af1a4a2163945885febb3aa0ab4d6f009237ed85..HEAD
64c0d2b docs: part 5b report
b2f6657 docs: part 5b verification
683e351 measure: A/A replay raw results
d18c500 loadtest: A/A run script
e5bca1f loadtest: surge profile for demo
741fd70 measure: hpa scale-out arithmetic
9227ff7 workload: raise checkout cpu request to 400m on both sides
6a0a35b measure: true knee raw results
859091d loadtest: capacity probe v2 script
```
