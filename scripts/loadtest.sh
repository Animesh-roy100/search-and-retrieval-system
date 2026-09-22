#!/usr/bin/env bash
# Drive a production-like load test. loadgen runs inside a throwaway Go container on
# the compose network and writes to Postgres; the pipeline (CDC -> normalize -> index)
# reacts exactly as in production. Watch progress with scripts/monitor.sh in parallel.
#
# Usage:
#   scripts/loadtest.sh backfill [COUNT] [RATE]
#   scripts/loadtest.sh stream   [RATE] [DURATION] [URGENT_PCT] [UPDATE_PCT]
#
# Examples:
#   scripts/loadtest.sh backfill 1000000 5000      # 1M-doc onboarding at 5k/s
#   scripts/loadtest.sh stream   1000 5m 20 30     # 1k writes/s for 5 min, 20% urgent, 30% updates
set -euo pipefail

MODE="${1:-stream}"
GO_IMAGE="golang:1.26-alpine"
# loadgen talks to postgres over the compose network on the internal port 5432.
DSN="postgresql://retrieval:retrieval@postgres:5432/retrieval"

run_loadgen() {
  docker run --rm --network retrieval_default -v "$PWD":/src -w /src \
    -e GOFLAGS=-mod=mod -e GOTOOLCHAIN=local \
    "$GO_IMAGE" go run ./cmd/loadgen "$@" -dsn "$DSN"
}

case "$MODE" in
  backfill)
    COUNT="${2:-1000000}"; RATE="${3:-5000}"
    echo "== BACKFILL: $COUNT docs @ ${RATE}/s =="
    run_loadgen -mode backfill -count "$COUNT" -rate "$RATE" -batch 200 -workers 6 -urgent-pct 20
    ;;
  stream)
    RATE="${2:-1000}"; DUR="${3:-2m}"; URG="${4:-20}"; UPD="${5:-30}"
    echo "== STREAM: ${RATE} writes/s for $DUR (urgent ${URG}%, update ${UPD}%) =="
    run_loadgen -mode stream -rate "$RATE" -duration "$DUR" -batch 50 -workers 6 \
      -urgent-pct "$URG" -update-pct "$UPD"
    ;;
  *)
    echo "usage: $0 {backfill|stream} ..." >&2; exit 1;;
esac
