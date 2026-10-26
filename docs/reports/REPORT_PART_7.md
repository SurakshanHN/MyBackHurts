# Report Part 7: Contract Fix, Prescaler Wiring, A/B Runs

## 1. Go Test Counts
- Part 4D: 83 tests (MEASURED)
- Part 6: 54 top-level tests (MEASURED)
- Part 7: 83 PASS events (54 top-level + 29 subtests) (MEASURED, [gotests.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/gotests.txt))

## 2. Contract Fix and Verification
- Server implementation: [predictor/server.py](file:///home/unhealthycoder/MyBackHurts/predictor/server.py), contract documentation: [docs/contract.md](file:///home/unhealthycoder/MyBackHurts/docs/contract.md)
- Curl verification files: [01_post_predict_noschedule.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/01_post_predict_noschedule.txt), [02_post_schedule.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/02_post_schedule.txt), [03_post_predict_surge.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/03_post_predict_surge.txt), [04_fail_error_post.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/04_fail_error_post.txt), [05_fail_slow_post.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/05_fail_slow_post.txt), [06_fail_garbage_post.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/06_fail_garbage_post.txt)

## 3. Prescaler Config & Gate Result
- Settings ([prescaler_config.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/prescaler_config.txt)): URL `http://172.19.0.100:8000/predict`, interval `15s`, horizon `90s` (stand-in default, unconfigurable in daemon), lease `60s`, cooldown `30s`, clamps `min=3, max=10`, capacity `80 rps` (`--conns-per-pod=80 --headroom=1.0`, 490/80=7 THEORETICAL).
- Gate: PASSED. Forecasts 60 then 490 received, desired=7 computed, dry-run raise min 3->6 logged ([gate_prescaler_logs.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/gate_prescaler_logs.txt)).

## 4. A/B Performance Table ([ab_summary.csv](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part7/ab_summary.csv))
- `breach_steps = sum(1 for val in p99_series.values() if val > slo_p99_ms); breach_seconds = breach_steps * 5`
- `pod_seconds = int(sum(ready_series.values()) * 5)`

| Run | Side | Cl p99 (ms) | Srv p99 (ms) | Breach | Fail% | Non2xx | DropIter | MaxReady | 1stScale | Pod*s |
|:---|:---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 5B-R2 (ref) | base | 2172.80 | 2539.66 | 80s | 0.02% | 1 | 8012 | 10 | 70s | 2175 |
| 5B-R2 (ref) | pred | 2527.88 | 4339.42 | 90s | 0.00% | 0 | 7650 | 10 | 70s | 2080 |
| B1 | base | 2164.50 | 2540.52 | 90s | 0.06% | 2 | 9582 | 10 | 83s | 2065 |
| B1 | pred | 39.59 | 58.52 | 0s | 0.01% | 0 | 0 | 10 | 3s | 2635 |
| B2 | base | 1829.51 | 2543.82 | 80s | 0.03% | 0 | 6151 | 10 | 56s | 2170 |
| B2 | pred | 37.72 | 63.01 | 0s | 0.01% | 0 | 0 | 10 | 1s | 2575 |
*(All table values MEASURED)*

## 5. Prescaler Timeline
- B1: boost offset -57s, boosted min 7, release offset +77s, cleaned yes, ready@SURGE 6, ready@SURGE+30s 7 (MEASURED).
- B2: boost offset -58s, boosted min 7, release offset +79s, cleaned yes, ready@SURGE 6, ready@SURGE+30s 7 (MEASURED).

## 6. Known Limits Carried Over
- Per-pod RPS and CPU do not reconcile with client RPS (carried over from 5B, UNEXPLAINED).
- k6 drops iterations on baseline side during surge (carried over from 5B, UNEXPLAINED).

## 7. AGENTS.md Conflicts & Scope Check
- AGENTS.md specifies Kubebuilder controller/scaffold removed in Part 2; active repo uses standalone daemon & KEDA.
- Scope: `git diff --stat START_HEAD..HEAD` strictly touches `deploy/prescaler/**`, `docs/**`, `predictor/**`, `scripts/load/**`.

## 8. Terminal Output
```text
$ git --no-pager status --short
# clean
$ git --no-pager log --oneline 89cbcdc87b8a9d2442ed05fe6c9cb3b7366bd22c..HEAD
3cccbb7 docs: part 7 report
31785e1 measure: A/B raw results
68ab705 loadtest: save prescaler logs in ab_run.sh
b716f85 deploy: use FQDN trailing dot for in-cluster victoria url
49f63cc loadtest: A/B run script
4e96da8 deploy: prescaler wired to standin
930ba38 predictor: accept go client contract
e77aa14 measure: part 7 go test counts
```
