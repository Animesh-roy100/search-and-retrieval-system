#!/usr/bin/env bash
# Sample the system's load signals at a fixed interval. Run this in one terminal
# while scripts/loadtest.sh (or loadgen directly) runs in another.
#
#   bash scripts/monitor.sh [interval_seconds] [samples]
set -euo pipefail

INTERVAL="${1:-5}"
SAMPLES="${2:-0}"   # 0 = run forever
PROM="${PROM:-http://localhost:9090}"

q() {  # promql instant query -> scalar (first value) or "-"
  curl -sf "$PROM/api/v1/query" --data-urlencode "query=$1" 2>/dev/null \
    | python3 -c "import sys,json;r=json.load(sys.stdin)['data']['result'];print(f'{float(r[0][\"value\"][1]):.3f}' if r else '-')" 2>/dev/null || echo "-"
}

lag() {  # total consumer-group lag for a group via rpk
  docker compose exec -T redpanda rpk group describe "$1" 2>/dev/null \
    | awk '/TOTAL-LAG/{print $2}' || echo "-"
}

count_os() { curl -sf localhost:9200/documents/_count 2>/dev/null | python3 -c "import sys,json;print(json.load(sys.stdin).get('count','-'))" 2>/dev/null || echo "-"; }

printf "%-8s %10s %10s %10s %12s %12s %10s\n" "time" "OS_docs" "fast_lag" "bulk_lag" "fresh_p99" "fresh_p50" "idx_rate"
printf "%-8s %10s %10s %10s %12s %12s %10s\n" "----" "-------" "--------" "--------" "---------" "---------" "--------"

i=0
while :; do
  ts=$(date +%H:%M:%S)
  osd=$(count_os)
  fl=$(lag indexer-fast)
  bl=$(lag indexer-bulk)
  p99=$(q 'histogram_quantile(0.99, sum(rate(doc_freshness_lag_seconds_bucket{tier="urgent"}[1m])) by (le))')
  p50=$(q 'histogram_quantile(0.50, sum(rate(doc_freshness_lag_seconds_bucket{tier="urgent"}[1m])) by (le))')
  rate=$(q 'sum(rate(indexer_docs_indexed_total[1m]))')
  printf "%-8s %10s %10s %10s %12s %12s %10s\n" "$ts" "$osd" "$fl" "$bl" "$p99" "$p50" "$rate"
  i=$((i+1))
  [ "$SAMPLES" -gt 0 ] && [ "$i" -ge "$SAMPLES" ] && break
  sleep "$INTERVAL"
done
