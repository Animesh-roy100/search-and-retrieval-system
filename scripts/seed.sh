#!/usr/bin/env bash
# The initial seed is loaded by deploy/postgres/init.sql on first boot.
# This script inserts an extra urgent row and can be re-run to generate change events.
set -euo pipefail
PGURL="${PGURL:-postgresql://retrieval:retrieval@localhost:5432/retrieval}"

docker compose exec -T postgres psql "$PGURL" -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO documents (tenant_id, title, body, category, tier) VALUES
(1, 'Backpressure in the indexer',
 'When sinks slow down the indexer applies backpressure by pausing fetches, so the consumer never outruns the write path and memory stays bounded.',
 'indexing', 'bulk');
SQL
echo "Seed row inserted."
