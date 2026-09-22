-- Source table + logical replication setup for Debezium CDC.

CREATE TABLE IF NOT EXISTS documents (
    id         BIGSERIAL PRIMARY KEY,
    tenant_id  BIGINT NOT NULL DEFAULT 1,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    category   TEXT,
    -- "urgent" rows travel the fast lane; everything else batches on the bulk lane.
    tier       TEXT NOT NULL DEFAULT 'bulk',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    version    BIGINT NOT NULL DEFAULT 1
);

-- REPLICA IDENTITY FULL so Debezium emits the full "before" image on update/delete.
ALTER TABLE documents REPLICA IDENTITY FULL;

-- Bump version + updated_at automatically on every update so per-source versioning is monotonic.
CREATE OR REPLACE FUNCTION bump_version() RETURNS trigger AS $$
BEGIN
    NEW.version := OLD.version + 1;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_bump_version ON documents;
CREATE TRIGGER trg_bump_version
    BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION bump_version();

-- Publication consumed by Debezium.
DROP PUBLICATION IF EXISTS retrieval_pub;
CREATE PUBLICATION retrieval_pub FOR TABLE documents;

-- Seed corpus (small, topical, good for hybrid-retrieval + RAG demo).
INSERT INTO documents (tenant_id, title, body, category, tier) VALUES
(1, 'How Debezium captures changes',
    'Debezium reads the PostgreSQL write-ahead log (WAL) using logical replication. Each committed row change becomes a change event. This lets downstream systems react to database changes in near real time without polling.',
    'cdc', 'urgent'),
(1, 'Redpanda as a Kafka-compatible broker',
    'Redpanda implements the Kafka API without a JVM or ZooKeeper. It buffers change events durably and lets multiple consumers read at their own pace. It exposes Prometheus metrics natively for observability.',
    'streaming', 'bulk'),
(1, 'What is hybrid retrieval',
    'Hybrid retrieval combines lexical search (BM25) with semantic vector search. BM25 matches exact keywords while vectors capture meaning. Reciprocal Rank Fusion merges the two ranked lists into one robust ordering.',
    'retrieval', 'bulk'),
(1, 'Reciprocal Rank Fusion explained',
    'RRF assigns each document a score of 1/(k+rank) summed across every result list, with k typically 60. It needs no score normalization, which makes it robust when combining BM25 and vector scores that live on different scales.',
    'retrieval', 'bulk'),
(1, 'Why rerank with a cross-encoder',
    'A cross-encoder reranker jointly encodes the query and each candidate document, producing a far more accurate relevance score than the first-stage retriever. It is expensive, so it is only applied to the top candidates.',
    'retrieval', 'bulk'),
(1, 'Grounded generation and faithfulness',
    'In retrieval-augmented generation the model must answer only from the retrieved sources and cite them. A faithfulness check verifies every claim is supported by a source chunk; unsupported claims trigger regeneration or abstention.',
    'rag', 'bulk'),
(1, 'Idempotent indexing with version guards',
    'The indexer applies a change only if its version strictly exceeds the stored version for that document. Combined with deterministic document ids, this makes reprocessing and replay safe: duplicates overwrite rather than corrupt.',
    'indexing', 'urgent'),
(1, 'Freshness lag as an SLO',
    'Freshness lag is the time between a source commit and the document becoming searchable. It is measured as now minus commit timestamp at index time and tracked per source and tier as the systems signature service-level objective.',
    'observability', 'urgent');
