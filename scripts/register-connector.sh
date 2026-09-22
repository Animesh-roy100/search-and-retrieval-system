#!/usr/bin/env bash
# Register the Debezium Postgres connector (idempotent).
set -euo pipefail
CONNECT_URL="${CONNECT_URL:-http://localhost:8083}"
CFG="$(dirname "$0")/../kafka/debezium-connector.json"

echo "Waiting for Kafka Connect at ${CONNECT_URL} ..."
for i in $(seq 1 60); do
  if curl -sf "${CONNECT_URL}/connectors" >/dev/null 2>&1; then break; fi
  sleep 2
done

name="$(grep -o '"name"[^,]*' "$CFG" | head -1 | cut -d'"' -f4)"
if curl -sf "${CONNECT_URL}/connectors/${name}" >/dev/null 2>&1; then
  echo "Connector ${name} already registered."
  exit 0
fi

echo "Registering connector ${name} ..."
curl -sf -X POST -H "Content-Type: application/json" \
  --data @"${CFG}" "${CONNECT_URL}/connectors" | sed 's/,/,\n/g'
echo
echo "Done."
