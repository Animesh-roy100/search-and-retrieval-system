"""Tests for the deterministic ML backends (no model download needed)."""
import os

os.environ["EMBED_BACKEND"] = "hash"
os.environ["RERANK_BACKEND"] = "lexical"

from embedder import HashEmbedder, DIM  # noqa: E402
from reranker import LexicalReranker  # noqa: E402


def test_embed_dim_and_norm():
    emb = HashEmbedder()
    vecs = emb.embed(["hello world", "debezium reads the wal"])
    assert len(vecs) == 2
    for v in vecs:
        assert len(v) == DIM
        norm = sum(x * x for x in v) ** 0.5
        assert abs(norm - 1.0) < 1e-6  # L2 normalized


def test_embed_deterministic():
    a = HashEmbedder().embed(["reciprocal rank fusion"])[0]
    b = HashEmbedder().embed(["reciprocal rank fusion"])[0]
    assert a == b


def test_embed_similarity_signal():
    emb = HashEmbedder()
    q, near, far = emb.embed([
        "hybrid retrieval combines bm25 and vectors",
        "hybrid retrieval mixes lexical bm25 with vector search",
        "the cat sat quietly on a warm windowsill",
    ])

    def cos(x, y):
        return sum(a * b for a, b in zip(x, y))

    assert cos(q, near) > cos(q, far)


def test_lexical_rerank_orders_relevant_first():
    rr = LexicalReranker()
    query = "how does debezium capture changes from postgres"
    docs = [
        "the weather today is sunny with a light breeze",
        "debezium captures changes from postgres by reading the wal",
    ]
    scores = rr.score(query, docs)
    assert scores[1] > scores[0]
