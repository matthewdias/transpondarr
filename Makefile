.DEFAULT_GOAL := build

BIN     := transpondarrd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/matthewdias/transpondarr/internal/version.Version=$(VERSION)
SQLC_VERSION := $(shell sed -n 's/^sqlc = "\([^"]*\)".*/\1/p' mise.toml)
GO_BUILD := CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/transpondarrd

.PHONY: build server web web-deps hooks gen gen-api lint go-lint web-lint typecheck test go-test web-test dev seed run migrate tidy notices clean

build: web ## Build frontend + server into ./$(BIN)
	$(GO_BUILD)

# Spelled out rather than `build: web server`, which under `make -j` would let
# the binary link against a stale web/dist.
server: ## Build the server alone (web/dist/.gitkeep satisfies the embed)
	$(GO_BUILD)

web: web-deps ## Build the frontend into web/dist (embedded by the binary)
	cd frontend && npm run build

# Install frontend deps only when the lockfile changes, so `make build` and
# `make lint` in the same CI run don't each pay for a full `npm ci`.
web-deps: frontend/node_modules hooks
frontend/node_modules: frontend/package-lock.json
	cd frontend && npm ci
	@touch frontend/node_modules

hooks: ## Point git at .githooks (fast pre-commit format check)
	@git config core.hooksPath .githooks 2>/dev/null || true

# sqlc's output is version-sensitive and committed, and CI diffs it — generating
# with an unpinned sqlc produces drift that looks like an unrelated failure.
gen: ## Regenerate the sqlc query layer
	@test -n "$(SQLC_VERSION)" || { echo "gen: no sqlc pin found in mise.toml" >&2; exit 1; }
	@have=$$(sqlc version 2>/dev/null); \
	if [ "$$have" != "v$(SQLC_VERSION)" ]; then \
		echo "gen: sqlc $${have:-not found} but mise.toml pins v$(SQLC_VERSION)." >&2; \
		echo "gen: run 'mise exec -- make gen', or install sqlc v$(SQLC_VERSION)." >&2; \
		exit 1; \
	fi
	sqlc generate

gen-api: ## Regenerate the frontend API types from the OpenAPI spec
	go run ./cmd/transpondarrd openapi > frontend/openapi.gen.yaml
	# npx (isolated) rather than a devDep: openapi-typescript@7 pins peer
	# typescript@^5, but the frontend runs typescript@6.
	cd frontend && npx --yes openapi-typescript@7.13.0 openapi.gen.yaml -o src/lib/api-types.ts
	rm -f frontend/openapi.gen.yaml

lint: go-lint web-lint ## Run linters (Go + frontend)

go-lint: ## Lint the Go tree (golangci-lint)
	golangci-lint run

web-lint: web-deps ## Lint the frontend (oxlint + prettier check)
	cd frontend && npm run lint
	cd frontend && npm run format:check

# web-deps first: with no node_modules, npm resolves tsc from PATH, so the check
# reports on whatever TypeScript is installed globally rather than the pinned one.
typecheck: web-deps ## Typecheck the frontend (tsc -b; vitest and the linters don't)
	cd frontend && npm run typecheck

test: go-test web-test ## Run tests (Go + frontend)

go-test: ## Run the Go tests (race detector)
	# -race: the job runner's status fields are written by each job's goroutine and
	# read by the HTTP handler, so a missing lock is invisible without the detector.
	# cgo because the race runtime needs it on Linux — macOS links it without, so
	# omitting this passes locally and fails only in CI. The binary stays CGO_ENABLED=0.
	CGO_ENABLED=1 go test -race ./...

web-test: web-deps ## Run the frontend tests (vitest)
	cd frontend && npm run test

dev: ## Run the API with live reload (air)
	air

seed: ## Seed a dev database and serve the AniList/Torznab stubs (see CONTRIBUTING.md)
	go run ./cmd/devseed

run: build ## Build then run the server
	./$(BIN)

migrate: ## Apply DB migrations directly (set TRANSPONDARR_DB)
	goose -dir internal/store/migrations sqlite3 "$(TRANSPONDARR_DB)" up

tidy: ## go mod tidy
	go mod tidy

notices: web-deps ## Regenerate THIRD-PARTY-NOTICES.md from shipped deps
	./scripts/gen-notices.sh

clean: ## Remove build artifacts
	rm -f $(BIN)
	rm -rf dist tmp
