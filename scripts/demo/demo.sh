#!/usr/bin/env bash
# scripts/demo/demo.sh
# Interactive Prescale Demo & Scenario Simulator Runner
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

KC="kubectl --context kind-prescale-demo"
PREDICTOR_NAME="prescale-predictor"

# Color constants
BOLD="\033[1m"
GREEN="\033[32m"
CYAN="\033[36m"
YELLOW="\033[33m"
RED="\033[31m"
MAGENTA="\033[35m"
BLUE="\033[34m"
RESET="\033[0m"

print_banner() {
    clear 2>/dev/null || true
    echo -e "${MAGENTA}${BOLD}"
    echo "  ╔═══════════════════════════════════════════════════════════════════════╗"
    echo "  ║                                                                       ║"
    echo "  ║      ██████╗ ██████╗ ███████╗███████╗ ██████╗ █████╗ ██╗     ███████╗ ║"
    echo "  ║      ██╔══██╗██╔══██╗██╔════╝██╔════╝██╔════╝██╔══██╗██║     ██╔════╝ ║"
    echo "  ║      ██████╔╝██████╔╝█████╗  ███████╗██║     ███████║██║     █████╗   ║"
    echo "  ║      ██╔═══╝ ██╔══██╗██╔══╝  ╚════██║██║     ██╔══██║██║     ██╔══╝   ║"
    echo "  ║      ██║     ██║  ██║███████╗███████║╚██████╗██║  ██║███████╗███████╗ ║"
    echo "  ║      ╚═╝     ╚═╝  ╚═╝╚══════╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚══════╝ ║"
    echo "  ║                                                                       ║"
    echo "  ║     Predictive Kubernetes Auto-Scaler Demo & Scenario Simulator       ║"
    echo "  ╚═══════════════════════════════════════════════════════════════════════╝"
    echo -e "${RESET}"
}

