# ferrflow-operator

[![CI](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/ci.yml/badge.svg)](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/ci.yml)
[![Release](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/release.yml/badge.svg)](https://github.com/FerrFlow-Org/FerrFlow-Operator/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/FerrFlow-Org/FerrFlow-Operator)](https://github.com/FerrFlow-Org/FerrFlow-Operator/releases/latest)
[![Coverage](https://codecov.io/gh/FerrFlow-Org/FerrFlow-Operator/graph/badge.svg)](https://codecov.io/gh/FerrFlow-Org/FerrFlow-Operator)
[![License](https://img.shields.io/github/license/FerrFlow-Org/FerrFlow-Operator)](LICENSE)

Kubernetes operator that syncs secrets stored in [FerrFlow](https://ferrflow.com) into native Kubernetes `Secret` resources.

> **Status: alpha.** The MVP reconciler is in place — it reads secrets from a FerrFlow vault via the bulk-reveal API and materialises them into a Kubernetes Secret, with owner-ref GC and status conditions. Rolling restarts, Helm chart, and integration tests are tracked in [issue #1](https://github.com/FerrFlow-Org/FerrFlow-Operator/issues/1).

## Custom resources

Two CRDs under `ferrflow.io/v1alpha1`:

### `FerrFlowConnection` (shortname `ffc`)

Declares how to reach a FerrFlow instance. One per (namespace, org). Shared by every `FerrFlowSecret` in that namespace that targets the same organization.

```yaml
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowConnection
metadata:
  name: prod
spec:
  url: https://ferrflow.example.com
  organization: acme
  tokenSecretRef:
    name: ferrflow-api-token
    key: token
```

The referenced Secret must hold a FerrFlow API token (`fft_...`) with at least the `secrets:read` scope.

### `FerrFlowSecret` (shortname `ffs`)

Declares a sync from a vault to a Kubernetes Secret.

```yaml
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowSecret
metadata:
  name: web-env
spec:
  connectionRef: { name: prod }
  project: web
  vault: production          # FerrFlow vault name (often the environment)
  selector:
    names: [DATABASE_URL, STRIPE_KEY]   # omit to sync every key in the vault
  target:
    name: web-env            # target Secret name; defaults to metadata.name
    type: Opaque
  refreshInterval: 30m       # Go time.Duration; 0s disables scheduled refresh
```

On reconciliation the operator calls `GET /api/v1/orgs/:org/projects/:project/vaults/by-name/:vault/secrets/reveal` once, writes the returned `{name: value}` map into `spec.target.name`, and sets the CR's `Ready` condition based on whether any requested keys were missing upstream.

The generated Secret is owned by the CR — deleting the CR garbage-collects the Secret.

## Running

### Helm (recommended)

```bash
helm install ferrflow-operator oci://ghcr.io/ferrflow-org/charts/ferrflow-operator \
  --namespace ferrflow-operator-system --create-namespace
```

Upgrade: `helm upgrade` against the same release. CRDs carry `helm.sh/resource-policy: keep` so they survive uninstall (protects your CRs + managed Secrets). See [`charts/ferrflow-operator/README.md`](charts/ferrflow-operator/README.md) for the full `values.yaml` reference.

### Locally against a cluster

```bash
make install-crds   # CRDs only
make run            # runs the manager as your user, not as a Pod
```

### Raw manifests (without Helm)

```bash
kubectl apply -f config/crd/bases/
kubectl create namespace ferrflow-operator-system
kubectl apply -f config/rbac/
# You still need a Deployment — render one from the chart:
#   helm template ferrflow-operator charts/ferrflow-operator > manager.yaml
```

## Prerequisites in FerrFlow

The operator relies on endpoints in [`FerrFlow-Org/Application`](https://github.com/FerrFlow-Org/Application) that shipped in `api@v4.0.0`:

- API token auth (`Authorization: Bearer fft_...`) with granular scopes — #268
- `secrets:read` scope enforcement on all secrets routes — #268
- Bulk reveal endpoint — #277

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Code of conduct in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Vulnerability reports via [SECURITY.md](SECURITY.md).

## License

[MPL-2.0](LICENSE)
