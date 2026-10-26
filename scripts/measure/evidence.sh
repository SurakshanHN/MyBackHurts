#!/usr/bin/env bash
# evidence.sh – helper sourced by all part-4d measurement scripts.
# Defines run() which logs the command, runs it, and logs the exit code.
# Usage:
#   source scripts/measure/evidence.sh
#   OUT=docs/reports/data/part4d/foo.txt
#   run go version
#   run grep -E 'Foo|Bar' /some/file
#
# All output (stdout+stderr) goes to $OUT.  run() also prints to the terminal.
# NOTE: use run() with plain arguments – no eval, quoting is via "$@".

run() {
    # Print the command as "$ arg0 arg1 ..."
    printf '$ %s\n' "$*" | tee -a "$OUT"
    # Execute with stdout+stderr to OUT (tee so terminal also sees it)
    "$@" 2>&1 | tee -a "$OUT"
    local rc=${PIPESTATUS[0]}
    printf '[exit %d]\n\n' "$rc" | tee -a "$OUT"
    return $rc
}
