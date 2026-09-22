#!/usr/bin/env bash
# Human-friendly demo: write a row, watch it become searchable, ask a cited question.
set -euo pipefail
QUERY_URL="${QUERY_URL:-http://localhost:8080}"

echo "1) Writing a new document to Postgres (urgent tier)..."
TOKEN="demo$(date +%s)"
docker compose exec -T postgres psql -U retrieval -d retrieval -c \
  "INSERT INTO documents (tenant_id,title,body,category,tier) VALUES (1,'Vector databases ${TOKEN}','A vector database like Qdrant stores embeddings and performs approximate nearest neighbour search to find semantically similar documents. Token ${TOKEN}.','vectors','urgent');" >/dev/null
echo "   wrote row token=${TOKEN}"

echo "2) Waiting for it to become searchable (CDC -> normalize -> index)..."
start=$(date +%s)
until curl -sf -X POST "$QUERY_URL/search" -H 'Content-Type: application/json' -d "{\"query\":\"${TOKEN}\"}" | grep -q "$TOKEN"; do
  sleep 1
done
echo "   searchable after ~$(( $(date +%s) - start ))s"

echo "3) Hybrid search: 'what is a vector database'"
curl -sf -X POST "$QUERY_URL/search" -H 'Content-Type: application/json' \
  -d '{"query":"what is a vector database and approximate nearest neighbour search"}' | python3 -m json.tool || true

echo
echo "4) Grounded RAG answer with citations: 'How does hybrid retrieval work?'"
curl -sf -X POST "$QUERY_URL/ask" -H 'Content-Type: application/json' \
  -d '{"query":"How does hybrid retrieval combine BM25 and vector search?"}' | python3 -m json.tool || true

echo
echo "Grafana: http://localhost:3000  (dashboard: Retrieval + RAG — Freshness & Quality)"
