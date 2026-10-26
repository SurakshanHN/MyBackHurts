#!/usr/bin/env python3
"""
scripts/load/test_part9b.py – Part 9B real failure-release tests (G1-G5).
Does NOT hand-inject minReplicaCount or annotations.
Uses stand-in predictor schedule to trigger natural prescaler boost,
then executes failure scenario and tracks release timing and log reasons.
"""

import json
import os
import subprocess
import time
import urllib.error
import urllib.request
from typing import Any, Dict, Optional, Tuple

KC = "kubectl --context kind-prescale-demo"
NS = "predictive"
SO = "checkout"
PREDICTOR_URL = "http://172.19.0.100:8000"
DATA_DIR = "/home/unhealthycoder/MyBackHurts/docs/reports/data/part9b"

def run_cmd(cmd: str) -> str:
    res = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    return res.stdout.strip()

def get_so_state() -> Tuple[int, Optional[str], Optional[str]]:
    cmd = f"{KC} get scaledobject {SO} -n {NS} -o json"
    out = run_cmd(cmd)
    try:
        data = json.loads(out)
        min_reps = data.get("spec", {}).get("minReplicaCount", 0)
        ann = data.get("metadata", {}).get("annotations") or {}
        orig = ann.get("prescale.io/original-min-replicas")
        boost_until = ann.get("prescale.io/boosted-until")
        return min_reps, orig, boost_until
    except Exception as e:
        print(f"Error parsing SO: {e}")
        return 0, None, None

def set_predictor_fail_mode(mode: str):
    req = urllib.request.Request(
        f"{PREDICTOR_URL}/fail",
        data=json.dumps({"mode": mode}).encode(),
        headers={"Content-Type": "application/json"}
    )
    urllib.request.urlopen(req, timeout=5)

def set_predictor_schedule(surge_start_epoch: float):
    req = urllib.request.Request(
        f"{PREDICTOR_URL}/schedule",
        data=json.dumps({"surge_start_epoch": surge_start_epoch}).encode(),
        headers={"Content-Type": "application/json"}
    )
    urllib.request.urlopen(req, timeout=5)

def ensure_predictor_running():
    out = run_cmd("docker ps -q -f name=prescale-predictor")
    if not out:
        exists = run_cmd("docker ps -aq -f name=prescale-predictor")
        if exists:
            run_cmd("docker start prescale-predictor")
        else:
            run_cmd("docker run -d --name prescale-predictor --network kind --ip 172.19.0.100 prescale-predictor:0.1.1")
        time.sleep(3)
    try:
        set_predictor_fail_mode("")
    except Exception:
        pass

def cleanup_between_cases():
    print("--- Cleanup between cases ---")
    run_cmd(f"{KC} scale deploy/prescaler -n {NS} --replicas=0")
    ensure_predictor_running()
    set_predictor_schedule(time.time() + 86400)
    set_predictor_fail_mode("")
    min_reps, orig, until = get_so_state()
    if min_reps != 3 or orig is not None or until is not None:
        print(f"Patching SO cleanup: min={min_reps}, orig={orig}")
        patch = '{"spec":{"minReplicaCount":3},"metadata":{"annotations":{"prescale.io/original-min-replicas":null,"prescale.io/boosted-until":null}}}'
        run_cmd(f"{KC} patch scaledobject {SO} -n {NS} --type=merge -p '{patch}'")
    time.sleep(5)

