# ferrflow-operator

[![CI](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/ci.yml/badge.svg)](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/ci.yml)
[![Release](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/release.yml/badge.svg)](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/FerrFlow-Org/FerrFlow-Operator)](https://github.com/FerrFlow-Org/FerrFlow-Operator/releases/latest)
[![Coverage](https://codecov.io/gh/FerrFlow-Org/FerrFlow-Operator/graph/badge.svg)](https://codecov.io/gh/FerrFlow-Org/FerrFlow-Operator)
[![License](https://img.shields.io/github/license/FerrFlow-Org/FerrFlow-Operator)](LICENSE)

Kubernetes operator that syncs secrets stored in [FerrFlow](https://ferrflow.com) into native Kubernetes `Secret` resources, with optional rolling restart of the workloads that consume them.

> **Status: pre-alpha / scaffolding.** No reconciler is implemented yet. The design is tracked in [issue #1](https://github.com/FerrFlow-Org/FerrFlow-Operator/issues/1).

## What it will do

Given a FerrFlow project + environment, the operator watches a `FerrFlowSecret` custom resource and keeps a matching Kubernetes `Secret` in sync:

```yaml
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowConnection
metadata:
  name: prod
spec:
  url: https://ferrflow.example.com
  tokenSecretRef:
    name: ferrflow-api-token
    key: token
---
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowSecret
metadata:
  name: web-env
spec:
  connectionRef: { name: prod }
  project: web
  environment: production
  selector:
    names: [DATABASE_URL, STRIPE_KEY]
  target:
    name: web-env
  refreshInterval: 1h
  rolloutRestart:
    - { kind: Deployment, name: web }
```

On each reconciliation the operator fetches the listed secrets from the FerrFlow API, writes them to the target `Secret`, and — if `rolloutRestart` is set — annotates the named workloads to trigger a rolling update.

## Prerequisites in FerrFlow

The operator has no value until the following ship in [FerrFlow-Org/Application](https://github.com/FerrFlow-Org/Application):

- FerrFlow Secrets feature (project-scoped, AES-GCM at rest)
- Long-lived API tokens with per-project scopes
- A `reveal` endpoint exposing decrypted secret values, rate-limited and audit-logged

See issue #1 for the full prerequisite list and phased plan.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Code of conduct in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Vulnerability reports via [SECURITY.md](SECURITY.md).

## License

[MPL-2.0](LICENSE)
