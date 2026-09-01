SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

SERVICE := appstore
VERSION ?= dev
GO_TEST_PACKAGES := . ./cmd/... ./internal/... ./migrations/... ./openapi/...
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/hkjang/appstore/internal/buildinfo.Version=$(VERSION) \
	-X github.com/hkjang/appstore/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/hkjang/appstore/internal/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: help web-install web-build embed-web build run test test-go test-web test-e2e \
	screenshots check check-env check-offline check-docs image archive \
	verify-archive clean

help: ## Show available targets.
	@awk 'BEGIN {FS = ":.*## "; printf "AppStore targets:\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

web-install: ## Install the locked frontend dependencies.
	cd web && npm ci --no-audit --no-fund

web-build: ## Build the React production bundle.
	cd web && npm run build

embed-web: web-build ## Copy the React bundle into the Go embed directory.
	./scripts/embed-web.sh

build: embed-web ## Build the single Go service binary with version metadata.
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o appstore ./cmd/server

run: build ## Run the locally built service (requires the four environment variables).
	./appstore

test: test-go test-web ## Run backend and frontend unit tests.

test-go: ## Run Go tests.
	go test $(GO_TEST_PACKAGES)

test-web: ## Run React unit tests.
	cd web && npm test

test-e2e: ## Run Playwright desktop and mobile tests.
	cd web && npm run test:e2e

screenshots: web-build ## Capture every route and publish full-page WebP docs (VERSION=vX.Y.Z).
	cd web && npm run test:e2e
	node ./scripts/publish-doc-screenshots.mjs web/test-results '$(VERSION)'
	./scripts/check-docs.sh

check: check-env check-offline check-docs ## Validate configuration and offline documentation contracts.

check-env: ## Confirm that application code reads only the four approved env variables.
	./scripts/check-env-contract.sh

check-offline: web-build ## Reject known runtime CDN references in the frontend bundle.
	./scripts/check-offline-assets.sh web/dist

check-docs: ## Validate the static GitHub Pages site and screenshot manifest.
	./scripts/check-docs.sh

image: ## Build appstore:vX.Y.Z (usage: make image VERSION=v2.0.0).
	./scripts/release-image.sh --image-only '$(VERSION)'

archive: ## Build and save appstore-vX.Y.Z.tar.gz under artifacts/.
	./scripts/release-image.sh '$(VERSION)'

verify-archive: ## Validate/load an archive (usage: make verify-archive VERSION=v2.0.0).
	./scripts/verify-release-archive.sh '$(VERSION)' 'artifacts/$(SERVICE)-$(VERSION).tar.gz'

clean: ## Remove generated local build output.
	rm -f ./appstore
	rm -rf ./web/dist ./web/coverage ./artifacts ./playwright-report ./test-results
