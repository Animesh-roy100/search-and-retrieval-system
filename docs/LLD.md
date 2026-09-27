# Low-Level Design (LLD) — Near Real-Time Multi-Source Retrieval + RAG System

> Component internals, data schemas, algorithms, and failure handling.
> Companion to `HLD.md`. **Rev 3** — implemented v1: OpenSearch-authority idempotency,
> tenant isolation + auth, generation-invalidated + semantic caching, LLM/lexical
> faithfulness, and OpenTelemetry tracing + Tempo service graph.

---

## 1. Repository Layout

```
search-and-retrieval-system/
├── sources/            # NEW: source-specific connectors + normalizers
│   ├── postgres/       # Debezium CDC → canonical doc
│   ├── s3/             # file events/poll → parse + chunk → canonical doc
│   ├── normalizer/     # shared canonical-doc builder + priority router
│   └── contract/       # canonical doc schema (proto/json)
├── indexer/            # Go: Redpanda consumer → OpenSearch + Qdrant (source-agnostic)
│   ├── cmd/indexer/
│   ├── internal/consumer/     # batch, offset mgmt, backpressure, fast/bulk lanes
│   ├── internal/sink/         # opensearch + qdrant bulk writers
│   ├── internal/idempotency/  # per-source version guard
│   ├── internal/dlq/          # dead-letter + replay
│   └── internal/metrics/      # prometheus + freshness lag (labeled source,tier)
├── mlservice/          # Python: embeddings + reranker (gRPC/HTTP)
├── query/              # Go: query planning + hybrid retrieval + RRF + RAG
│   ├── internal/plan/         # query understanding & planning (Broker-style)
│   ├── internal/retrieve/     # bm25, vector, rrf, mmr (source filters)
│   ├── internal/rag/          # prompt, generate, lexical + llm-judge faithfulness, cite(source)
│   ├── internal/cache/        # Redis exact-match cache + per-tenant generation counter
│   └── internal/semcache/     # Qdrant-backed semantic answer cache (tenant+gen scoped)
├── internal/tracing/   # shared OpenTelemetry OTLP setup + W3C propagation (Go services)
├── eval/               # Python: nDCG/MRR/recall + faithfulness eval
├── kafka/              # Redpanda + Debezium connector config + compose
├── deploy/             # docker-compose + Helm + observability stack (Prometheus, Tempo, Grafana)
└── docs/               # HLD.md, LLD.md
```

---

## 2. Data Model

### 2.1 Canonical document (the multi-source contract)

Emitted by every normalizer to Redpanda. Downstream code only ever sees this.

```json
{
  "source": "postgres",
  "source_id": "documents:42",
  "doc_id": "postgres:documents:42",
  "tenant_id": 7,
  "op": "upsert",
  "tier": "urgent",
  "version": 3,
  "commit_ts": "2026-09-15T10:00:00Z",
  "title": "...",
  "body": "...",
  "chunk_id": 0,
  "metadata": { "category": "infra", "url": "...", "author": "..." }
}
```

| Field | Meaning |
|---|---|
| `source` | provenance; also a filterable facet |
| `doc_id` | globally unique = `{source}:{native_id}[:chunk]` |
| `version` | **per-source** monotonic (LSN / resume token / ETag / updated_at) |
| `tier` | `urgent` → docs.fast, `bulk` → docs.bulk |
| `commit_ts` | per-source freshness clock start |
| `chunk_id` | for unstructured sources split into chunks |

### 2.2 Source table (PostgreSQL)

```sql
CREATE TABLE documents (
    id         BIGSERIAL PRIMARY KEY,
    tenant_id  BIGINT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    category   TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version    BIGINT NOT NULL DEFAULT 1
);
CREATE PUBLICATION retrieval_pub FOR TABLE documents;
```

### 2.3 Per-source change mechanism & versioning

| Source | Change mechanism | `version` source | `tier` rule |
|---|---|---|---|
| PostgreSQL | Debezium (WAL) | LSN | stock/price/delete → urgent; else bulk |
| S3 / files | bucket event → SQS, or poll | object version / ETag | new/changed file → bulk (parse cost); deletes → urgent |
| MongoDB (v2) | change stream | resume token | app-defined |
| SaaS API (v2) | webhook / poll `updated_since` | `updated_at` | webhook → urgent; poll → bulk |

### 2.4 OpenSearch mapping (lexical)

