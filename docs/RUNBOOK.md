# Runbook — Retrieval + RAG v1

Operational guide for running, demoing, verifying, and debugging the stack.

## Bring up / tear down

```bash
make up      # build images, start all services, register the Debezium connector
make ps      # container status
make logs    # tail logs
make down    # stop + remove (also deletes volumes)
```

On first boot, `deploy/postgres/init.sql` creates the `documents` table (with a
version-bump trigger and `REPLICA IDENTITY FULL`), the `retrieval_pub` publication,
and 8 seed rows. The Debezium connector then snapshots those rows into
`cdc.public.documents`; the normalizer routes them to `docs.fast` / `docs.bulk`; the
indexer writes them to OpenSearch + Qdrant.

## Verify

```bash
make e2e     # writes a unique row, asserts it is searchable within 30s,
             # asserts hybrid retrieval + a cited, faithful RAG answer
make eval    # ablation table (BM25 → +vector → +RRF → +rerank) + faithfulness,
             # with CI gates (rerank !< bm25 nDCG; faithfulness >= 0.9)
make test    # Go + Python unit tests, in containers
```

Manual checks:

```bash
# doc counts in both stores
curl -s localhost:9200/documents/_count
curl -s localhost:6333/collections/documents | python3 -m json.tool

# consumer lag (should be ~0 at rest)
docker compose exec redpanda rpk group describe indexer-bulk
docker compose exec redpanda rpk group describe indexer-fast

# freshness SLO metric
curl -s localhost:9101/metrics | grep doc_freshness_lag_seconds
```

## Simulating production scale (load testing)

The 8-doc seed proves correctness; a load test proves behavior under volume. There are
two scenarios worth simulating, and they stress different parts of the system:

1. **Backfill** — bulk-onboard a large corpus. Stresses the *write path* (indexer
   throughput, embedding, sink bulk-write, memory). Metric: throughput + whether lag drains.
2. **Sustained stream** — a continuous CDC change stream. Stresses the *freshness SLO*.
   Metric: `doc_freshness_lag_seconds` p99 while the bulk lane is busy.

`cmd/loadgen` writes to the Postgres source (the real front door — everything flows
through CDC exactly as in production). Run it via the Make targets:

```bash
# Bulk-onboard 1,000,000 docs targeting 5,000/s (20% urgent tier)
make loadtest-backfill COUNT=1000000 RATE=5000

# Sustain 1,000 writes/s for 2 minutes (25% urgent, 30% updates)
make loadtest-stream RATE=1000 DUR=2m URGENT=25 UPDATE=30

# In another terminal, watch the load signals live:
make monitor            # OS_docs, fast_lag, bulk_lag, freshness p99/p50, index rate
```

`loadgen` runs in a throwaway Go container on the compose network, so nothing extra is
needed on the host. It reports achieved write throughput; the pipeline reacts asynchronously.

### Scaling the write path

The topics have 4 partitions per lane, so up to 4 indexer replicas per lane do useful work:

```bash
make scale-indexer N=4     # run 4 indexer replicas (docker compose --scale)
```

### Reference numbers (this laptop-scale stack)

Measured on the reference machine (10 vCPU / 8 GB Docker), single indexer replica,
deterministic `hash` embeddings:

| Scenario | Result |
|---|---|
| Backfill write rate into Postgres | ~4,900 docs/s |
| Sustained stream | 813 writes/s for 60s (34.2k inserts + 14.9k updates), lag stayed small and drained |
| Steady-state indexer throughput | ~500–600 docs/s |
| Urgent freshness p50 under sustained load | ~1.2 s (SLO 5 s) |
| Store consistency after 54k docs | OpenSearch = Qdrant = 54,163, DLQ = 0 |

> The first freshness bottleneck at higher volumes is **embedding** (the `hash` backend is
> µs; a real cross-encoder/sentence-transformer on CPU is 10–50 ms/doc). To scale: add
> indexer replicas, scale the ML service (HPA on CPU), and switch the bulk lane to
> interval-based OpenSearch refresh instead of `refresh=true` per bulk.

## Ports

| Service | Host port |
|---|---|
| Query API | 8080 |
| OpenSearch | 9200 |
| Qdrant REST / gRPC | 6333 / 6334 |
| Redpanda Kafka / admin | 9092 / 9644 |
| Kafka Connect (Debezium) | 8083 |
| Postgres | 5433 → 5432 (remapped to avoid host conflicts) |
| Prometheus / Grafana | 9090 / 3000 |
| Metrics: normalizer / indexer / query | 9100 / 9101 / 9102 |

## Common issues

**Nothing gets indexed / consumer lag stuck.**
Check the connector is RUNNING: `curl localhost:8083/connectors/postgres-documents-connector/status`.
Check the CDC topic has messages: `docker compose exec redpanda rpk topic describe cdc.public.documents -p`.
Check the DLQ: `docker compose exec redpanda rpk topic describe docs.dlq -p` — a growing DLQ
means events are failing validation/decoding (inspect with `rpk topic consume docs.dlq -o start`).

**Qdrant "Vector dimension error: expected N, got 0".**
The official Go client and the Qdrant server must be within one minor version. The
compose file pins the server to match the client (`v1.19.x`). If you bump one, bump both.

**OpenSearch won't start (exit 137).**
Out of memory. Heap is pinned to 512 MB (`OPENSEARCH_JAVA_OPTS`); give Docker ≥ 4 GB.

**Port already allocated on `make up`.**
Another process holds one of the host ports above. Stop it or remap the port in
`docker-compose.yml` (Postgres is already remapped to 5433 for this reason).

## Using a real LLM

The default RAG provider is deterministic and needs no key. To use Anthropic:

```bash
export LLM_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-...
docker compose up -d query
```

## Using real embedding / rerank models

The ML image defaults to deterministic `hash` / `lexical` backends (fast, no downloads).
To use real models, build the ML image with the full requirements and switch backends:

```bash
docker compose build --build-arg REQS=requirements.txt mlservice
# then set EMBED_BACKEND=auto and RERANK_BACKEND=auto in the mlservice env
```
