#!/bin/sh
set -eu

# Findings are reported on stdout with exit status zero. Analyzer failures must
# also fail the gate, even when there are no findings (or the tool is missing).
# Include integration tests, which are the callers of DB-only helpers.
if out=$(deadcode -test -tags=integration ./...); then
  if [ -n "$out" ]; then
    printf '%s\n' 'Dead code found:' "$out"
    exit 1
  fi
  printf '%s\n' 'No dead code found.'
else
  status=$?
  printf 'Deadcode analysis failed (exit %s).\n' "$status" >&2
  exit "$status"
fi
