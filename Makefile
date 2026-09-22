SHELL := /bin/bash
COMPOSE := docker compose

.PHONY: help up down logs ps register seed demo test test-go test-py e2e eval clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: ## Build + start the whole stack, register CDC connector, seed loads on first boot
	$(COMPOSE) up -d --build
	@echo "Waiting for services to become healthy..."
	@$(COMPOSE) ps
	@bash scripts/register-connector.sh
	@echo ""
	@echo "Stack is up. Query API: http://localhost:8080  Grafana: http://localhost:3000"
	@echo "Try:  make demo   |   make e2e   |   make eval"

down: ## Stop and remove the stack
	$(COMPOSE) down -v

logs: ## Tail logs
	$(COMPOSE) logs -f --tail=100

ps: ## Show container status
	$(COMPOSE) ps

register: ## Register the Debezium connector
	bash scripts/register-connector.sh

seed: ## Insert an extra document (generates a change event)
	bash scripts/seed.sh

demo: ## Human-friendly walkthrough
	bash scripts/demo.sh

test: test-go test-py ## Run all unit tests

test-go: ## Run Go unit tests in a container (no host Go needed)
	docker run --rm -v "$$PWD":/src -w /src golang:1.26-alpine go test ./...

test-py: ## Run Python unit tests in a container
	docker run --rm -v "$$PWD":/src -w /src/mlservice python:3.12-slim \
		sh -c "pip install -q pytest >/dev/null && python -m pytest test_ml.py -q"
	docker run --rm -v "$$PWD":/src -w /src/eval python:3.12-slim \
		sh -c "pip install -q pytest >/dev/null && python -m pytest test_eval.py -q"

e2e: ## End-to-end assertions against the running stack
	bash scripts/e2e.sh

eval: ## Retrieval ablation table + faithfulness against the running stack
	docker run --rm --network host -v "$$PWD":/src -w /src/eval python:3.12-slim python eval.py

loadtest-backfill: ## Bulk-onboard docs: make loadtest-backfill COUNT=1000000 RATE=5000
	bash scripts/loadtest.sh backfill $(or $(COUNT),1000000) $(or $(RATE),5000)

loadtest-stream: ## Sustained write stream: make loadtest-stream RATE=1000 DUR=2m
	bash scripts/loadtest.sh stream $(or $(RATE),1000) $(or $(DUR),2m) $(or $(URGENT),20) $(or $(UPDATE),30)

monitor: ## Live load signals (lag, freshness p99, index rate). Ctrl-C to stop.
	bash scripts/monitor.sh $(or $(INTERVAL),5)

scale-indexer: ## Scale indexer replicas: make scale-indexer N=4
	$(COMPOSE) up -d --scale indexer=$(or $(N),3) --no-recreate

clean: down ## Alias for down
