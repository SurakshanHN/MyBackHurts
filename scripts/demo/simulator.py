#!/usr/bin/env python3
"""
prescale Scenario Simulator Engine
Simulates the exact pure decision logic of PlanDecision (from internal/scaledobject/plan.go)
across simulated time steps and diverse real-world operational scenarios.
No external dependencies (Python 3 stdlib only).
"""

import sys
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone, timedelta
from typing import Optional, List, Tuple

# ANSI color codes
BOLD = "\033[1m"
GREEN = "\033[32m"
CYAN = "\033[36m"
YELLOW = "\033[33m"
RED = "\033[31m"
BLUE = "\033[34m"
MAGENTA = "\033[35m"
RESET = "\033[0m"

@dataclass
class Config:
    baseline_min: int = 3
    max_limit: int = 10
    boost_ttl_sec: float = 60.0
    max_failures: int = 3
    max_step: int = 3
    cooldown_sec: float = 30.0

@dataclass
class State:
    current_min_replicas: int = 3
    max_replica_count: int = 10
    original_min: int = 0
    boosted_until: Optional[float] = None
    consecutive_failures: int = 0
    last_release_time: Optional[float] = None

@dataclass
class Plan:
    action: str  # raise, hold, release, noop, error
    new_min: int
    save_original: int = 0
    new_boosted_until: Optional[float] = None
    clear_annotations: bool = False
    new_failures: int = 0
    new_release_time: Optional[float] = None
    reason: str = ""

def plan_decision(s: State, cfg: Config, now: float, desired: int, tick_err: Optional[str]) -> Plan:
    """Exact port of PlanDecision in internal/scaledobject/plan.go"""
    baseline = cfg.baseline_min if cfg.baseline_min > 0 else 3
    original = s.original_min if s.original_min > 0 else s.current_min_replicas
    if original <= 0:
        original = baseline

    is_boosted = s.original_min > 0

    ceiling = s.max_replica_count
    if cfg.max_limit > 0 and (ceiling <= 0 or cfg.max_limit < ceiling):
        ceiling = cfg.max_limit
    if ceiling <= 0:
        ceiling = 10

    max_step = cfg.max_step if cfg.max_step > 0 else 3
    boost_ttl = cfg.boost_ttl_sec if cfg.boost_ttl_sec > 0 else 60.0
    max_failures = cfg.max_failures if cfg.max_failures > 0 else 3
    cooldown = cfg.cooldown_sec if cfg.cooldown_sec > 0 else 30.0

    # 1. Handle tick failure
    if tick_err is not None:
        new_failures = s.consecutive_failures + 1
        if is_boosted:
            if new_failures >= max_failures:
                return Plan(
                    action="release",
                    new_min=original,
                    clear_annotations=True,
                    new_failures=new_failures,
                    new_release_time=now,
                    reason=f"consecutive failures ({new_failures}) reached max-failures ({max_failures})",
                )
            if s.boosted_until is not None and now >= s.boosted_until:
                return Plan(
                    action="release",
                    new_min=original,
                    clear_annotations=True,
                    new_failures=new_failures,
                    new_release_time=now,
                    reason="boost lease expired during failure",
                )
            return Plan(
                action="hold",
                new_min=s.current_min_replicas,
                new_failures=new_failures,
                new_release_time=s.last_release_time,
                reason=f"failure {new_failures}/{max_failures} (holding lease): {tick_err}",
            )
        return Plan(
            action="error",
            new_min=s.current_min_replicas,
            new_failures=new_failures,
            new_release_time=s.last_release_time,
            reason=f"tick error: {tick_err}",
        )

    # Tick succeeded
    new_failures = 0

    # 2. Check lease expiry
    if is_boosted and s.boosted_until is not None and now >= s.boosted_until:
        return Plan(
            action="release",
            new_min=original,
            clear_annotations=True,
            new_failures=new_failures,
            new_release_time=now,
            reason="boost lease expired",
        )

    # 3. Desired at or below original baseline
    if desired <= original:
        if is_boosted:
            return Plan(
                action="release",
                new_min=original,
                clear_annotations=True,
                new_failures=0,
                new_release_time=now,
                reason=f"desired ({desired}) <= original ({original})",
            )
        return Plan(
            action="noop",
            new_min=s.current_min_replicas,
            new_failures=0,
            new_release_time=s.last_release_time,
            reason=f"desired ({desired}) <= baseline ({original})",
        )

    # 4. Desired > original: check cooldown
    if not is_boosted and s.last_release_time is not None and (now - s.last_release_time) < cooldown:
        rem = cooldown - (now - s.last_release_time)
        return Plan(
            action="noop",
            new_min=s.current_min_replicas,
            new_failures=0,
            new_release_time=s.last_release_time,
            reason=f"in cooldown ({rem:.1f}s remaining)",
        )

    # 5. Clamps
    target = desired
    if target < original:
        target = original
    if target > ceiling:
        target = ceiling
    if target > s.current_min_replicas + max_step:
        target = s.current_min_replicas + max_step

    # 6. Action
    if target > s.current_min_replicas:
        save_orig = original if not is_boosted else 0
        return Plan(
            action="raise",
            new_min=target,
            save_original=save_orig,
            new_boosted_until=now + boost_ttl,
            new_failures=0,
            new_release_time=s.last_release_time,
            reason=f"raising minReplicaCount from {s.current_min_replicas} to {target} (desired {desired}, max {ceiling})",
        )

    if target == s.current_min_replicas:
        if is_boosted:
            return Plan(
                action="hold",
                new_min=target,
                new_boosted_until=now + boost_ttl,
                new_failures=0,
                new_release_time=s.last_release_time,
                reason=f"holding minReplicaCount at {target} and renewing lease",
            )
        return Plan(
            action="noop",
            new_min=target,
            new_failures=0,
            new_release_time=s.last_release_time,
            reason=f"at baseline {target}",
        )

    # Stepping down while still boosted
    return Plan(
        action="hold",
        new_min=target,
        new_boosted_until=now + boost_ttl,
        new_failures=0,
        new_release_time=s.last_release_time,
        reason=f"adjusting minReplicaCount from {s.current_min_replicas} to {target} and renewing lease",
    )

