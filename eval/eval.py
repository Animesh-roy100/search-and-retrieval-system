"""Offline retrieval + RAG evaluation harness.

Runs an ablation over retrieval configurations (BM25 → +vector → +RRF → +rerank)
against the live OpenSearch, Qdrant and ML services, and reports nDCG@10, MRR and
Recall@k on a golden set. Also reports RAG faithfulness via the query service /ask.

Metrics (nDCG, MRR, Recall) are pure functions and unit-tested in test_eval.py.
"""
from __future__ import annotations

import argparse
import json
import math
import os
import urllib.request
from typing import Dict, List


# --------------------------------------------------------------------------- metrics
def dcg(relevances: List[int]) -> float:
    return sum(rel / math.log2(i + 2) for i, rel in enumerate(relevances))


def ndcg_at_k(ranked_ids: List[str], relevant: set, k: int = 10) -> float:
    gains = [1 if doc in relevant else 0 for doc in ranked_ids[:k]]
    ideal = sorted(gains, reverse=True)
    idcg = dcg(ideal)
    return dcg(gains) / idcg if idcg > 0 else 0.0


def mrr(ranked_ids: List[str], relevant: set) -> float:
    for i, doc in enumerate(ranked_ids):
        if doc in relevant:
            return 1.0 / (i + 1)
    return 0.0


def recall_at_k(ranked_ids: List[str], relevant: set, k: int = 100) -> float:
    if not relevant:
        return 0.0
    hit = sum(1 for doc in ranked_ids[:k] if doc in relevant)
    return hit / len(relevant)


# --------------------------------------------------------------------------- http
def _post(url: str, payload: dict) -> dict:
    data = json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())


def bm25_ids(os_url: str, query: str, k: int) -> List[str]:
    body = {"size": k, "query": {"multi_match": {"query": query, "fields": ["title^2", "body"]}}, "_source": ["doc_id"]}
    out = _post(f"{os_url}/documents/_search", body)
    return [h["_source"]["doc_id"] for h in out["hits"]["hits"]]


def vector_ids(ml_url: str, qd_url: str, query: str, k: int) -> List[str]:
    vec = _post(f"{ml_url}/embed", {"texts": [query]})["vectors"][0]
    out = _post(f"{qd_url}/collections/documents/points/search",
                {"vector": vec, "limit": k, "with_payload": True})
    return [r["payload"]["doc_id"] for r in out["result"]]


def rrf(lists: List[List[str]], k: int = 60) -> List[str]:
    score: Dict[str, float] = {}
    for lst in lists:
        for rank, doc in enumerate(lst):
            score[doc] = score.get(doc, 0.0) + 1.0 / (k + rank + 1)
    return [d for d, _ in sorted(score.items(), key=lambda kv: (-kv[1], kv[0]))]


def rerank(ml_url: str, os_url: str, query: str, ids: List[str]) -> List[str]:
    # fetch text for candidates
    out = _post(f"{os_url}/documents/_mget", {"ids": ids})
    texts, have = [], []
    for d in out["docs"]:
        if d.get("found"):
            src = d["_source"]
            texts.append(src.get("title", "") + " " + src.get("body", ""))
            have.append(src["doc_id"])
    if not have:
        return ids
    res = _post(f"{ml_url}/rerank", {"query": query, "docs": texts})
    return [have[i] for i in res["order"]]


# --------------------------------------------------------------------------- run
def load_golden(path: str) -> List[dict]:
    with open(path) as fh:
        return json.load(fh)


def run_ablation(golden: List[dict], os_url: str, qd_url: str, ml_url: str, k: int = 10):
    configs = ["bm25", "+vector", "+rrf", "+rerank"]
    agg = {c: {"ndcg": 0.0, "mrr": 0.0, "recall": 0.0} for c in configs}

    for row in golden:
        q, rel = row["query"], set(row["relevant_doc_ids"])
        b = bm25_ids(os_url, q, 50)
        v = vector_ids(ml_url, qd_url, q, 50)
        fused = rrf([b, v])
        reranked = rerank(ml_url, os_url, q, fused[:50])

        ranked_by_config = {"bm25": b, "+vector": v, "+rrf": fused, "+rerank": reranked}
        for c in configs:
            r = ranked_by_config[c]
            agg[c]["ndcg"] += ndcg_at_k(r, rel, k)
            agg[c]["mrr"] += mrr(r, rel)
            agg[c]["recall"] += recall_at_k(r, rel, 100)

    n = len(golden)
    for c in configs:
        for m in agg[c]:
            agg[c][m] /= n
    return configs, agg


def run_faithfulness(golden: List[dict], query_url: str):
    scores, cited = [], 0
    for row in golden:
        resp = _post(f"{query_url}/ask", {"query": row["query"]})
        scores.append(resp.get("faithfulness", 0.0))
        if resp.get("citations"):
            cited += 1
    avg = sum(scores) / len(scores) if scores else 0.0
    return avg, cited / len(golden) if golden else 0.0


def print_table(configs, agg):
    print(f"\n{'config':<10} {'nDCG@10':>9} {'MRR':>7} {'Recall@100':>11}")
    print("-" * 40)
    for c in configs:
        m = agg[c]
        print(f"{c:<10} {m['ndcg']:>9.3f} {m['mrr']:>7.3f} {m['recall']:>11.3f}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--golden", default=os.path.join(os.path.dirname(__file__), "golden.json"))
    ap.add_argument("--os-url", default=os.getenv("OPENSEARCH_URL", "http://localhost:9200"))
    ap.add_argument("--qd-url", default=os.getenv("QDRANT_URL", "http://localhost:6333"))
    ap.add_argument("--ml-url", default=os.getenv("ML_SERVICE_ADDR", "http://localhost:8000"))
    ap.add_argument("--query-url", default=os.getenv("QUERY_URL", "http://localhost:8080"))
    args = ap.parse_args()

    golden = load_golden(args.golden)
    print(f"Loaded {len(golden)} golden queries")

    configs, agg = run_ablation(golden, args.os_url, args.qd_url, args.ml_url)
    print_table(configs, agg)

    avg_faith, cite_acc = run_faithfulness(golden, args.query_url)
    print(f"\nRAG faithfulness (avg): {avg_faith:.3f}")
    print(f"Citation coverage:      {cite_acc:.3f}")

    # CI gate: rerank should not hurt nDCG vs bm25, and faithfulness must clear the bar.
    assert agg["+rerank"]["ndcg"] >= agg["bm25"]["ndcg"] - 1e-9, "rerank regressed nDCG"
    assert avg_faith >= 0.9, f"faithfulness {avg_faith:.3f} below 0.9"
    print("\nEval gates passed ✅")


if __name__ == "__main__":
    main()
