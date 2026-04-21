# Contributing to ferrflow-operator

Thanks for your interest in contributing! Here's how to get started.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/<your-username>/ferrflow-operator.git`
3. Create a branch: `git checkout -b feat/my-feature`
4. Make your changes
5. Push and open a pull request

## Development Setup

### Prerequisites

- [Go](https://go.dev/dl/) 1.23+
- [Kubebuilder](https://book.kubebuilder.io/quick-start.html) v4
- [kubectl](https://kubernetes.io/docs/tasks/tools/) and a Kubernetes cluster (kind, k3d, or minikube work)
- [Docker](https://www.docker.com/) for building container images

### Build and Test

```bash
# Lint and format
make lint
make fmt

# Unit tests
make test

# Integration tests (envtest)
make test-integration

# Build the operator binary
make build

# Build the container image
make docker-build
```

### Running locally against a cluster

```bash
# Install CRDs into the current-context cluster
make install

# Run the operator against the current-context cluster (from your terminal)
make run
```

## Guidelines

### Branches

Use conventional prefixes: `feat/`, `fix/`, `refactor/`, `docs/`, `chore/`, `test/`.

One branch per topic. Don't mix unrelated changes.

### Commits

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add FerrFlowSecret reconciler
fix(api): handle 404 from FerrFlow reveal endpoint
docs: update Helm chart installation steps
```

- Single line, no body
- Breaking changes: add `!` after type/scope (e.g. `feat(crd)!: rename spec.project to spec.projectName`)

### Pull Requests

- Every PR must reference a GitHub issue. If none exists, create one first.
- PR titles follow the same Conventional Commits format (squash merge uses the title).
- Keep PRs focused. One feature or fix per PR.

### Code Style

- `gofmt` / `goimports` with no diff
- `golangci-lint run` with no warnings
- Write tests for new reconciler logic, API client code, and CRD validation

## Reporting Bugs

Use the [bug report template](https://github.com/FerrLabs/FerrFlow-Operator/issues/new?template=bug_report.md).

## Requesting Features

Use the [feature request template](https://github.com/FerrLabs/FerrFlow-Operator/issues/new?template=feature_request.md).

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

By contributing, you agree that your contributions will be licensed under the [MPL-2.0 License](LICENSE).
