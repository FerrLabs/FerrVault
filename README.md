<div align="center">

# FerrVault Operator

**Secrets from FerrVault, as native Kubernetes Secrets.**

Your workloads read a normal `Secret`. The operator keeps it in sync with the vault,<br />
garbage-collects it with the CR, and reports drift through status conditions.

[![Latest release](https://img.shields.io/github/v/release/FerrLabs/FerrVault)](https://github.com/FerrLabs/FerrVault/releases/latest)
[![Quality Gate](https://sonar.ferrlabs.com/api/project_badges/measure?project=FerrVault&metric=alert_status&token=sqb_d3cde9fc7a86f70e3652d22ce13b5a39522212c8)](https://sonar.ferrlabs.com/dashboard?id=FerrVault)
[![Coverage](https://sonar.ferrlabs.com/api/project_badges/measure?project=FerrVault&metric=coverage&token=sqb_d3cde9fc7a86f70e3652d22ce13b5a39522212c8)](https://sonar.ferrlabs.com/dashboard?id=FerrVault)
[![License](https://img.shields.io/github/license/FerrLabs/FerrVault)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/FerrLabs/FerrVault/badge)](https://scorecard.dev/viewer/?uri=github.com/FerrLabs/FerrVault)

[ferrvault.com](https://ferrvault.com) | [FerrVault-Cloud](https://github.com/FerrLabs/FerrVault-Cloud) | [FerrLabs](https://ferrlabs.com)

</div>

> [!WARNING]
> **Alpha.** The reconciler reads secrets from a vault through the bulk-reveal API and
> materialises them into a Kubernetes Secret, with owner-ref GC and status conditions. Rolling
> restarts, the Helm chart and integration tests are tracked in
> [issue #1](https://github.com/FerrLabs/FerrVault/issues/1).

## Custom resources

Two CRDs under `ferrvault.com/v1alpha1`:

### `FerrVaultConnection` (shortname `fvc`)

Declares how to reach a FerrVault instance. One per (namespace, org). Shared by every `FerrVaultSecret` in that namespace that targets the same organization.

```yaml
apiVersion: ferrvault.com/v1alpha1
kind: FerrVaultConnection
metadata:
  name: prod
spec:
  url: https://ferrvault.example.com
  organization: acme
  tokenSecretRef:
    name: ferrvault-api-token
    key: token
```

The referenced Secret must hold a FerrVault API token (`fft_...`) with at least the `secrets:read` scope.

### `FerrVaultSecret` (shortname `fvs`)

Declares a sync from a vault to a Kubernetes Secret.

```yaml
apiVersion: ferrvault.com/v1alpha1
kind: FerrVaultSecret
metadata:
  name: web-env
spec:
  connectionRef: { name: prod }
  project: web
  vault: production          # FerrVault vault name (often the environment)
  selector:
    names: [DATABASE_URL, STRIPE_KEY]   # omit to sync every key in the vault
  target:
    name: web-env            # target Secret name; defaults to metadata.name
    type: Opaque
  refreshInterval: 30m       # Go time.Duration; 0s disables scheduled refresh
```

On reconciliation the operator calls `GET /api/v1/orgs/:org/projects/:project/vaults/by-name/:vault/secrets/reveal` once, writes the returned `{name: value}` map into `spec.target.name`, and sets the CR's `Ready` condition based on whether any requested keys were missing upstream.

The generated Secret is owned by the CR, so deleting the CR garbage-collects the Secret.

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

Malformed transforms (unknown type, invalid base64, non-object JSON, destination-key collisions) leave the CR in `Ready=False` with `Reason=TransformError` and increment `ferrvault_secret_sync_errors_total{reason="TransformError"}`. The target Secret is not written on failure, so workloads keep the last known-good value.

## Running

### Helm (recommended)

```bash
helm install ferrvault-operator oci://ghcr.io/ferrlabs/charts/ferrvault-operator \
  --namespace ferrvault-operator-system --create-namespace
```

Upgrade: `helm upgrade` against the same release. CRDs carry `helm.sh/resource-policy: keep` so they survive uninstall (protects your CRs + managed Secrets). See [`charts/ferrvault-operator/README.md`](charts/ferrvault-operator/README.md) for the full `values.yaml` reference.

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
kubectl create namespace ferrvault-operator-system
helm template ferrvault-operator charts/ferrvault-operator \
  --namespace ferrvault-operator-system \
  > manager.yaml
kubectl apply -f manager.yaml
```

No duplicate `config/rbac/` or `config/crd/` lives in the repo; anything
rendered from the chart *is* the canonical version.

## Prerequisites in FerrVault

The operator relies on endpoints in [`FerrLabs/FerrVault-Cloud`](https://github.com/FerrLabs/FerrVault-Cloud) that shipped in `api@v4.0.0`:

- API token auth (`Authorization: Bearer fft_...`) with granular scopes (#268)
- `secrets:read` scope enforcement on all secrets routes (#268)
- Bulk reveal endpoint (#277)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Code of conduct in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Vulnerability reports via [SECURITY.md](SECURITY.md).

## License

[MPL-2.0](LICENSE)
