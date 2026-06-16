package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConnectionSpec declares how to reach a FerrVault API instance and which
// organization the operator should scope its reads to. A single connection
// object is typically shared by multiple secret objects in the same namespace
// — users create one per (cluster, org) pair.
//
// Exactly one authentication source must be set: either `tokenSecretRef`
// (long-lived token stored in a k8s Secret) or `oidc` (workload-identity
// exchange — recommended, no long-lived secret at rest).
type ConnectionSpec struct {
	// Mode selects which backend API the operator talks to:
	//
	//   - `cloud` — legacy FerrLabs-Cloud shape, vault addressed as
	//     `orgs/{org}/projects/{project}/vaults/by-name/{vault}`.
	//     Auth via `tokenSecretRef` (`ffclust_…` / `fft_…`) or `oidc`.
	//
	//   - `ferrvault` — new FerrVault SaaS, flat `/v1/operator/secrets/reveal`
	//     surface. Auth via a Service-Account Token (`sat_…`) bound to a
	//     specific vault. `organization` is ignored; the `project` field on
	//     each secret is ignored too — the SAT scopes everything.
	//
	// +kubebuilder:validation:Enum=cloud;ferrvault
	// +kubebuilder:default=ferrvault
	// +optional
	Mode string `json:"mode,omitempty"`

	// URL is the base of the FerrVault API — e.g. `https://ferrvault.example.com`.
	// The operator appends `/api/v1/…` paths itself.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// Organization is the FerrVault org slug this connection targets. Every
	// secret referencing this connection resolves `project` and `vault`
	// inside this org.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Organization string `json:"organization"`

	// TokenSecretRef points at a Kubernetes Secret holding a FerrVault
	// authentication token — `ffclust_...` (cluster identity) or `fft_...`
	// (user API token, org-scoped, discouraged).
	//
	// Kept for **air-gapped clusters** where FerrVault cannot reach the
	// cluster's OIDC JWKS endpoint, and for **non-Kubernetes callers**.
	// When FerrVault can reach the cluster, prefer `oidc` — no secret at
	// rest, short-lived tokens, automatic rotation.
	//
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`

	// OIDC enables workload-identity authentication: the operator presents
	// its projected ServiceAccount token to FerrVault, which validates it
	// against the cluster's registered OIDC config and mints a short-lived
	// bearer. No long-lived secret on the cluster side, revocation is
	// instant on the FerrVault side.
	//
	// Requires the FerrVault cluster resource to be pre-configured with
	// OIDC (`PUT /orgs/:org/clusters/:id/oidc` on the FerrVault API), and
	// the operator pod to have a projected SA token volume mounted at
	// `oidc.tokenPath` with audience matching `oidc.audience`.
	//
	// +optional
	OIDC *OIDCAuth `json:"oidc,omitempty"`
}

// OIDCAuth configures the workload-identity exchange. The operator posts
// the JWT at `oidc.tokenPath` to FerrVault's `POST /clusters/oidc-exchange`
// and caches the returned bearer for its lifetime (currently ~15 min).
type OIDCAuth struct {
	// ClusterID is the UUID of the cluster resource registered in FerrVault.
	// Admins retrieve it from the FerrVault UI after creating the cluster.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F-]{36}$`
	ClusterID string `json:"clusterID"`

	// TokenPath is the filesystem location where kubelet mounts the
	// projected ServiceAccount token. Defaults to
	// `/var/run/secrets/ferrvault/token` — match this in the operator pod's
	// `volumeMounts` + `serviceAccountToken` projection.
	//
	// +optional
	TokenPath string `json:"tokenPath,omitempty"`

	// Audience the projected ServiceAccount token declares in its `aud`
	// claim. Must match `EXPECTED_AUDIENCE` on the FerrVault side
	// (`https://ferrflow.com`). Defaults to that value when omitted.
	//
	// +optional
	Audience string `json:"audience,omitempty"`
}

// Backend mode literals for ConnectionSpec.Mode.
const (
	ModeCloud     = "cloud"
	ModeFerrVault = "ferrvault"
)

// DefaultTokenPath is where kubelet mounts the projected SA token in our
// sample Deployment. Exposed so both the controllers and the chart share
// a single source of truth.
const DefaultTokenPath = "/var/run/secrets/ferrvault/token"

// DefaultAudience matches the FerrVault API's `EXPECTED_AUDIENCE` constant.
// Changing this is a coordinated breaking change across both repos.
const DefaultAudience = "https://ferrflow.com"

// ResolvedMode returns the effective Mode for the connection spec, applying
// the `ferrvault` default for empty values so callers don't have to repeat it.
func (s ConnectionSpec) ResolvedMode() string {
	if s.Mode == "" {
		return ModeFerrVault
	}
	return s.Mode
}

// SecretKeyRef selects a single key inside a Kubernetes Secret.
type SecretKeyRef struct {
	// Name of the Secret. Must live in the same namespace as the connection
	// object.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key within the Secret's `data` map.
	//
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// ConnectionStatus reports the outcome of the most recent probe against the
// configured FerrVault instance. The controller refreshes this on every
// reconciliation of any secret that references the connection.
type ConnectionStatus struct {
	// Conditions follows the upstream `metav1.Condition` convention. The
	// primary condition is `Ready` — `True` when the token authenticates and
	// the `secrets:read` scope is present.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastCheckedAt is the timestamp of the most recent auth probe.
	//
	// +optional
	LastCheckedAt *metav1.Time `json:"lastCheckedAt,omitempty"`
}

// DeepCopyInto copies the receiver into out. Pointer fields (TokenSecretRef,
// OIDC) are the reason we need a real deep copy here instead of the shallow
// `= *in`. Shallow would share the inner struct between receivers of the deep
// copy, which breaks the "caller can mutate without affecting the source"
// invariant that the Kubernetes controller-runtime relies on.
func (in *ConnectionSpec) DeepCopyInto(out *ConnectionSpec) {
	*out = *in
	if in.TokenSecretRef != nil {
		out.TokenSecretRef = new(SecretKeyRef)
		*out.TokenSecretRef = *in.TokenSecretRef
	}
	if in.OIDC != nil {
		out.OIDC = new(OIDCAuth)
		*out.OIDC = *in.OIDC
	}
}

// DeepCopyInto copies the receiver into out.
func (in *ConnectionStatus) DeepCopyInto(out *ConnectionStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.LastCheckedAt != nil {
		out.LastCheckedAt = in.LastCheckedAt.DeepCopy()
	}
}