def apply_plan(state: State, plan: Plan):
    state.current_min_replicas = plan.new_min
    state.consecutive_failures = plan.new_failures
    state.last_release_time = plan.new_release_time
    if plan.save_original > 0:
        state.original_min = plan.save_original
    if plan.new_boosted_until is not None:
        state.boosted_until = plan.new_boosted_until
    if plan.clear_annotations:
        state.original_min = 0
        state.boosted_until = None

def render_ascii_timeline(history: List[dict]):
    """Renders a visual timeline chart with replicas and load levels"""
    print(f"\n{BOLD}Replica Count & Traffic Timeline:{RESET}")
    print(f"{'Time':<7} | {'RPS':<6} | {'Prescaled Min':<14} | {'Visual Replica Level (1-10)':<30} | {'Action':<10}")
    print("-" * 75)

    for step in history:
        t = f"+{step['rel_t']}s"
        rps = int(step['rps'])
        min_rep = step['min_rep']
        action = step['action']

        color = GREEN if action == "raise" else (CYAN if action == "hold" else (YELLOW if action == "release" else RESET))
        if action == "error":
            color = RED

        bar_len = min_rep * 2
        bar = "█" * bar_len + "░" * (20 - bar_len)
        print(f"{t:<7} | {rps:<6} | {min_rep:<14} | {color}[{bar}] ({min_rep}){RESET} | {color}{action.upper()}{RESET}")

# ─────────────────────────────────────────────────────────────────────────────
# Scenarios
# ─────────────────────────────────────────────────────────────────────────────

