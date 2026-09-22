#!/usr/bin/env bash
# End-to-end verification:
#  1. Freshness: write a NEW row, assert it becomes searchable within a budget.
#  2. Hybrid retrieval returns relevant docs.
#  3. RAG answer is cited and faithful.
set -euo pipefail

QUERY_URL="${QUERY_URL:-http://localhost:8080}"
BUDGET_SEC="${BUDGET_SEC:-30}"
fail() { echo "E2E FAIL: $*" >&2; exit 1; }
pass() { echo "  ✓ $*"; }

echo "== 1. Freshness: write a unique row and wait for it to be searchable =="
TOKEN="zebra$(date +%s)"
docker compose exec -T postgres psql -U retrieval -d retrieval -v ON_ERROR_STOP=1 -c \
  "INSERT INTO documents (tenant_id,title,body,category,tier) VALUES (1,'Freshness probe ${TOKEN}','The magic token is ${TOKEN} and it proves near real time indexing works end to end.','probe','urgent');" >/dev/null
echo "  inserted probe row with token ${TOKEN}"

start=$(date +%s)
found=""
while [ $(( $(date +%s) - start )) -lt "$BUDGET_SEC" ]; do
  resp=$(curl -sf -X POST "$QUERY_URL/search" -H 'Content-Type: application/json' \
    -d "{\"query\":\"${TOKEN}\"}" || true)
  if echo "$resp" | grep -q "$TOKEN"; then found="yes"; break; fi
  sleep 1
done
elapsed=$(( $(date +%s) - start ))
[ -n "$found" ] || fail "probe row not searchable within ${BUDGET_SEC}s"
pass "probe searchable in ~${elapsed}s (freshness budget ${BUDGET_SEC}s)"

echo "== 2. Hybrid retrieval returns relevant docs =="
resp=$(curl -sf -X POST "$QUERY_URL/search" -H 'Content-Type: application/json' \
  -d '{"query":"how does reciprocal rank fusion combine bm25 and vector search"}')
echo "$resp" | grep -qi "fusion\|rrf\|bm25" || fail "hybrid retrieval returned no relevant doc: $resp"
pass "hybrid retrieval returned relevant results"

echo "== 3. RAG answer is cited and faithful =="
resp=$(curl -sf -X POST "$QUERY_URL/ask" -H 'Content-Type: application/json' \
  -d '{"query":"How does Debezium capture changes from PostgreSQL?"}')
echo "$resp"
echo "$resp" | grep -q "doc_1" || fail "no citation marker in answer"
faith=$(echo "$resp" | sed -n 's/.*"faithfulness":\([0-9.]*\).*/\1/p')
[ -n "$faith" ] || fail "no faithfulness score"
awk "BEGIN{exit !($faith >= 0.9)}" || fail "faithfulness $faith below 0.9"
pass "RAG answer cited (doc_1) and faithful ($faith)"

echo
echo "E2E PASS ✅"
