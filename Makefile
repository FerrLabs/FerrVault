# Basic developer tasks for ferrvault-operator.
#
# This is intentionally minimal — kubebuilder-generated Makefiles pull in a
# large zoo of helper binaries we don't need yet. We'll add codegen, envtest,
# and release wiring as the project grows.

SHELL := /usr/bin/env bash
IMG ?= ghcr.io/ferrlabs/ferrvault-operator:dev
CHART_DIR := charts/ferrvault-operator

.PHONY: help
help:
	@echo "Targets:"
	@echo "  fmt              Run gofmt on all Go files."
	@echo "  lint             Run golangci-lint."
	@echo "  test             Run unit tests."
	@echo "  build            Build the manager binary into bin/manager."
	@echo "  docker-build     Build the container image as \$$IMG (default: $(IMG))."
	@echo "  install-crds     Apply CRDs (rendered from the Helm chart) to the current context."
	@echo "  uninstall-crds   Remove CRDs from the current kubectl context."
	@echo "  run              Run the manager against the current kubectl context."
	@echo "  helm-lint        Run 'helm lint' on charts/ferrvault-operator."
	@echo "  helm-template    Render the chart and print to stdout (sanity check)."
	@echo "  helm-package     Package the chart into dist/."
	@echo "  helm-install     helm upgrade --install against the current context."

.PHONY: fmt
fmt:
	gofmt -l -w .

.PHONY: lint
lint:
	golangci-lint run

.PHONY: test
test:
	go test -race -coverprofile=coverage.out ./...

.PHONY: build
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/manager ./cmd

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

# CRDs are owned by the Helm chart (the only source of truth). For a
# `make run`-style local dev loop we still need them applied — render them
# from the chart and pipe into kubectl instead of maintaining a duplicate set
# under config/.
.PHONY: install-crds
install-crds:
	helm template ferrvault-operator $(CHART_DIR) --show-only templates/crd-*.yaml | kubectl apply -f -

.PHONY: uninstall-crds
uninstall-crds:
	helm template ferrvault-operator $(CHART_DIR) --show-only templates/crd-*.yaml | kubectl delete --ignore-not-found -f -

.PHONY: run
run: build
	./bin/manager --leader-elect=false

# --- Helm ---

.PHONY: helm-lint
helm-lint:
	helm lint $(CHART_DIR)

.PHONY: helm-template
helm-template:
	helm template ferrvault-operator $(CHART_DIR) --debug

.PHONY: helm-package
helm-package:
	mkdir -p dist
	helm package $(CHART_DIR) -d dist/

.PHONY: helm-install
helm-install:
	helm upgrade --install ferrvault-operator $(CHART_DIR) \
		--namespace ferrvault-operator-system --create-namespace