def run_scenario_1_anticipated_surge():
    print(f"\n{BOLD}{CYAN}=== Scenario 1: Anticipated Surge (Predictive Pre-Scaling vs Reactive HPA) ==={RESET}")
    print(f"Overview: Traffic surge (60 -> 490 RPS) is predicted 60 seconds in advance.")
    print(f"Goal: Prove Prescale raises minReplicaCount from 3 to 7 ahead of the spike, avoiding latency breach.\n")

    cfg = Config(baseline_min=3, max_limit=10, boost_ttl_sec=60, max_step=3, cooldown_sec=30)
    state = State(current_min_replicas=3, max_replica_count=10)
    now = 0.0
    history = []

    # Timeline (every 15s tick from t=0 to t=120s)
    # Predicted RPS leads actual traffic by 60s
    ticks = [
        # (rel_t, actual_rps, predicted_rps_horizon_60s, error)
        (0,   60,  60,  None),
        (15,  60,  490, None), # Prediction sees surge arriving at t=75s!
        (30,  60,  490, None), # Desired replicas = 7 (490/80 = 6.125 -> 7)
        (45,  60,  490, None),
        (60,  60,  490, None), # Pods already ready (7 replicas)!
        (75,  490, 490, None), # Traffic arrives at pre-warmed pods!
        (90,  490, 490, None), # Peak traffic sustained
        (105, 490, 60,  None), # Prediction sees drop arriving at t=165s
        (120, 60,  60,  None), # Traffic drops; prescaler releases lease
    ]

    for rel_t, rps, pred_rps, err in ticks:
        desired = int(pred_rps / 80) + 1 if pred_rps > 60 else 3
        plan = plan_decision(state, cfg, rel_t, desired, err)
        apply_plan(state, plan)
        history.append({
            "rel_t": rel_t,
            "rps": rps,
            "pred_rps": pred_rps,
            "min_rep": state.current_min_replicas,
            "action": plan.action,
            "reason": plan.reason
        })

    render_ascii_timeline(history)

    # Verification
    pre_warmed = history[4]["min_rep"] >= 6 # at t=60, before surge hits at t=75
    released = history[-1]["min_rep"] == 3
    print(f"\n{BOLD}Verification Scorecard:{RESET}")
    print(f"  [{GREEN if pre_warmed else RED}✓{RESET}] Pre-warmed to 7 replicas before traffic hit: {BOLD}{pre_warmed}{RESET}")
    print(f"  [{GREEN if released else RED}✓{RESET}] Cleanly released back to baseline min=3 after surge: {BOLD}{released}{RESET}")
    return pre_warmed and released

def run_scenario_2_predictor_outage():
    print(f"\n{BOLD}{CYAN}=== Scenario 2: Predictor Outage & Self-Healing Lease Expiry ==={RESET}")
    print(f"Overview: Predictor goes down mid-boost (HTTP 500 / Network partition).")
    print(f"Goal: Prove Prescale holds lease across transient failures, then safely releases back to baseline.\n")

    cfg = Config(baseline_min=3, max_limit=10, boost_ttl_sec=60, max_failures=3, cooldown_sec=30)
    state = State(current_min_replicas=3, max_replica_count=10)
    history = []

    ticks = [
        (0,  60,  60,  None),
        (15, 60,  490, None), # Boosted to 6 (maxStep=3)
        (30, 490, 490, None), # Boosted to 7
        (45, 490, 0,   "connection refused (predictor down)"), # Fail 1/3 (HOLD)
        (60, 490, 0,   "connection timeout (predictor down)"), # Fail 2/3 (HOLD)
        (75, 490, 0,   "http 500 internal server error"),      # Fail 3/3 -> RELEASE!
        (90, 490, 0,   "predictor down"),                      # Error (at baseline)
        (105, 60, 60,  None),                                  # Predictor recovers
    ]

    for rel_t, rps, pred_rps, err in ticks:
        desired = int(pred_rps / 80) + 1 if pred_rps > 60 else 3
        plan = plan_decision(state, cfg, rel_t, desired, err)
        apply_plan(state, plan)
        history.append({
            "rel_t": rel_t,
            "rps": rps,
            "pred_rps": pred_rps,
            "min_rep": state.current_min_replicas,
            "action": plan.action,
            "reason": plan.reason
        })

    render_ascii_timeline(history)

    held_during_glitch = history[3]["action"] == "hold" and history[3]["min_rep"] == 7
    auto_released = history[5]["action"] == "release" and history[5]["min_rep"] == 3
    print(f"\n{BOLD}Verification Scorecard:{RESET}")
    print(f"  [{GREEN if held_during_glitch else RED}✓{RESET}] Held active lease during transient failures (ticks 1 & 2): {BOLD}{held_during_glitch}{RESET}")
    print(f"  [{GREEN if auto_released else RED}✓{RESET}] Released to baseline min=3 after 3 consecutive failures: {BOLD}{auto_released}{RESET}")
    return held_during_glitch and auto_released

