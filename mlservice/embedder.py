"""Embedding backends.

Two backends share one interface:

* ``sentence-transformers`` — production-correct dense embeddings (768d).
* ``hash`` — a deterministic, dependency-light fallback so the whole stack is
  testable offline with no model download. It hashes token n-grams into a fixed
  768-d space and L2-normalizes, so lexical overlap still produces cosine signal.

Select with the ``EMBED_BACKEND`` env var (``auto`` tries sentence-transformers,
falls back to hash).
"""
from __future__ import annotations

import hashlib
import math
import os
import re
from typing import List

DIM = 768
_TOKEN_RE = re.compile(r"[a-z0-9]+")


def _tokenize(text: str) -> List[str]:
    return _TOKEN_RE.findall(text.lower())


class HashEmbedder:
    """Deterministic hashing embedder. Stable across processes and runs."""

    model_version = "hash-v1"
    dim = DIM

    def embed(self, texts: List[str]) -> List[List[float]]:
        return [self._one(t) for t in texts]

    def _one(self, text: str) -> List[float]:
        vec = [0.0] * DIM
        tokens = _tokenize(text)
        # unigrams + bigrams give a little sequence sensitivity
        grams = tokens + [f"{a}_{b}" for a, b in zip(tokens, tokens[1:])]
        for g in grams:
            h = hashlib.sha1(g.encode()).digest()
            idx = int.from_bytes(h[:4], "big") % DIM
            sign = 1.0 if h[4] & 1 else -1.0
            vec[idx] += sign
        norm = math.sqrt(sum(v * v for v in vec))
        if norm == 0.0:
            return vec
        return [v / norm for v in vec]


class SentenceTransformerEmbedder:
    """Real dense embeddings via sentence-transformers (lazy import)."""

    def __init__(self, model_name: str):
        from sentence_transformers import SentenceTransformer  # noqa: WPS433

        self._model = SentenceTransformer(model_name)
        self.model_version = f"st-{model_name}"
        self.dim = self._model.get_sentence_embedding_dimension()

    def embed(self, texts: List[str]) -> List[List[float]]:
        vecs = self._model.encode(texts, normalize_embeddings=True)
        return [v.tolist() for v in vecs]


def build_embedder():
    backend = os.getenv("EMBED_BACKEND", "auto").lower()
    model_name = os.getenv("EMBED_MODEL", "sentence-transformers/all-mpnet-base-v2")
    if backend == "hash":
        return HashEmbedder()
    if backend in ("auto", "sentence-transformers", "st"):
        try:
            return SentenceTransformerEmbedder(model_name)
        except Exception as exc:  # noqa: BLE001
            if backend != "auto":
                raise
            print(f"[ml] sentence-transformers unavailable ({exc}); using hash embedder")
            return HashEmbedder()
    raise ValueError(f"unknown EMBED_BACKEND={backend}")