```json
{
  "mappings": { "properties": {
    "doc_id":    { "type": "keyword" },
    "source":    { "type": "keyword" },
    "tenant_id": { "type": "keyword" },
    "title":     { "type": "text" },
    "body":      { "type": "text" },
    "category":  { "type": "keyword" },
    "version":   { "type": "long" },
    "commit_ts": { "type": "date" }
  }}
}
```

### 2.5 Qdrant collection (semantic)

```
collection: documents
vector: { size: 768, distance: Cosine }
payload: { doc_id, source, tenant_id, category, version, model_version, commit_ts }
```

### 2.6 Entity relationships

```mermaid
erDiagram
    SOURCE ||--o{ CANONICAL_DOC : "normalizes to"
    CANONICAL_DOC ||--|| OS_DOC : "indexed (BM25)"
    CANONICAL_DOC ||--|| QD_POINT : "embedded (vector)"
    CANONICAL_DOC {
        string doc_id PK
        string source
        bigint tenant_id
        long version
        string tier
        date commit_ts
    }
    OS_DOC {
        keyword doc_id PK
        keyword source
        long version
        date commit_ts
    }
    QD_POINT {
        uuid point_id PK
        string doc_id
        string source
        string model_version
        long version
    }
```

---

## 3. Normalizer & Priority Router (NEW)

The **only** source-specific code. Turns raw source changes into canonical docs and routes by urgency.

```mermaid
flowchart TD
    A["raw change from source"] --> B["extract fields"]
    B --> C{"structured?"}
    C -->|no| D["parse (PDF/HTML→text)<br/>clean + chunk"]
    C -->|yes| E["map fields"]
    D --> F["build canonical doc<br/>namespaced doc_id, per-source version"]
    E --> F
    F --> G["classify tier<br/>(urgent | bulk)"]
    G -->|urgent| H["→ Redpanda docs.fast"]
    G -->|bulk| I["→ Redpanda docs.bulk"]
```

- Chunking (unstructured): each chunk → separate canonical doc with `chunk_id`, sharing parent `doc_id` prefix.
- Tier classification is a small rule set per source (see §2.3).

---

## 4. Go Indexer — Internals

### 4.1 Processing pipeline (dual-lane)

```mermaid
flowchart TD
    F1["consume docs.fast"] --> M["merge"]
    F2["consume docs.bulk"] --> M
    M --> B["decode canonical docs"]
    B --> C{"valid schema?"}
    C -->|no| DLQ["→ docs.dlq"]
    C -->|yes| D{"op"}
    D -->|upsert| E["idempotency check<br/>(source, doc_id, version)"]
    D -->|delete| TS["build tombstone"]
    E --> G{"version > stored<br/>for this source?"}
    G -->|no| SKIP["skip (stale)"]
    G -->|yes| H["embed via ML svc"]
    H --> I["accumulate bulk buffer"]
    TS --> I
    I --> J{"buffer full OR flush interval?"}
    J -->|no| M
    J -->|yes| K["bulk upsert OS + Qdrant"]
    K --> L{"partial failure?"}
    L -->|yes| R["retry failed → DLQ after N"]
    L -->|no| N["commit offsets<br/>observe freshness lag{source,tier}"]
    N --> M
```

> Fast lane gets a smaller flush interval (low latency); bulk lane uses larger batches (throughput). Both feed the same idempotent sink.

### 4.2 Idempotency — OpenSearch is the version authority

Two layers, so correctness does **not** depend on the volatile in-memory guard:

1. **In-memory guard (optimization):** skip obviously stale in-flight docs (`in.Version > stored`), avoiding wasted embeds. Lost on restart/rebalance — so it cannot be the authority.
2. **OpenSearch (durable authority):** every write uses `version_type=external`, **including deletes**. A stale upsert or a stale delete (e.g. a DLQ replay) is rejected with `409`. `Bulk` returns the **set of doc_ids actually applied**; Qdrant is written **only for that set**. Result: the two stores stay consistent even when the guard map is empty.

```go
// OpenSearch _bulk: _id = doc_id, version = in.Version, version_type = external
//   upsert: 2xx = applied · 409 = stale (skip) · other >=400 = hard error (retry)
//   delete: 2xx = applied · 404 = already-absent (applied, safe) · 409 = stale (skip)
// Bulk returns applied[doc_id]; the indexer embeds + upserts/deletes Qdrant ONLY for applied docs.
// Qdrant point_id = UUIDv5(NameSpaceDNS, doc_id)  (google/uuid) → deterministic overwrite.
```

