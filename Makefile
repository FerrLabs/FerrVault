# Basic developer tasks for ferrflow-operator.
#
# This is intentionally minimal — kubebuilder-generated Makefiles pull in a
# large zoo of helper binaries we don't need yet. We'll add codegen, envtest,
# and release wiring as the project grows.

SHELL := /usr/bin/env bash
IMG ?= ghcr.io/ferrflow-org/ferrflow-operator:dev

.PHONY: help
help:
	@echo "Targets:"
	@echo "  fmt              Run gofmt on all Go files."
	@echo "  lint             Run golangci-lint."
	@echo "  test             Run unit tests."
	@echo "  build            Build the manager binary into bin/manager."
	@echo "  docker-build     Build the container image as \$$IMG (default: $(IMG))."
	@echo "  install-crds     Apply CRDs to the current kubectl context."
	@echo "  uninstall-crds   Remove CRDs from the current kubectl context."
	@echo "  deploy-rbac      Apply the operator ServiceAccount, Role, Binding."
	@echo "  run              Run the manager against the current kubectl context."

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

.PHONY: install-crds
install-crds:
	kubectl apply -f config/crd/bases/

.PHONY: uninstall-crds
uninstall-crds:
	kubectl delete --ignore-not-found -f config/crd/bases/

.PHONY: deploy-rbac
deploy-rbac:
	kubectl apply -f config/rbac/

.PHONY: run
run: build
	./bin/manager --leader-elect=false
