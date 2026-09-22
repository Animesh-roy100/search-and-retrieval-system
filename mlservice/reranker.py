"""Reranker backends.

* ``cross-encoder`` — production-correct cross-encoder relevance scoring.
* ``lexical`` — deterministic fallback scoring query/doc token overlap (Jaccard
  + coverage), so reranking is testable offline. Select via ``RERANK_BACKEND``.
"""
from __future__ import annotations

import os
import re
from typing import List

_TOKEN_RE = re.compile(r"[a-z0-9]+")


def _tokens(text: str) -> set:
    return set(_TOKEN_RE.findall(text.lower()))


class LexicalReranker:
    model_version = "lexical-v1"

    def score(self, query: str, docs: List[str]) -> List[float]:
        q = _tokens(query)
        if not q:
            return [0.0 for _ in docs]
        out = []
        for d in docs:
            dt = _tokens(d)
            if not dt:
                out.append(0.0)
                continue
            inter = len(q & dt)
            union = len(q | dt)
            jaccard = inter / union if union else 0.0
            coverage = inter / len(q)  # how much of the query the doc covers
            out.append(0.5 * jaccard + 0.5 * coverage)
        return out


class CrossEncoderReranker:
    def __init__(self, model_name: str):
        from sentence_transformers import CrossEncoder  # noqa: WPS433

        self._model = CrossEncoder(model_name)
        self.model_version = f"ce-{model_name}"

    def score(self, query: str, docs: List[str]) -> List[float]:
        pairs = [[query, d] for d in docs]
        scores = self._model.predict(pairs)
        return [float(s) for s in scores]


def build_reranker():
    backend = os.getenv("RERANK_BACKEND", "auto").lower()
    model_name = os.getenv("RERANK_MODEL", "cross-encoder/ms-marco-MiniLM-L-6-v2")
    if backend == "lexical":
        return LexicalReranker()
    if backend in ("auto", "cross-encoder", "ce"):
        try:
            return CrossEncoderReranker(model_name)
        except Exception as exc:  # noqa: BLE001
            if backend != "auto":
                raise
            print(f"[ml] cross-encoder unavailable ({exc}); using lexical reranker")
            return LexicalReranker()
    raise ValueError(f"unknown RERANK_BACKEND={backend}")