Proven by an integration test (`internal/opensearch`, build tag `integration`): index v3 → replay stale v2 upsert and v1 delete → both rejected, doc stays v3; v4 delete applies.

### 4.3 Delivery & ordering guarantees

| Concern | Mechanism |
|---|---|
| Per-doc ordering | Redpanda key = `doc_id` → same partition |
| At-least-once | commit offsets only after successful bulk write |
| Idempotent apply | OpenSearch external-version authority (§4.2) + deterministic IDs; Qdrant driven off the applied set |
| Stale delete safety | deletes are external-versioned too → a replayed old delete can't remove a re-created doc |
| Poison events | DLQ topic after N retries; benign `404`-on-delete tolerated (does not poison the batch) |
| Idle-lane flush | each poll is bounded to the flush interval → a low-traffic lane's buffered batch is never stranded |
| Backpressure | bounded channel; pause fetch when sinks slow |
| Tier isolation | separate topics; fast lane not blocked by bulk backlog |

---

## 5. Dead-Letter Queue & Replay

```mermaid
stateDiagram-v2
    [*] --> Processing
    Processing --> Retrying: transient error
    Retrying --> Processing: attempt < N
    Retrying --> DeadLettered: attempt >= N
    Processing --> DeadLettered: permanent error (bad schema)
    DeadLettered --> Replay: operator triggers replay CLI
    Replay --> Processing: re-inject to docs.fast/bulk
    Processing --> [*]: success (offset committed)
```

DLQ record: original canonical doc, `source`, error, retry count, first-seen ts, source offset. Replay CLI can filter by `source` or error type.

---

## 6. Retrieval Layer

### 6.1 Hybrid search flow (source-filterable)

```mermaid
flowchart TD
    Q["query + server-injected tenant filter"] --> EXACT{"exact-match cache?<br/>(Redis, key includes gen)"}
    EXACT -->|hit| RET["return cached"]
    EXACT -->|miss| EMB["embed query (once)"]
    EMB --> SEM{"semantic cache?<br/>(Qdrant, cosine ≥ θ, tenant+gen)"}
    SEM -->|hit| RET
    SEM -->|miss| PAR["BM25 ∥ vector (concurrent)"]
    PAR --> RRF["RRF fuse (k=60)"]
    RRF --> TOP["top-50 candidates"]
    TOP --> RERANK["cross-encoder rerank"]
    RERANK --> MMR["MMR diversity + dedup"]
    MMR --> OUT["top-k (carry source per doc)"]
```

**Caching (two layers, freshness-safe):** an exact-match Redis cache (fast path) and a
Qdrant-backed **semantic** cache (paraphrase hits above a cosine threshold). Both are
scoped by `tenant_id` + a per-tenant **generation** counter; the indexer bumps the
generation on every applied write, so any indexed change makes prior cache entries
unreachable — no scan, no stale reads. The query is embedded **once** and the vector is
reused for the semantic lookup and vector retrieval.

### 6.2 RRF algorithm

```go
func rrf(lists [][]DocID, k int) []Scored {
    score := map[DocID]float64{}
    for _, list := range lists {
        for rank, id := range list {
            score[id] += 1.0 / float64(k+rank+1)
        }
    }
    return sortDescByScore(score)
}
```

### 6.3 Filtering & query planning

- Filters (`source`, `tenant_id`, `category`, date) pushed **into both** OpenSearch and Qdrant queries (never post-filtered) — preserves recall + tenant/source isolation.
- **Query Understanding & Planning** (Broker-style) runs first: rewrite/expand, extract filters from NL (`"in confluence"` → `source=confluence`), optional HyDE, route simple vs complex.

---

## 7. RAG Layer (Level 2)

### 7.1 Answer generation flow

```mermaid
sequenceDiagram
    autonumber
    participant Q as RAG Service
    participant ML as ML Service
    participant OS as OpenSearch
    participant QD as Qdrant
    participant LLM as Claude

    Q->>ML: embed(query)
    par
        Q->>OS: BM25 (filtered)
    and
        Q->>QD: vector (filtered)
    end
    Q->>Q: RRF → rerank → top-k (k=5..8)
    Q->>Q: assemble context [source:doc_N]
    Q->>LLM: generate(system=grounding, context, query)
    LLM-->>Q: answer with [source:doc_N] citations
    Q->>LLM: faithfulness judge(answer, context)
    LLM-->>Q: score + unsupported claims
    alt score < threshold
        Q->>Q: regenerate / abstain
    end
    Q-->>Q: attach source-attributed citations
```

