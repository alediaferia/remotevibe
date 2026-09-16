BINARY  ?= bin/remotevibed
IMAGE   ?= remotevibe/agent:latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Its own docker config, not ~/.docker/config.json, so the GHCR credential
# here doesn't leak into (or get clobbered by) whatever else this host runs.
export DOCKER_CONFIG ?= /opt/remotevibe/.docker

.PHONY: help build image run check auth verify fmt vet test clean install up pull login down logs

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the daemon
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/remotevibed

image: ## Build the session container image
	docker build -t $(IMAGE) image

up: ## Build and start the daemon with docker compose (local dev)
	docker compose --profile build build agent
	docker compose up -d --build

login: ## Log in to GHCR (prompts for username/PAT; needed once before `make pull`)
	@mkdir -p "$(DOCKER_CONFIG)"
	docker login ghcr.io

pull: ## Pull CI-built images and start the daemon (production deploy)
	@mkdir -p "$(DOCKER_CONFIG)"
	docker compose pull
	set -a; . ./.env; set +a; docker pull "$$RV_AGENT_IMAGE"
	docker compose up -d

down: ## Stop the daemon (running sessions are untouched)
	docker compose down

logs: ## Follow the daemon log
	docker compose logs -f remotevibed

run: build ## Run the daemon locally with .env loaded
	set -a; . ./.env; set +a; $(BINARY)

check: build ## Validate configuration and environment
	set -a; . ./.env; set +a; $(BINARY) -check

auth: ## Bootstrap agent credentials (one-time, interactive)
	./scripts/bootstrap-auth.sh

verify: ## Smoke-test that Remote Control works from a container
	./scripts/verify-remote-control.sh

fmt: ## Format Go sources
	gofmt -l -w ./cmd ./internal ./web

vet: ## Static checks
	go vet ./...

test: ## Run tests
	go test ./...

install: build ## Install the daemon to /usr/local/bin (needs sudo)
	install -m 0755 $(BINARY) /usr/local/bin/remotevibed

clean:
	rm -rf bin
