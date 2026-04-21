# ferrflow-operator

Helm chart for the [ferrflow-operator](https://github.com/FerrLabs/FerrFlow-Operator) — a Kubernetes operator that syncs secrets from a FerrFlow instance into native `Secret` resources.

## Install

```bash
helm install ferrflow-operator oci://ghcr.io/ferrlabs/charts/ferrflow-operator \
  --namespace ferrflow-operator-system --create-namespace
```

Pin to a specific version:

```bash
helm install ferrflow-operator oci://ghcr.io/ferrlabs/charts/ferrflow-operator \
  --version 0.1.0 \
  --namespace ferrflow-operator-system --create-namespace
```

## Upgrade

```bash
helm upgrade ferrflow-operator oci://ghcr.io/ferrlabs/charts/ferrflow-operator \
  --namespace ferrflow-operator-system
```

CRDs carry the `helm.sh/resource-policy: keep` annotation by default so an upgrade or uninstall never deletes your `FerrFlowSecret` / `FerrFlowConnection` CRs (and the Kubernetes Secrets they own). If you want CRD changes applied, set `crds.keep=false` before upgrading.

## Uninstall

```bash
helm uninstall ferrflow-operator --namespace ferrflow-operator-system
```

CRDs survive. To remove them (and every CR + managed Secret they own), do it deliberately:

```bash
kubectl delete crd ferrflowsecrets.ferrflow.io ferrflowconnections.ferrflow.io
```

## Values

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `ghcr.io/ferrlabs/ferrflow-operator` | |
| `image.tag` | `""` → `Chart.AppVersion` | |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | For private registries. |
| `replicaCount` | `1` | Raise when using leader election for HA. |
| `leaderElection.enabled` | `true` | |
| `leaderElection.id` | `ferrflow-operator.ferrflow.io` | Change to run multiple isolated instances in one cluster. |
| `watchNamespace` | `""` (cluster-wide) | Single namespace scope when set. |
| `defaultRefreshInterval` | `1h` | Fallback for `FerrFlowSecret.spec.refreshInterval`. |
| `logLevel` | `info` | `debug`, `info`, `warn`, `error`. |
| `extraArgs` | `[]` | Extra manager CLI flags. |
| `metrics.enabled` | `true` | |
| `metrics.port` | `8080` | |
| `metrics.serviceMonitor.enabled` | `false` | Requires Prometheus Operator CRDs. |
| `probe.port` | `8081` | Liveness/readiness. |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | `""` | Defaults to the release's fullname. |
| `serviceAccount.annotations` | `{}` | |
| `rbac.create` | `true` | Set to false if RBAC is managed externally. |
| `crds.install` | `true` | |
| `crds.keep` | `true` | CRDs survive uninstall. Preserves user data. |
| `resources` | `100m/128Mi` requests, `500m/256Mi` limits | |
| `podSecurityContext` | non-root 65532, seccomp RuntimeDefault | |
| `containerSecurityContext` | no privesc, drop ALL caps, readOnlyRootFS | |
| `nodeSelector`, `tolerations`, `affinity` | `{}` / `[]` / `{}` | |
| `extraEnv` | `[]` | Extra env vars on the manager container. |

See [`values.yaml`](values.yaml) for the full surface with comments.

## Usage example

Once the chart is installed, create a token Secret, a Connection, and a Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ferrflow-api-token
  namespace: default
type: Opaque
stringData:
  token: fft_replace_me
---
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowConnection
metadata:
  name: prod
  namespace: default
spec:
  url: https://ferrflow.example.com
  organization: acme
  tokenSecretRef:
    name: ferrflow-api-token
    key: token
---
apiVersion: ferrflow.io/v1alpha1
kind: FerrFlowSecret
metadata:
  name: web-env
  namespace: default
spec:
  connectionRef: { name: prod }
  project: web
  vault: production
  selector:
    names: [DATABASE_URL, STRIPE_KEY]
  target:
    name: web-env
  refreshInterval: 30m
```

Watch the `FerrFlowSecret` reconcile and the target Secret appear:

```bash
kubectl get ferrflowsecret web-env -n default -o yaml
kubectl get secret web-env -n default -o yaml
```

## License

[MPL-2.0](../../LICENSE).