### 7.2 Grounding prompt (contract)

```
SYSTEM:
Answer ONLY using the numbered sources. Cite each claim as [doc_N].
If the sources do not contain the answer, reply "Not found in sources."
Do not use outside knowledge. Do not guess.

USER:
Sources:
[doc_1] (source=postgres) {chunk_1}
[doc_2] (source=confluence) {chunk_2}
Question: {query}
```

Model routing: `claude-haiku-4-5` simple, `claude-opus-4-8` complex synthesis.

### 7.3 Faithfulness check (`FAITHFULNESS_MODE`)

Two strategies behind one `ScoreFaithfulness` entry point:

- **`lexical` (default):** deterministic token-overlap per sentence (flow below). No API
  cost; correct calibration for the extractive provider (verbatim answers).
- **`llm`:** the LLM provider's `JudgeFaithfulness` scores grounding (understands
  paraphrase/entailment), returning `{score, unsupported[]}`. **Falls back to lexical on
  any error.** Right calibration for a generative LLM answer.

```mermaid
flowchart LR
    A["answer sentences"] --> B["per claim"]
    B --> C{"supported by any source chunk?<br/>(lexical overlap OR llm judge)"}
    C -->|yes| D["keep"]
    C -->|no| E["mark unsupported"]
    D --> F["faithfulness = supported/total"]
    E --> F
    F --> G{"< threshold?"}
    G -->|yes| H["regenerate or abstain"]
    G -->|no| I["return answer + score"]
```

### 7.4 Response schema (source-attributed)

```json
{
  "answer": "Debezium reads the WAL [doc_1] and Redpanda buffers it [doc_2].",
  "citations": [
    { "marker": "doc_1", "doc_id": "postgres:documents:42", "source": "postgres", "score": 0.91 },
    { "marker": "doc_2", "doc_id": "confluence:page:88", "source": "confluence", "score": 0.87 }
  ],
  "faithfulness": 1.0,
  "latency_ms": 240,
  "model": "claude-opus-4-8"
}
```

---

## 8. ML Service (Python) — Interface

```mermaid
classDiagram
    class MLService {
        +Embed(texts[]) EmbedResponse
        +Rerank(query, docs[]) RerankResponse
        +health() Status
    }
    class EmbedResponse {
        +vectors float[][]
        +model_version string
    }
    class RerankResponse {
        +scores float[]
        +order int[]
    }
    MLService --> EmbedResponse
    MLService --> RerankResponse
```

Stateless; HPA on CPU/GPU. `model_version` written to Qdrant payload → enables reindex cutover. Batched inference.

---

## 9. Zero-Downtime Reindex (alias swap)

```mermaid
sequenceDiagram
    autonumber
    participant OP as Operator
    participant IDX as Indexer
    participant OS as OpenSearch
    participant QD as Qdrant

    OP->>OS: create documents_v2 (new mapping)
    OP->>QD: create documents_v2 (new model dim)
    OP->>IDX: backfill from all sources → v2 (new model_version)
    IDX->>IDX: dual-write live docs to v1 + v2
    OP->>OS: move alias "documents" → v2 (atomic)
    OP->>QD: switch query collection → v2
    Note over OP: queries never stop; v1 retired after soak
```

---

## 10. Observability — Signals

### 10.1 Freshness lag (signature metric, per source & tier)

```mermaid
flowchart LR
    A["source commit_ts"] -->|carried in canonical doc| B["Redpanda fast/bulk"]
    B --> C["Indexer"]
    C -->|"on write: lag = now - commit_ts"| E["histogram:<br/>doc_freshness_lag_seconds{source,tier}"]
    E --> F["Grafana panels per source+tier<br/>SLO alert: urgent p99 < 5s"]
```

### 10.2 Metric catalog

| Metric | Type | Labels | Source |
|---|---|---|---|
| `doc_freshness_lag_seconds` | histogram | source, tier | indexer |
| `indexer_bulk_write_seconds` | histogram | sink | indexer |
| `indexer_dlq_events_total` | counter | source, reason | indexer |
| consumer group lag | gauge | topic | Redpanda |
| `rag_faithfulness_score` | histogram | — | query svc |
| `search_stage_latency_seconds` | histogram | stage | query svc |
| `cache_hits_total / misses_total` | counter | — | query svc |

