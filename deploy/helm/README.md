# Helm chart — search-and-retrieval (K3s / Kubernetes)

Self-contained umbrella chart: brings up the whole stack (Postgres CDC → Redpanda →
indexer → OpenSearch + Qdrant, query/RAG, ML/LLM, observability) in one `helm install`.
Targets a **single amd64 K3s node with ~16GB RAM**. Defaults match a stock K3s
(`local-path` storage, `traefik` ingress).

## 1. Build + publish the app images

The chart references **your** built images (Go services + ML service):

```sh
# from repo root — build the 3 Go services + the ML service
docker build -f deploy/go.Dockerfile --build-arg SERVICE=normalizer -t $REG/srs-normalizer:latest .
docker build -f deploy/go.Dockerfile --build-arg SERVICE=indexer   -t $REG/srs-indexer:latest .
docker build -f deploy/go.Dockerfile --build-arg SERVICE=query     -t $REG/srs-query:latest .
docker build -t $REG/srs-mlservice:latest ./mlservice          # add --build-arg REQS=requirements.txt for real models
# push to a registry the cluster can pull from...
docker push $REG/srs-normalizer:latest   # etc.
# ...OR import straight into k3s (no registry needed):
docker save $REG/srs-query:latest | sudo k3s ctr images import -
```

Set `global.appRegistry` (and `global.appTag`) to `$REG`.

## 2. Install

```sh
kubectl create namespace srs
helm install srs deploy/helm -n srs \
  --set global.appRegistry=$REG

kubectl -n srs get pods -w
```

The Debezium connector registers automatically (a post-install Job). The `documents`
table is seeded by the Postgres init script.

## 3. Use a real LLM (optional)

Default LLM is `extractive` (keyless). For a real model via LiteLLM:

**Local, keyless (Ollama):**
```sh
helm upgrade srs deploy/helm -n srs --reuse-values \
  --set litellm.enabled=true --set ollama.enabled=true \
  --set query.llmProvider=litellm --set query.llmModel=local \
  --set query.requestTimeout=180s --set query.faithfulnessMode=llm
```

**Cloud (e.g. Gemini):**
```sh
helm upgrade srs deploy/helm -n srs --reuse-values \
  --set litellm.enabled=true \
  --set query.llmProvider=litellm --set query.llmModel=gemini-flash \
  --set-string secrets.geminiApiKey=$GEMINI_API_KEY
```

## 4. Reach it

```sh
# ingress (add hosts to /etc/hosts -> node IP), or port-forward:
kubectl -n srs port-forward svc/query 8080:8080
curl -X POST localhost:8080/ask -H 'content-type: application/json' \
  -d '{"query":"How does Debezium capture changes from PostgreSQL?"}'
```

## Toggles (values.yaml)

| Key | Default | Notes |
|---|---|---|
| `observability.enabled` | true | Prometheus + Tempo + Grafana |
| `litellm.enabled` | false | LiteLLM proxy (needed for real LLM) |
| `ollama.enabled` | false | local model; pulls `ollama.model` |
| `tei.enabled` | false | TEI embeddings/rerank (amd64 only) |
| `mlservice.embedBackend` | hash | set `auto` + the full ML image for real embeddings |
| `indexer/query.hpa.enabled` | true | CPU-based HPA (needs metrics-server) |
| `query.apiKey` | "" | set to enforce auth + tenant isolation |

## K3s notes / caveats

- **Resources:** OpenSearch + Redpanda + Qdrant (+ Ollama) are heavy; a single node needs
  ~16GB RAM. Tune `*.resources` for smaller nodes.
- **metrics-server:** HPAs need it (K3s bundles it; ensure it's running).
- **OpenSearch** sets `vm.max_map_count=262144` via a privileged initContainer.
- **Storage:** PVCs use `local-path` (K3s default). Multi-node clusters need a shared/
  proper StorageClass (these are single-instance Deployments with RWO PVCs, not HA).
- **Not production-HA:** single replicas for stateful infra, `Recreate` strategy. For
  production, swap infra for their official/Bitnami charts (StatefulSets, replicas, backups).
- **arm64:** `tei.enabled` won't schedule (amd64 nodeSelector) — use mlservice/Ollama.

## Validate before applying

```sh
helm lint deploy/helm
helm template srs deploy/helm | kubectl apply --dry-run=client -f -
```