def run_case(case_name: str, trigger_fn) -> Dict[str, Any]:
    print(f"\n==================== Starting {case_name} ====================")
    cleanup_between_cases()

    # Pre-check
    min_reps, orig, until = get_so_state()
    print(f"Pre-state: min={min_reps}, orig={orig}, until={until}")

    # Set schedule so predictor wants boost (surge_start_epoch = now + 60)
    now = time.time()
    set_predictor_schedule(now + 60)
    print(f"Schedule set for now+60 ({now+60:.1f})")

    # Scale prescaler to 1
    print("Scaling prescaler to 1...")
    run_cmd(f"{KC} scale deploy/prescaler -n {NS} --replicas=1")

    # Wait until PRESCALER itself has boosted min (>3) with annotations present
    print("Waiting for prescaler to boost...")
    boost_epoch = None
    start_wait = time.time()
    while time.time() - start_wait < 120:
        min_reps, orig, until = get_so_state()
        if min_reps > 3 and orig is not None and until is not None:
            boost_epoch = time.time()
            print(f"BOOST DETECTED at epoch {boost_epoch:.2f}: min={min_reps}, orig={orig}, until={until}")
            break
        time.sleep(2)

    if boost_epoch is None:
        raise RuntimeError(f"{case_name}: Prescaler failed to boost within 120s!")

    # Keep schedule so predictor still wants a boost. Trigger failure!
    print(f"Triggering failure for {case_name}...")
    failure_epoch = time.time()
    trigger_fn()
    print(f"Failure triggered at epoch {failure_epoch:.2f}")

    # Poll minReplicaCount every 2 s (max 5 min = 300 s)
    print("Polling minReplicaCount every 2s for release...")
    release_epoch = None
    not_released = False
    poll_start = time.time()
    while time.time() - poll_start < 300:
        time.sleep(2)
        min_reps, orig, until = get_so_state()
        now_ts = time.time()
        print(f"  [+{now_ts-failure_epoch:.1f}s] min={min_reps}, orig={orig}, until={until}")
        if min_reps == 3 and orig is None and until is None:
            release_epoch = now_ts
            print(f"RELEASE DETECTED at epoch {release_epoch:.2f}")
            break

    if release_epoch is None:
        not_released = True
        print(f"WARNING: NOT RELEASED in 300s for {case_name}!")
        # Patch cleanup
        patch = '{"spec":{"minReplicaCount":3},"metadata":{"annotations":{"prescale.io/original-min-replicas":null,"prescale.io/boosted-until":null}}}'
        run_cmd(f"{KC} patch scaledobject {SO} -n {NS} --type=merge -p '{patch}'")
        offset = None
    else:
        offset = round(release_epoch - failure_epoch, 2)

    # Collect prescaler logs
    time.sleep(2)
    logs = run_cmd(f"{KC} logs deploy/prescaler -n {NS} --tail=100")
    
    # Save case logs
    os.makedirs(DATA_DIR, exist_ok=True)
    with open(f"{DATA_DIR}/{case_name}.log", "w") as f:
        f.write(logs)

    # Extract release reason and quoted log line
    quoted_log = ""
    release_reason = "UNEXPLAINED"
    for line in logs.splitlines():
        if 'action=release' in line or 'action="release"' in line:
            quoted_log = line
            # Extract reason
            if 'reason="' in line:
                release_reason = line.split('reason="')[1].split('"')[0]
            elif 'reason=' in line:
                release_reason = line.split('reason=')[1].split()[0]

    # Check annotations after
    final_min, final_orig, final_until = get_so_state()
    annotations_cleaned = (final_orig is None and final_until is None)

    res = {
        "case": case_name,
        "boost_epoch": boost_epoch,
        "failure_epoch": failure_epoch,
        "release_epoch": release_epoch,
        "release_offset_s": offset,
        "not_released": not_released,
        "release_reason": release_reason,
        "quoted_log": quoted_log,
        "final_min": final_min,
        "annotations_cleaned": annotations_cleaned,
    }
    print(f"Result for {case_name}: {json.dumps(res, indent=2)}")
    return res

def main():
    os.makedirs(DATA_DIR, exist_ok=True)
    results = []

    # G1: error
    def trigger_g1():
        set_predictor_fail_mode("error")
    r1 = run_case("G1_error", trigger_g1)
    results.append(r1)

    # G2: slow
    def trigger_g2():
        set_predictor_fail_mode("slow")
    r2 = run_case("G2_slow", trigger_g2)
    results.append(r2)

    # G3: garbage
    def trigger_g3():
        set_predictor_fail_mode("garbage")
    r3 = run_case("G3_garbage", trigger_g3)
    results.append(r3)

    # G4: predictor container stopped
    def trigger_g4():
        run_cmd("docker stop prescale-predictor")
    r4 = run_case("G4_stopped", trigger_g4)
    results.append(r4)

    # G5: prescaler pod deleted while boosted (predictor healthy, schedule moved far future)
    def trigger_g5():
        set_predictor_schedule(time.time() + 86400)
        run_cmd(f"{KC} delete pod -n {NS} -l app=prescaler")
    r5 = run_case("G5_restarted", trigger_g5)
    results.append(r5)

    cleanup_between_cases()

    # Save summary
    with open(f"{DATA_DIR}/summary.json", "w") as f:
        json.dump(results, f, indent=2)
    print("\nAll cases completed. Summary written to:", f"{DATA_DIR}/summary.json")

if __name__ == "__main__":
    main()