### 10.3 Collection topology

```mermaid
graph LR
    RP["Redpanda /public_metrics"] --> PROM
    IDX["indexer /metrics"] --> PROM
    QRY["query /metrics + OTel"] --> PROM
    ML["ml /metrics"] --> PROM
    PROM["Prometheus (agent)"] -->|remote_write| CORTEX["Mimir/Cortex"]
    CORTEX --> MINIO[("MinIO")]
    QRY & IDX & ML -->|OTLP spans| TEMPO["Tempo"]
    TEMPO -->|service-graph metrics| PROM
    CORTEX & TEMPO --> GRAF["Grafana"]
    PROM --> ALERT["Alertmanager"]
```

**Implemented stack:** Prometheus + **Tempo** + Grafana, wired end-to-end. **Distributed
tracing** (OpenTelemetry, W3C propagation) spans `query → mlservice` on the read path and
`indexer → mlservice` on the write path — one `/ask` yields a single trace across Go+Python.
Tempo's metrics-generator emits **service-graph** metrics (`traces_service_graph_*`) to
Prometheus, rendered as a live topology (nodes = services, edges = call rate/latency/errors)
in Grafana. Mimir/Cortex + MinIO remain the scale-out option for long-term metric storage.

---

## 11. Evaluation Harness

```mermaid
flowchart TD
    GS["golden set (queries + labels, multi-source)"] --> RUN["run each config"]
    RUN --> A["BM25 only"]
    RUN --> B["+ vector"]
    RUN --> C["+ RRF"]
    RUN --> D["+ rerank"]
    A & B & C & D --> M["nDCG@10, MRR, Recall@100"]
    GS --> RAGE["RAG eval: faithfulness, citation accuracy"]
    M & RAGE --> TBL["ablation table (CI-gated)"]
```

---

## 12. Failure Modes & Degradation

| Failure | Behavior |
|---|---|
| Qdrant down | BM25-only (degraded, flagged) |
| OpenSearch down | vector-only |
| ML service down | skip rerank, return RRF order |
| LLM down/timeout | return retrieved docs, no generated answer |
| Faithfulness < threshold | abstain or regenerate |
| Indexer crash | resume from committed offset, idempotent |
| Poison event | DLQ + alert, pipeline continues |
| One source connector down | other sources keep flowing (isolated normalizers) |
| Bulk backlog | fast lane unaffected (separate topic) |

---

## 13. Configuration (env)

```
# Redpanda
REDPANDA_BROKERS, FAST_TOPIC=docs.fast, BULK_TOPIC=docs.bulk, DLQ_TOPIC=docs.dlq, CONSUMER_GROUP
# Sources
PG_DSN, PG_PUBLICATION=retrieval_pub
S3_BUCKET, S3_POLL_INTERVAL, S3_EVENT_QUEUE
# Indexer
FAST_FLUSH_MS=200, BULK_FLUSH_MS=2000, BATCH_SIZE, MAX_IN_FLIGHT, BULK_RETRIES
# Stores
OPENSEARCH_URL, QDRANT_HOST, QDRANT_PORT, REDIS_ADDR
# ML
ML_SERVICE_ADDR, EMBED_MODEL, RERANK_MODEL
# RAG
LLM_MODEL_FAST=claude-haiku-4-5, LLM_MODEL_QUALITY=claude-opus-4-8
FAITHFULNESS_THRESHOLD=0.9, RAG_TOP_K=6, RERANK_TOP_N=50, RRF_K=60
# Caching (query) + freshness invalidation (indexer bumps gen via REDIS_ADDR)
CACHE_TTL=5m, SEM_CACHE=true, SEM_CACHE_THRESHOLD=0.95
# Quality / security / limits (query)
FAITHFULNESS_MODE=lexical|llm, QUERY_API_KEY, REQUEST_TIMEOUT=5s
# Tracing (all services) — unset = graceful no-op
OTEL_EXPORTER_OTLP_ENDPOINT=http://tempo:4317, OTEL_EXPORTER_OTLP_INSECURE=true
```

See `HLD.md` for architecture, NFRs, prior art, and roadmap.
