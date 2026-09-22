"""Unit tests for the pure metric functions (no live services)."""
from eval import ndcg_at_k, mrr, recall_at_k, rrf


def test_ndcg_perfect_vs_bad():
    rel = {"a"}
    perfect = ndcg_at_k(["a", "b", "c"], rel, 10)
    worse = ndcg_at_k(["b", "c", "a"], rel, 10)
    assert perfect == 1.0
    assert worse < perfect


def test_ndcg_no_relevant():
    assert ndcg_at_k(["x", "y"], set(), 10) == 0.0


def test_mrr():
    assert mrr(["a", "b"], {"a"}) == 1.0
    assert mrr(["b", "a"], {"a"}) == 0.5
    assert mrr(["x", "y"], {"a"}) == 0.0


def test_recall_at_k():
    assert recall_at_k(["a", "b", "c"], {"a", "b"}, 100) == 1.0
    assert recall_at_k(["a", "x", "y"], {"a", "b"}, 100) == 0.5
    assert recall_at_k([], {"a"}, 100) == 0.0


def test_rrf_prefers_docs_in_both_lists():
    order = rrf([["a", "b"], ["b", "c"]])
    assert order[0] == "b"  # appears in both lists


def test_rrf_deterministic_tiebreak():
    order = rrf([["z"], ["a"]])
    assert order[0] == "a"  # alphabetical tie-break
