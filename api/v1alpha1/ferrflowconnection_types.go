package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// FerrFlowConnectionSpec declares how to reach a FerrFlow API instance and
// which organization the operator should scope its reads to. A single
// `FerrFlowConnection` object is typically shared by multiple `FerrFlowSecret`
// objects in the same namespace — users create one per (cluster, org) pair.
//
// Exactly one authentication source must be set: either `tokenSecretRef`
// (long-lived token stored in a k8s Secret) or `oidc` (workload-identity
// exchange — recommended, no long-lived secret at rest).
type FerrFlowConnectionSpec struct {
	// URL is the base of the FerrFlow API — e.g. `https://ferrflow.example.com`.
	// The operator appends `/api/v1/…` paths itself.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// Organization is the FerrFlow org slug this connection targets. Every
	// `FerrFlowSecret` referencing this connection resolves `project` and
	// `vault` inside this org.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Organization string `json:"organization"`

	// TokenSecretRef points at a Kubernetes Secret holding a FerrFlow
	// authentication token — `ffclust_...` (cluster identity) or `fft_...`
	// (user API token, org-scoped, discouraged).
	//
	// Kept for **air-gapped clusters** where FerrFlow cannot reach the
	// cluster's OIDC JWKS endpoint, and for **non-Kubernetes callers**.
	// When FerrFlow can reach the cluster, prefer `oidc` — no secret at
	// rest, short-lived tokens, automatic rotation.
	//
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`

	// OIDC enables workload-identity authentication: the operator presents
	// its projected ServiceAccount token to FerrFlow, which validates it
	// against the cluster's registered OIDC config and mints a short-lived
	// bearer. No long-lived secret on the cluster side, revocation is
	// instant on the FerrFlow side.
	//
	// Requires the FerrFlow cluster resource to be pre-configured with
	// OIDC (`PUT /orgs/:org/clusters/:id/oidc` on the FerrFlow API), and
	// the operator pod to have a projected SA token volume mounted at
	// `oidc.tokenPath` with audience matching `oidc.audience`.
	//
	// +optional
	OIDC *OIDCAuth `json:"oidc,omitempty"`
}

// OIDCAuth configures the workload-identity exchange. The operator posts
// the JWT at `oidc.tokenPath` to FerrFlow's `POST /clusters/oidc-exchange`
// and caches the returned bearer for its lifetime (currently ~15 min).
type OIDCAuth struct {
	// ClusterID is the UUID of the cluster resource registered in FerrFlow.
	// Admins retrieve it from the FerrFlow UI after creating the cluster.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F-]{36}$`
	ClusterID string `json:"clusterID"`

	// TokenPath is the filesystem location where kubelet mounts the
	// projected ServiceAccount token. Defaults to
	// `/var/run/secrets/ferrflow/token` — match this in the operator pod's
	// `volumeMounts` + `serviceAccountToken` projection.
	//
	// +optional
	TokenPath string `json:"tokenPath,omitempty"`

	// Audience the projected ServiceAccount token declares in its `aud`
	// claim. Must match `EXPECTED_AUDIENCE` on the FerrFlow side
	// (`https://ferrflow.com`). Defaults to that value when omitted.
	//
	// +optional
	Audience string `json:"audience,omitempty"`
}

// DefaultTokenPath is where kubelet mounts the projected SA token in our
// sample Deployment. Exposed so both the controllers and the chart share
// a single source of truth.
const DefaultTokenPath = "/var/run/secrets/ferrflow/token"

// DefaultAudience matches the FerrFlow API's `EXPECTED_AUDIENCE` constant.
// Changing this is a coordinated breaking change across both repos.
const DefaultAudience = "https://ferrflow.com"

// SecretKeyRef selects a single key inside a Kubernetes Secret.
type SecretKeyRef struct {
	// Name of the Secret. Must live in the same namespace as the
	// FerrFlowConnection object.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key within the Secret's `data` map.
	//
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// FerrFlowConnectionStatus reports the outcome of the most recent probe
// against the configured FerrFlow instance. The controller refreshes this on
// every reconciliation of any `FerrFlowSecret` that references the connection.
type FerrFlowConnectionStatus struct {
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

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ffc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Org",type=string,JSONPath=`.spec.organization`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// FerrFlowConnection is the Schema for the ferrflowconnections API.
type FerrFlowConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FerrFlowConnectionSpec   `json:"spec,omitempty"`
	Status FerrFlowConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FerrFlowConnectionList contains a list of FerrFlowConnection.
type FerrFlowConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FerrFlowConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FerrFlowConnection{}, &FerrFlowConnectionList{})
}

// DeepCopyInto copies the receiver into out.
func (in *FerrFlowConnection) DeepCopyInto(out *FerrFlowConnection) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopyInto for the spec — pointer fields (TokenSecretRef, OIDC) are the
// reason we need a real deep copy here instead of the shallow `= *in`.
// Shallow would share the inner struct between receivers of the deep copy,
// which breaks the "caller can mutate without affecting the source"
// invariant that the Kubernetes controller-runtime relies on.
func (in *FerrFlowConnectionSpec) DeepCopyInto(out *FerrFlowConnectionSpec) {
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

// DeepCopy returns a deep copy of the receiver.
func (in *FerrFlowConnection) DeepCopy() *FerrFlowConnection {
	if in == nil {
		return nil
	}
	out := new(FerrFlowConnection)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *FerrFlowConnection) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto copies the receiver into out.
func (in *FerrFlowConnectionList) DeepCopyInto(out *FerrFlowConnectionList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]FerrFlowConnection, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the receiver.
func (in *FerrFlowConnectionList) DeepCopy() *FerrFlowConnectionList {
	if in == nil {
		return nil
	}
	out := new(FerrFlowConnectionList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *FerrFlowConnectionList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto for the status block — manually written so we can drop the
// codegen step for now.
func (in *FerrFlowConnectionStatus) DeepCopyInto(out *FerrFlowConnectionStatus) {
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
