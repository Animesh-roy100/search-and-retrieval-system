# Slim ML service image: deterministic hash/lexical backends, no torch.
# For real models, build with --build-arg REQS=requirements.txt and set
# EMBED_BACKEND=auto / RERANK_BACKEND=auto.
FROM python:3.12-slim AS base

ARG REQS=requirements-slim.txt
WORKDIR /app

COPY requirements.txt requirements-slim.txt ./
RUN pip install --no-cache-dir -r ${REQS}

COPY embedder.py reranker.py app.py test_ml.py ./

ENV EMBED_BACKEND=hash \
    RERANK_BACKEND=lexical \
    PORT=8000

EXPOSE 8000
HEALTHCHECK --interval=10s --timeout=3s --retries=10 --start-period=10s \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/health').status==200 else 1)"

CMD ["sh", "-c", "uvicorn app:app --host 0.0.0.0 --port ${PORT}"]
