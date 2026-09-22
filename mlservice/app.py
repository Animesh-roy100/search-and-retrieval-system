"""ML service: embeddings + reranking over HTTP.

Endpoints:
  GET  /health
  GET  /metrics                 Prometheus
  POST /embed    {texts:[...]}   -> {vectors:[[...]], model_version, dim}
  POST /rerank   {query, docs:[...]} -> {scores:[...], order:[...], model_version}
"""
from __future__ import annotations

from typing import List

from fastapi import FastAPI
from prometheus_client import Counter, Histogram, make_asgi_app
from pydantic import BaseModel

from embedder import build_embedder
from reranker import build_reranker

app = FastAPI(title="ml-service", version="1.0.0")
app.mount("/metrics", make_asgi_app())

EMBED_LAT = Histogram("ml_embed_seconds", "Embedding latency")
RERANK_LAT = Histogram("ml_rerank_seconds", "Rerank latency")
EMBED_DOCS = Counter("ml_embed_texts_total", "Texts embedded")

_embedder = build_embedder()
_reranker = build_reranker()


class EmbedRequest(BaseModel):
    texts: List[str]


class EmbedResponse(BaseModel):
    vectors: List[List[float]]
    model_version: str
    dim: int


class RerankRequest(BaseModel):
    query: str
    docs: List[str]


class RerankResponse(BaseModel):
    scores: List[float]
    order: List[int]
    model_version: str


@app.get("/health")
def health():
    return {"status": "ok", "embed_model": _embedder.model_version, "dim": _embedder.dim}


@app.post("/embed", response_model=EmbedResponse)
def embed(req: EmbedRequest):
    with EMBED_LAT.time():
        vectors = _embedder.embed(req.texts)
    EMBED_DOCS.inc(len(req.texts))
    return EmbedResponse(vectors=vectors, model_version=_embedder.model_version, dim=_embedder.dim)


@app.post("/rerank", response_model=RerankResponse)
def rerank(req: RerankRequest):
    with RERANK_LAT.time():
        scores = _reranker.score(req.query, req.docs)
    order = sorted(range(len(scores)), key=lambda i: scores[i], reverse=True)
    return RerankResponse(scores=scores, order=order, model_version=_reranker.model_version)