def run_scenario_3_safety_bounds():
    print(f"\n{BOLD}{CYAN}=== Scenario 3: Safety Clamping (MaxStep & Ceiling Protection) ==={RESET}")
    print(f"Overview: Massive flash crowd demands 20 replicas. Cluster ceiling is 10, maxStep is 3.")
    print(f"Goal: Prove Prescale clamps to ceiling (10) and rates step-ups (3 pods/tick), preventing cluster thrashing.\n")

    cfg = Config(baseline_min=3, max_limit=10, max_step=3)
    state = State(current_min_replicas=3, max_replica_count=10)
    history = []

    ticks = [
        (0,  60,   60,   None),
        (15, 1200, 1600, None), # Desired = 20 replicas (Ceiling = 10, MaxStep = 3) -> 3+3=6
        (30, 1200, 1600, None), # 6 + 3 = 9
        (45, 1200, 1600, None), # 9 + 1 = 10 (Clamped to ceiling!)
        (60, 1200, 1600, None), # At ceiling 10 (Hold)
    ]

    for rel_t, rps, pred_rps, err in ticks:
        desired = int(pred_rps / 80) + 1 if pred_rps > 60 else 3
        plan = plan_decision(state, cfg, rel_t, desired, err)
        apply_plan(state, plan)
        history.append({
            "rel_t": rel_t,
            "rps": rps,
            "pred_rps": pred_rps,
            "min_rep": state.current_min_replicas,
            "action": plan.action,
            "reason": plan.reason
        })

    render_ascii_timeline(history)

    step_1_ok = history[1]["min_rep"] == 6
    step_2_ok = history[2]["min_rep"] == 9
    ceiling_ok = history[3]["min_rep"] == 10 and history[4]["min_rep"] == 10
    print(f"\n{BOLD}Verification Scorecard:{RESET}")
    print(f"  [{GREEN if step_1_ok else RED}✓{RESET}] Tick 1 respected maxStep=3 (raised 3 -> 6): {BOLD}{step_1_ok}{RESET}")
    print(f"  [{GREEN if step_2_ok else RED}✓{RESET}] Tick 2 respected maxStep=3 (raised 6 -> 9): {BOLD}{step_2_ok}{RESET}")
    print(f"  [{GREEN if ceiling_ok else RED}✓{RESET}] Capped at hard safety ceiling (10 replicas): {BOLD}{ceiling_ok}{RESET}")
    return step_1_ok and step_2_ok and ceiling_ok

def run_scenario_4_cooldown():
    print(f"\n{BOLD}{CYAN}=== Scenario 4: Demand Drop & Flapping Prevention (Cooldown Window) ==={RESET}")
    print(f"Overview: Traffic drops back to normal, but erratic demand attempts immediate re-boost.")
    print(f"Goal: Prove Prescale enforces 30s cooldown after release to avoid replica flapping.\n")

    cfg = Config(baseline_min=3, max_limit=10, cooldown_sec=30)
    state = State(current_min_replicas=7, original_min=3, boosted_until=60.0)
    history = []

    ticks = [
        (0,  60,  60,  None), # Desired dropped -> Release! Cooldown begins
        (10, 60,  400, None), # Erratic spike arrives during cooldown (t=10s < 30s cooldown) -> NOOP!
        (25, 60,  400, None), # Still in cooldown (t=25s < 30s) -> NOOP!
        (35, 400, 400, None), # Cooldown expired (t=35s >= 30s) -> Boost permitted!
    ]

    for rel_t, rps, pred_rps, err in ticks:
        desired = int(pred_rps / 80) + 1 if pred_rps > 60 else 3
        plan = plan_decision(state, cfg, rel_t, desired, err)
        apply_plan(state, plan)
        history.append({
            "rel_t": rel_t,
            "rps": rps,
            "pred_rps": pred_rps,
            "min_rep": state.current_min_replicas,
            "action": plan.action,
            "reason": plan.reason
        })

    render_ascii_timeline(history)

    released = history[0]["action"] == "release"
    cooldown_blocked = history[1]["action"] == "noop" and "in cooldown" in history[1]["reason"]
    reboost_allowed = history[3]["action"] == "raise"
    print(f"\n{BOLD}Verification Scorecard:{RESET}")
    print(f"  [{GREEN if released else RED}✓{RESET}] Clean release on demand drop: {BOLD}{released}{RESET}")
    print(f"  [{GREEN if cooldown_blocked else RED}✓{RESET}] Blocked flapping during 30s cooldown: {BOLD}{cooldown_blocked}{RESET}")
    print(f"  [{GREEN if reboost_allowed else RED}✓{RESET}] Allowed scaling after cooldown expired: {BOLD}{reboost_allowed}{RESET}")
    return released and cooldown_blocked and reboost_allowed

