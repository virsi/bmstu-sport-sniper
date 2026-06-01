.PHONY: help all gen proto sqlc build test test-race test-integration coverage lint fmt tidy clean up down logs migrate-up

SHELL := /bin/bash
SERVICES := $(notdir $(wildcard services/*))
# Per-service coverage floor (Wave 4 baseline; raise as suites grow).
# The default is intentionally permissive — cmd/server and internal/config
# carry 0% by design (no logic worth testing in main.go and env structs).
COVERAGE_THRESHOLD ?= 40

help:
	@echo "Targets:"
	@echo "  build              - go build всех сервисов"
	@echo "  test               - go test ./... во всех модулях (unit, без -tags)"
	@echo "  test-race          - go test -race -count=1"
	@echo "  test-integration   - integration-тесты с testcontainers (требуется Docker)"
	@echo "  coverage           - per-svc coverage отчёт, проверка порога $(COVERAGE_THRESHOLD)%"
	@echo "  lint               - golangci-lint run (требуется golangci-lint в PATH)"
	@echo "  fmt                - gofumpt -w . (или gofmt -s -w .)"
	@echo "  tidy               - go mod tidy во всех модулях"
	@echo "  gen                - protoc (buf) + sqlc"
	@echo "  proto              - buf generate"
	@echo "  sqlc               - sqlc generate"
	@echo "  up                 - docker compose up -d"
	@echo "  down               - docker compose down"
	@echo "  logs               - docker compose logs -f"
	@echo "  migrate-up         - применить миграции goose (нужен goose в PATH)"
	@echo "  clean              - удалить bin/, gen/, coverage.out"

# ---------- build / test ----------

build:
	@for svc in $(SERVICES); do \
		echo "=== build $$svc ==="; \
		(cd services/$$svc && go build -o /dev/null ./...) || exit 1; \
	done
	@echo "=== build pkg ==="
	@(cd pkg && go build ./...)

test:
	@(cd pkg && go test ./...)
	@for svc in $(SERVICES); do \
		echo "=== test $$svc ==="; \
		(cd services/$$svc && go test ./...) || exit 1; \
	done

test-race:
	@(cd pkg && go test -race -count=1 ./...)
	@for svc in $(SERVICES); do \
		echo "=== test-race $$svc ==="; \
		(cd services/$$svc && go test -race -count=1 ./...) || exit 1; \
	done

# Integration tests live next to each service under integration_test/ and
# require the `integration` build tag (testcontainers spin up real Postgres
# / NATS via Docker). Skip pkg/ (no integration).
test-integration:
	@if ! docker info >/dev/null 2>&1; then \
		echo "Docker is not running. Start Docker and retry."; exit 1; \
	fi
	@for svc in auth-svc bmstu-svc filter-svc notifier-svc teachers-svc; do \
		if [ -d services/$$svc/integration_test ]; then \
			echo "=== test-integration $$svc ==="; \
			(cd services/$$svc && go test -tags integration -count=1 -timeout 120s -v ./integration_test/...) || exit 1; \
		fi; \
	done

# Per-service coverage report + threshold gate. Writes coverage.out alongside
# each service's go.mod and the aggregated HTML at backend/coverage.html.
# pkg is built and tested but excluded from the gate — it is mostly type
# declarations and small adapters with no meaningful business logic.
coverage:
	@mkdir -p coverage
	@(cd pkg && go test -coverprofile=../coverage/pkg.out ./...) || exit 1
	@for svc in $(SERVICES); do \
		echo "=== coverage $$svc ==="; \
		(cd services/$$svc && go test -coverprofile=../../coverage/$$svc.out ./...) || exit 1; \
	done
	@echo "--- per-service totals (threshold $(COVERAGE_THRESHOLD)%, pkg excluded) ---"
	@fail=0; \
	for f in coverage/*.out; do \
		name=$$(basename $$f .out); \
		pct=$$(go tool cover -func=$$f | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
		printf "%-30s %s%%" "$$name" "$$pct"; \
		if [ "$$name" = "pkg" ]; then \
			printf "  (excluded from gate)\n"; \
		elif awk -v p=$$pct -v t=$(COVERAGE_THRESHOLD) 'BEGIN { exit (p+0 < t+0) }'; then \
			printf "\n"; \
		else \
			printf "  -> below threshold $(COVERAGE_THRESHOLD)%%\n"; \
			fail=1; \
		fi; \
	done; \
	exit $$fail

# ---------- lint / fmt ----------

lint:
	@if ! command -v golangci-lint >/dev/null; then \
		echo "golangci-lint not found; install: https://golangci-lint.run/welcome/install/"; exit 1; \
	fi
	@(cd pkg && golangci-lint run ./...)
	@for svc in $(SERVICES); do \
		echo "=== lint $$svc ==="; \
		(cd services/$$svc && golangci-lint run ./...) || exit 1; \
	done

fmt:
	@if command -v gofumpt >/dev/null; then \
		gofumpt -w .; \
	else \
		gofmt -s -w .; \
	fi

tidy:
	@(cd pkg && go mod tidy)
	@for svc in $(SERVICES); do \
		echo "=== tidy $$svc ==="; \
		(cd services/$$svc && go mod tidy) || exit 1; \
	done
	@(cd tests/testhelpers && go mod tidy)
	@go work sync

# ---------- code generation ----------

gen: proto sqlc

proto:
	@if ! command -v buf >/dev/null; then \
		echo "buf not found; install: https://buf.build/docs/installation"; exit 1; \
	fi
	buf generate

sqlc:
	@if ! command -v sqlc >/dev/null; then \
		echo "sqlc not found; install: https://docs.sqlc.dev/en/latest/overview/install.html"; exit 1; \
	fi
	sqlc generate

# ---------- infra ----------

up:
	cd deploy && docker compose up -d --build

down:
	cd deploy && docker compose down

logs:
	cd deploy && docker compose logs -f --tail=100

# ---------- migrations ----------

# Применить goose-миграции для всех БД. Требует goose CLI и POSTGRES_DSN_BASE
# (без имени БД), например: POSTGRES_DSN_BASE=postgres://user:pass@localhost:5432
migrate-up:
	@if ! command -v goose >/dev/null; then \
		echo "goose not found; install: go install github.com/pressly/goose/v3/cmd/goose@latest"; exit 1; \
	fi
	@for db in auth_db bmstu_db filter_db teachers_db; do \
		echo "=== migrate $$db ==="; \
		goose -dir migrations/$$db postgres "$(POSTGRES_DSN_BASE)/$$db?sslmode=disable" up; \
	done

# ---------- misc ----------

clean:
	rm -rf bin/ gen/ coverage.out coverage.html coverage/