get_status_line() {
    # Check kind cluster
    local cluster_ok="DOWN"
    if $KC cluster-info &>/dev/null; then
        cluster_ok="${GREEN}OK${RESET}"
    else
        cluster_ok="${RED}UNREACHABLE${RESET}"
    fi

    # Check predictor container
    local pred_status="STOPPED"
    if docker inspect -f '{{.State.Running}}' "$PREDICTOR_NAME" 2>/dev/null | grep -q true; then
        pred_status="${GREEN}RUNNING (172.19.0.100:8000)${RESET}"
    else
        pred_status="${YELLOW}STOPPED${RESET}"
    fi

    # Check prescaler pod
    local prescaler_status="0 REPLICAS"
    local p_ready
    p_ready=$($KC get deploy prescaler -n predictive -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    if [ "$p_ready" = "1" ]; then
        prescaler_status="${GREEN}RUNNING (1/1)${RESET}"
    else
        prescaler_status="${YELLOW}SCALED TO 0${RESET}"
    fi

    # Check ScaledObjects
    local b_min p_min
    b_min=$($KC get scaledobject checkout -n baseline -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo "?")
    p_min=$($KC get scaledobject checkout -n predictive -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null || echo "?")

    echo -e "${BOLD}Current System Health & Cluster Status:${RESET}"
    echo -e "  • Cluster:           ${cluster_ok}"
    echo -e "  • Stand-in Predictor:${pred_status}"
    echo -e "  • Prescaler Daemon:  ${prescaler_status}"
    echo -e "  • Baseline SO Min:   ${CYAN}${b_min}${RESET} pods (HPA reactive only)"
    echo -e "  • Predictive SO Min: ${GREEN}${p_min}${RESET} pods (HPA + Prescaler boost)"
    echo "-----------------------------------------------------------------------"
}

show_cluster_health() {
    print_banner
    echo -e "${BOLD}${CYAN}Detailed Cluster Health & Recent Daemon Logs:${RESET}\n"
    
    echo -e "${BOLD}1. Deployments & Pods:${RESET}"
    $KC get deploy,pods -n predictive 2>/dev/null || true
    echo ""
    $KC get deploy,pods -n baseline 2>/dev/null || true
    echo ""

    echo -e "${BOLD}2. ScaledObjects:${RESET}"
    $KC get scaledobjects.keda.sh -A 2>/dev/null || true
    echo ""

    echo -e "${BOLD}3. Last 10 Prescaler Daemon Logs:${RESET}"
    $KC logs -n predictive deploy/prescaler --tail=10 2>/dev/null || echo "No logs (prescaler scaled to 0)"
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_fast_simulator() {
    print_banner
    python3 scripts/demo/simulator.py all
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_live_scenario_1() {
    print_banner
    bash scripts/demo/scenarios/s1_surge_ab.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_live_scenario_2() {
    print_banner
    bash scripts/demo/scenarios/s2_predictor_outage.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_live_scenario_3() {
    print_banner
    bash scripts/demo/scenarios/s3_ttl_expiry.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_live_scenario_4() {
    print_banner
    bash scripts/demo/scenarios/s4_crash_recovery.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

run_all_live_failures() {
    print_banner
    echo -e "${BOLD}${CYAN}Running Complete Failure & Safety Verification Suite...${RESET}\n"
    bash scripts/failure_test.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

start_stack() {
    print_banner
    echo -e "${BOLD}${GREEN}Starting Demo Stack (Predictor + Prescaler)...${RESET}"
    bash scripts/demo/up.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

teardown_stack() {
    print_banner
    echo -e "${BOLD}${YELLOW}Tearing Down Demo Stack & Resetting ScaledObjects...${RESET}"
    bash scripts/demo/down.sh
    echo ""
    read -rp "Press Enter to return to main menu..."
}

# CLI Non-interactive flags
if [ "${1:-}" = "--simulate" ] || [ "${1:-}" = "-s" ]; then
    python3 scripts/demo/simulator.py "${2:-all}"
    exit $?
elif [ "${1:-}" = "--live" ]; then
    case "${2:-1}" in
        1) bash scripts/demo/scenarios/s1_surge_ab.sh ;;
        2) bash scripts/demo/scenarios/s2_predictor_outage.sh ;;
        3) bash scripts/demo/scenarios/s3_ttl_expiry.sh ;;
        4) bash scripts/demo/scenarios/s4_crash_recovery.sh ;;
        all) bash scripts/failure_test.sh ;;
        *) echo "Unknown live scenario: $2"; exit 1 ;;
    esac
    exit 0
elif [ "${1:-}" = "--up" ]; then
    bash scripts/demo/up.sh
    exit 0
elif [ "${1:-}" = "--down" ]; then
    bash scripts/demo/down.sh
    exit 0
elif [ "${1:-}" = "--status" ]; then
    get_status_line
    exit 0
fi

# Interactive Loop
while true; do
    print_banner
    get_status_line
    echo ""
    echo -e "${BOLD}Select a Demo Scenario or Action:${RESET}"
    echo -e "  ${BOLD}${GREEN}[1]${RESET} Fast Scenario Simulator ${CYAN}(5 scenarios in <2s with ASCII timelines)${RESET}"
    echo -e "  ${BOLD}${GREEN}[2]${RESET} Live Scenario 1: A/B Traffic Surge ${CYAN}(KEDA reactive vs Predictive)${RESET}"
    echo -e "  ${BOLD}${GREEN}[3]${RESET} Live Scenario 2: Predictor Outage & Fallback ${CYAN}(Self-healing lease release)${RESET}"
    echo -e "  ${BOLD}${GREEN}[4]${RESET} Live Scenario 3: Lease TTL Expiry ${CYAN}(Automatic stale boost cleanup)${RESET}"
    echo -e "  ${BOLD}${GREEN}[5]${RESET} Live Scenario 4: Daemon Crash Recovery ${CYAN}(State reconstruction from K8s)${RESET}"
    echo -e "  ${BOLD}${GREEN}[6]${RESET} Run All Live Failure & Safety Scenarios ${CYAN}(Full automated test suite)${RESET}"
    echo -e "  ${BOLD}${BLUE}[7]${RESET} Detailed Cluster Health & Recent Logs"
    echo -e "  ${BOLD}${BLUE}[8]${RESET} Start Demo Stack (Predictor + Prescaler up)"
    echo -e "  ${BOLD}${YELLOW}[9]${RESET} Teardown & Reset Cluster State"
    echo -e "  ${BOLD}${RED}[0]${RESET} Exit"
    echo ""
    read -rp "Enter choice [0-9]: " choice

    case "$choice" in
        1) run_fast_simulator ;;
        2) run_live_scenario_1 ;;
        3) run_live_scenario_2 ;;
        4) run_live_scenario_3 ;;
        5) run_live_scenario_4 ;;
        6) run_all_live_failures ;;
        7) show_cluster_health ;;
        8) start_stack ;;
        9) teardown_stack ;;
        0|q|exit) echo -e "\nExiting demo. Bye!"; exit 0 ;;
        *) echo -e "${RED}Invalid option.${RESET}"; sleep 1 ;;
    esac
done