def run_scenario_5_crash_recovery():
    print(f"\n{BOLD}{CYAN}=== Scenario 5: Daemon Crash & State Reconstruction (Chaos Recovery) ==={RESET}")
    print(f"Overview: Prescaler process is killed mid-boost. New pod starts with no in-memory state.")
    print(f"Goal: Prove Prescale reconstructs originalMin and lease expiry from ScaledObject annotations.\n")

    cfg = Config(baseline_min=3, max_limit=10, boost_ttl_sec=60)
    # State before crash
    print(f"  Phase 1: Daemon boosts ScaledObject to min=7 (saving annotation original-min=3, boosted-until=+60s)")
    now = 10.0
    k8s_annotations = {
        "prescale.io/original-min-replicas": "3",
        "prescale.io/boosted-until": str(now + 60.0)
    }
    k8s_spec_min = 7

    print(f"  Phase 2: Daemon crashes! (Process killed, memory cleared)")
    print(f"  Phase 3: New daemon starts. Reads annotations from Kubernetes ScaledObject...")

    # Reconstructed state from K8s annotations
    reconstructed_state = State(
        current_min_replicas=k8s_spec_min,
        max_replica_count=10,
        original_min=int(k8s_annotations["prescale.io/original-min-replicas"]),
        boosted_until=float(k8s_annotations["prescale.io/boosted-until"]),
    )

    now_restart = 25.0
    desired = 7 # Demand continues
    plan = plan_decision(reconstructed_state, cfg, now_restart, desired, None)
    apply_plan(reconstructed_state, plan)

    history = [
        {"rel_t": 10, "rps": 490, "pred_rps": 490, "min_rep": 7, "action": "raise", "reason": "boosted before crash"},
        {"rel_t": 25, "rps": 490, "pred_rps": 490, "min_rep": reconstructed_state.current_min_replicas, "action": plan.action, "reason": plan.reason}
    ]

    render_ascii_timeline(history)

    preserved = reconstructed_state.original_min == 3 and reconstructed_state.current_min_replicas == 7
    lease_held = plan.action == "hold"
    print(f"\n{BOLD}Verification Scorecard:{RESET}")
    print(f"  [{GREEN if preserved else RED}✓{RESET}] Reconstructed original baseline (3) from annotations: {BOLD}{preserved}{RESET}")
    print(f"  [{GREEN if lease_held else RED}✓{RESET}] Maintained active boost lease without flapping or double-scaling: {BOLD}{lease_held}{RESET}")
    return preserved and lease_held

def main():
    if len(sys.argv) > 1 and sys.argv[1] in ("-h", "--help"):
        print("Usage: python3 scripts/demo/simulator.py [1|2|3|4|5|all]")
        sys.exit(0)

    target = sys.argv[1] if len(sys.argv) > 1 else "all"

    print(f"\n{BOLD}{MAGENTA}╔═════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{MAGENTA}║               PRESCALE HIGH-SPEED SCENARIO SIMULATOR                ║{RESET}")
    print(f"{BOLD}{MAGENTA}║     Evaluating Pure Planning Logic Across Operational Scenarios     ║{RESET}")
    print(f"{BOLD}{MAGENTA}╚═════════════════════════════════════════════════════════════════════╝{RESET}")

    scenarios = {
        "1": ("Anticipated Surge (A/B Test)", run_scenario_1_anticipated_surge),
        "2": ("Predictor Outage & Self-Healing", run_scenario_2_predictor_outage),
        "3": ("Safety Bounds & Rate Clamping", run_scenario_3_safety_bounds),
        "4": ("Demand Drop & Cooldown", run_scenario_4_cooldown),
        "5": ("Daemon Crash & State Reconstruction", run_scenario_5_crash_recovery),
    }

    results = []
    if target in scenarios:
        name, fn = scenarios[target]
        passed = fn()
        results.append((target, name, passed))
    else:
        for k, (name, fn) in scenarios.items():
            passed = fn()
            results.append((k, name, passed))

    print(f"\n{BOLD}{'='*75}{RESET}")
    print(f"{BOLD}{'Scenario Suite Results Summary':^75}{RESET}")
    print(f"{'='*75}")
    all_ok = True
    for k, name, passed in results:
        status = f"{GREEN}PASS{RESET}" if passed else f"{RED}FAIL{RESET}"
        if not passed:
            all_ok = False
        print(f"  Scenario {k}: {name:<45} [{status}]")
    print(f"{'='*75}")

    if all_ok:
        print(f"{BOLD}{GREEN}ALL SCENARIOS VERIFIED SUCCESSFULLY!{RESET}\n")
        sys.exit(0)
    else:
        print(f"{BOLD}{RED}ONE OR MORE SCENARIOS FAILED!{RESET}\n")
        sys.exit(1)

if __name__ == "__main__":
    main()
