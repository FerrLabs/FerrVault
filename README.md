# ferrflow-operator

[![CI](https://github.com/FerrLabs/FerrFlow-Operator/actions/workflows/ci.yml/badge.svg)](https://github.com/FerrLabs/FerrFlow-Operator/actions/workflows/ci.yml)
[![Release](https://github.com/FerrLabs/FerrFlow-Operator/actions/workflows/release.yml/badge.svg)](https://github.com/FerrLabs/FerrFlow-Operator/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/FerrLabs/FerrFlow-Operator)](https://github.com/FerrLabs/FerrFlow-Operator/releases/latest)
[![Coverage](https://codecov.io/gh/FerrLabs/FerrFlow-Operator/graph/badge.svg)](https://codecov.io/gh/FerrLabs/FerrFlow-Operator)
[![License](https://img.shields.io/github/license/FerrLabs/FerrFlow-Operator)](LICENSE)

Kubernetes operator that syncs secrets stored in [FerrFlow](https://ferrflow.com) into native Kubernetes `Secret` resources.

> **Status: alpha.** The MVP reconciler is in place — it reads secrets from a FerrFlow vault via the bulk-reveal API and materialises them into a Kubernetes Secret, with owner-ref GC and status conditions. Rolling restarts, Helm chart, and integration tests are tracked in [issue #1](https://github.com/FerrLabs/FerrFlow-Operator/issues/1).

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

#### Value transforms

Revealed values can be reshaped before they land in the target Secret via `spec.transforms`. Transforms are applied in order; each one sees the output of the previous step.

```yaml
spec:
  connectionRef: { name: prod }
  project: web
  vault: production
  selector:
    names: [DATABASE_URL, STRIPE_KEY, CONFIG_JSON]
  transforms:
    - type: rename
      from: DATABASE_URL
      to: DB_URL
    - type: base64Decode
      keys: [STRIPE_KEY]          # omit `keys` to decode every value
    - type: jsonExpand
      key: CONFIG_JSON            # {"db":{"host":"pg"}} → CONFIG_JSON_DB_HOST=pg
    - type: prefix
      value: APP_                 # stamps APP_ on every remaining key
```

Supported types:

| `type`         | Fields              | Effect                                                       |
| -------------- | ------------------- | ------------------------------------------------------------ |
| `prefix`       | `value`             | Prepends `value` to every key.                               |
| `suffix`       | `value`             | Appends `value` to every key.                                |
| `rename`       | `from`, `to`        | Projects one key. Missing `from` is a no-op; collisions fail.|
| `base64Decode` | `keys` (optional)   | Decodes listed keys (or all when empty) from base64.         |
| `jsonExpand`   | `key`               | Flattens a JSON object under `<KEY>_<SUB>`. Drops the source.|

Malformed transforms (unknown type, invalid base64, non-object JSON, destination-key collisions) leave the CR in `Ready=False` with `Reason=TransformError` and increment `ferrflow_secret_sync_errors_total{reason="TransformError"}`. The target Secret is not written on failure — workloads keep the last known-good value.

## Running

### Helm (recommended)

```bash
helm install ferrflow-operator oci://ghcr.io/ferrlabs/charts/ferrflow-operator \
  --namespace ferrflow-operator-system --create-namespace
```

Upgrade: `helm upgrade` against the same release. CRDs carry `helm.sh/resource-policy: keep` so they survive uninstall (protects your CRs + managed Secrets). See [`charts/ferrflow-operator/README.md`](charts/ferrflow-operator/README.md) for the full `values.yaml` reference.

### Locally against a cluster

```bash
make install-crds   # CRDs only
make run            # runs the manager as your user, not as a Pod
```

### Raw manifests (without Helm at runtime)

The Helm chart is the single source of truth for all manifests (CRDs, RBAC,
ServiceAccount, Deployment). If your cluster policy forbids running Helm at
deploy time, render once and commit/apply the plain YAML:

```bash
kubectl create namespace ferrflow-operator-system
helm template ferrflow-operator charts/ferrflow-operator \
  --namespace ferrflow-operator-system \
  > manager.yaml
kubectl apply -f manager.yaml
```

No duplicate `config/rbac/` or `config/crd/` lives in the repo — anything
rendered from the chart *is* the canonical version.

## Prerequisites in FerrFlow

The operator relies on endpoints in [`FerrLabs/Application`](https://github.com/FerrLabs/Application) that shipped in `api@v4.0.0`:

- API token auth (`Authorization: Bearer fft_...`) with granular scopes — #268
- `secrets:read` scope enforcement on all secrets routes — #268
- Bulk reveal endpoint — #277

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Code of conduct in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Vulnerability reports via [SECURITY.md](SECURITY.md).

## License

[MPL-2.0](LICENSE)
