# prescale — Complete Demo & Scenario Simulation Suite

> **Forecaster**: The stand-in oracle predictor (`prescale-predictor:0.1.1`) is used for live surge schedules.
> The fast scenario simulator (`scripts/demo/simulator.py`) directly verifies the deterministic Go decision logic across real-world operational scenarios.

---

## 1. Quickstart: Interactive Demo Runner

Launch the unified interactive terminal demo:

```bash
bash scripts/demo/demo.sh
```

This presents a terminal control center:
```text
  ╔═══════════════════════════════════════════════════════════════════════╗
  ║     Predictive Kubernetes Auto-Scaler Demo & Scenario Simulator       ║
  ╚═══════════════════════════════════════════════════════════════════════╝

Current System Health & Cluster Status:
  • Cluster:           OK
  • Stand-in Predictor:RUNNING (172.19.0.100:8000)
  • Prescaler Daemon:  RUNNING (1/1)
  • Baseline SO Min:   3 pods (HPA reactive only)
  • Predictive SO Min: 3 pods (HPA + Prescaler boost)
-----------------------------------------------------------------------

Select a Demo Scenario or Action:
  [1] Fast Scenario Simulator (5 scenarios in <2s with ASCII timelines)
  [2] Live Scenario 1: A/B Traffic Surge (KEDA reactive vs Predictive)
  [3] Live Scenario 2: Predictor Outage & Fallback (Self-healing lease release)
  [4] Live Scenario 3: Lease TTL Expiry (Automatic stale boost cleanup)
  [5] Live Scenario 4: Daemon Crash Recovery (State reconstruction from K8s)
  [6] Run All Live Failure & Safety Scenarios (Full automated test suite)
  [7] Detailed Cluster Health & Recent Logs
  [8] Start Demo Stack (Predictor + Prescaler up)
  [9] Teardown & Reset Cluster State
  [0] Exit
```

---

## 2. Fast Scenario Simulator (Offline / < 2s)

For fast verification or demonstration without running full 6-minute k6 load tests, run the built-in simulator engine:

```bash
# Run all 5 scenarios with visual ASCII timelines:
python3 scripts/demo/simulator.py all

# Or run individual scenarios (e.g. Scenario 2):
python3 scripts/demo/simulator.py 2
```

### Simulated Scenarios Covered:

| # | Scenario | What it Tests | Expected Outcome |
|---|---|---|---|
| **S1** | **Anticipated Surge** | Traffic jumps 60 → 490 RPS, predicted 60s in advance. | Pre-warms `minReplicaCount` to 7 before spike arrives; flat latency curve. |
| **S2** | **Predictor Outage** | Predictor drops offline (HTTP 500 / timeout) mid-boost. | Holds lease across transient glitches, auto-releases to baseline min=3 after 3 failed ticks. |
| **S3** | **Safety Bounds & Clamping** | Huge flash crowd demands 20 replicas (ceiling is 10). | Enforces `maxStep=3` per tick and hard clamps at `maxLimit=10` ceiling. |
| **S4** | **Demand Drop & Cooldown** | Traffic abruptly subsides; noisy spikes follow. | Releases boost immediately; enforces 30s cooldown to prevent flapping. |
| **S5** | **Daemon Crash & Recovery** | Prescaler process killed during active boost. | New pod restarts and reconstructs lease state from ScaledObject annotations. |

### Visual Output Example:

```text
Replica Count & Traffic Timeline:
Time    | RPS    | Prescaled Min  | Visual Replica Level (1-10)    | Action    
---------------------------------------------------------------------------
+0s     | 60     | 3              | [██████░░░░░░░░░░░░░░] (3)     | NOOP
+15s    | 60     | 6              | [████████████░░░░░░░░] (6)     | RAISE
+30s    | 60     | 7              | [██████████████░░░░░░] (7)     | RAISE
+45s    | 60     | 7              | [██████████████░░░░░░] (7)     | HOLD
+60s    | 60     | 7              | [██████████████░░░░░░] (7)     | HOLD
+75s    | 490    | 7              | [██████████████░░░░░░] (7)     | HOLD
+90s    | 490    | 7              | [██████████████░░░░░░] (7)     | HOLD
+105s   | 490    | 3              | [██████░░░░░░░░░░░░░░] (3)     | RELEASE
+120s   | 60     | 3              | [██████░░░░░░░░░░░░░░] (3)     | NOOP
```

---

## 3. Live Cluster Scenarios

Run individual live scenarios directly from CLI flags or the menu:

```bash
# 1. Live A/B Surge Benchmark (KEDA reactive vs Prescale under live k6 traffic)
bash scripts/demo/demo.sh --live 1

# 2. Live Predictor Outage (Kill predictor container -> watch auto-release in cluster)
bash scripts/demo/demo.sh --live 2

# 3. Live TTL Lease Expiry (Expired timestamp detection and cleanup)
bash scripts/demo/demo.sh --live 3

# 4. Live Daemon Crash & Restart (Kill prescaler pod -> verify annotation reconstruction)
bash scripts/demo/demo.sh --live 4

# 5. Run full live failure & safety test suite (F1-F5)
bash scripts/demo/demo.sh --live all
```

---

## 4. Stack Lifecycle Commands

```bash
# Bring up predictor and prescaler daemon
bash scripts/demo/demo.sh --up      # or bash scripts/demo/up.sh

# Check current cluster status
bash scripts/demo/demo.sh --status

# Teardown demo state and release boosts
bash scripts/demo/demo.sh --down    # or bash scripts/demo/down.sh
```

---

## 5. Grafana Dashboard

- **Import script**: `scripts/grafana_import.sh`
- **Dashboard JSON**: `deploy/grafana/prescale-demo.json`
- **Port-forward / Local URL**: `http://localhost:3000` (admin / admin)
- Visualizes side-by-side RPS, server-side p99 latency, ready replica counts, and prescaler boost lease status.

---

## 6. Directory Layout

```
scripts/demo/
  demo.sh                   Unified Interactive Demo Runner
  simulator.py              High-speed visual scenario simulator
  up.sh                     Start predictor + prescaler daemon
  run.sh                    Run live A/B load test
  down.sh                   Tear down demo state and release boost
  scenarios/
    s1_surge_ab.sh          Live A/B benchmark runner
    s2_predictor_outage.sh  Live predictor failure & recovery
    s3_ttl_expiry.sh        Live TTL expiration test
    s4_crash_recovery.sh    Live daemon crash recovery test
scripts/
  failure_test.sh           Full F1-F5 failure scenario tests
  load/ab_run.sh            Concurrent A/B k6 runner
  grafana_import.sh         Grafana dashboard import via API
docs/
  DEMO.md                   This guide
  BACKUP.md                 Cluster state backup/restore guide
  reports/                  Per-part analysis reports & test data
```
