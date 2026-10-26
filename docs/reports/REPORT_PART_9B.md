# Part 9B: Real Failure-Release Test (No Load)

## 1. Preflight
- START_HEAD: `be8fc43`
- Prescaler flags: `max-failures=3`, `interval=15s`, `boost-ttl=60s`
- Initial state: prescaler replicas=0, both ScaledObjects min=3, annotations=clean

## 2. Results (G1-G5)
| Case | Boost Epoch | Fail Epoch | Release Epoch | Offset (s) | Release Reason | Cleaned | Not Released |
|---|---|---|---|---|---|---|---|
| G1 (error) | 1793054764.24 | 1793054764.24 | 1793054809.85 | 45.61 | consecutive failures (3) reached max-failures (3) | yes | false |
| G2 (slow) | 1793054819.46 | 1793054819.46 | 1793054871.24 | 51.78 | consecutive failures (3) reached max-failures (3) | yes | false |
| G3 (garbage) | 1793054883.90 | 1793054883.90 | 1793054927.46 | 43.56 | consecutive failures (3) reached max-failures (3) | yes | false |
| G4 (stopped) | 1793054937.04 | 1793054937.04 | 1793054986.73 | 49.68 | consecutive failures (3) reached max-failures (3) | yes | false |
| G5 (restart) | 1793055001.76 | 1793055001.76 | 1793055005.08 | 3.32 | desired (3) <= original (3) | yes | false |

## 3. Quoted Prescaler Release Logs
- G1: `time=2026-10-26T22:46:49.085Z level=INFO msg="Prescaler tick" forecast=0 desired=0 current=6 original=3 action=release reason="consecutive failures (3) reached max-failures (3)"`
- G2: `time=2026-10-26T22:47:49.450Z level=INFO msg="Prescaler tick" forecast=0 desired=0 current=6 original=3 action=release reason="consecutive failures (3) reached max-failures (3)"`
- G3: `time=2026-10-26T22:48:46.960Z level=INFO msg="Prescaler tick" forecast=0 desired=0 current=6 original=3 action=release reason="consecutive failures (3) reached max-failures (3)"`
- G4: `time=2026-10-26T22:49:45.092Z level=INFO msg="Prescaler tick" forecast=0 desired=0 current=6 original=3 action=release reason="consecutive failures (3) reached max-failures (3)"`
- G5: `time=2026-10-26T22:50:03.882Z level=INFO msg="Prescaler tick" forecast=60 desired=3 current=6 original=3 action=release reason="desired (3) <= original (3)"`

## 4. Scope Check & Git Verification
- Scope: `git diff be8fc43..HEAD --name-only` modified only `scripts/load/**` and `docs/**`
- `git --no-pager status --short`: clean
- `git --no-pager log --oneline be8fc43..HEAD`:
```
77e731c docs: part 9b report
aab0f6d test: Part 9B failure-release tests and raw data
```
