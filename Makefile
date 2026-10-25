SHELL := /bin/bash
GO ?= go
COMPOSE := docker compose -f deploy/compose.yaml
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REVISION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
TEST_PG := aos-test-pg
TEST_PG_PORT := 55499
TEST_DB_URL := postgres://postgres:test@127.0.0.1:$(TEST_PG_PORT)/postgres?sslmode=disable

.PHONY: test test-integration lint web-build web-test build docker up down backup restore ops-test

## test : tests Go unitaires (les tests PostgreSQL sont ignorés sans AOS_TEST_DATABASE_URL)
test:
	$(GO) test ./...

## test-integration : tests Go complets contre un PostgreSQL jetable (Docker)
test-integration:
	@docker rm -f $(TEST_PG) >/dev/null 2>&1 || true
	docker run -d --rm --name $(TEST_PG) -e POSTGRES_PASSWORD=test -p 127.0.0.1:$(TEST_PG_PORT):5432 postgres:16-alpine >/dev/null
	@for i in $$(seq 1 60); do docker exec $(TEST_PG) pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
	AOS_TEST_DATABASE_URL='$(TEST_DB_URL)' $(GO) test -count=1 ./... ; status=$$?; docker rm -f $(TEST_PG) >/dev/null; exit $$status

## lint : go vet + gofmt
lint:
	$(GO) vet ./...
	@out=$$(gofmt -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './web/node_modules/*')); \
	  if [ -n "$$out" ]; then echo "fichiers non formatés :"; echo "$$out"; exit 1; fi

web-build:
	cd web && npm ci && npm run build

web-test:
	cd web && npm ci && npm test

## build : binaire local (embarque web/dist tel quel)
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.revision=$(REVISION)" -o bin/agentic-os ./cmd/agentic-os

docker:
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) -t agentic-os:dev .

up:
	AOS_VERSION=$(VERSION) AOS_REVISION=$(REVISION) $(COMPOSE) up -d --build

down:
	$(COMPOSE) down

## backup : sauvegarde vérifiée (0600, SHA-256, rétention) — voir docs/technique/sauvegarde-restauration.md
backup:
	deploy/ops/backup.sh manual

## restore : make restore FILE=<archive.dump> CONFIRM=yes (destructif ; sauvegarde pre-restore automatique)
restore:
	@test -n "$(FILE)" && test "$(CONFIRM)" = yes || { echo "usage : make restore FILE=<archive.dump> CONFIRM=yes"; exit 2; }
	deploy/ops/restore.sh "$(FILE)" --yes

## ops-test : test des scripts backup/restore/upgrade/rollback contre un PostgreSQL jetable
ops-test:
	deploy/ops/test-ops.sh
