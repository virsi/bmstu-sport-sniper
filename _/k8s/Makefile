# Helpers для k8s-деплоя fizcultor.
# Usage: make -C _/k8s <target>
#
# Все команды используют kubeconfig из переменной KUBECONFIG (по дефолту
# k8s.cluster.yaml в корне проекта — он gitignored).

REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || cd ../.. && pwd)
KUBECONFIG ?= $(REPO_ROOT)/k8s.cluster.yaml
NS := fizcultor

export KUBECONFIG

.PHONY: help build apply diff sync-dashboards create-secrets

help:
	@echo "Targets:"
	@echo "  sync-dashboards  - копия backend/deploy/grafana/dashboards/*.json → base/observability/dashboards/"
	@echo "  build            - kubectl kustomize overlays/prod (печать)"
	@echo "  diff             - kubectl diff -k overlays/prod"
	@echo "  apply            - kubectl apply -k overlays/prod"
	@echo "  create-secrets   - kubectl create secret из локального .env (interactive)"

sync-dashboards:
	@mkdir -p base/observability/dashboards
	@cp $(REPO_ROOT)/backend/deploy/grafana/dashboards/*.json base/observability/dashboards/
	@echo "dashboards synced"

build: sync-dashboards
	kubectl kustomize overlays/prod

diff: sync-dashboards
	kubectl diff -k overlays/prod || true

apply: sync-dashboards
	kubectl apply -k overlays/prod

# Создание Secret из локального backend/.env. Парсит KEY=VALUE строки и
# выставляет ключи fizcultor-secrets. Не коммитится и не логирует значения.
create-secrets:
	@if [ ! -f "$(REPO_ROOT)/backend/.env" ]; then \
		echo "backend/.env не найден"; exit 1; \
	fi
	@kubectl -n $(NS) create secret generic fizcultor-secrets \
		--dry-run=client -o yaml \
		--from-env-file="$(REPO_ROOT)/backend/.env" \
		| kubectl apply -f -
	@echo "secret applied"
