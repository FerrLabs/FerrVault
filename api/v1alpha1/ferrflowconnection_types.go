package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// FerrFlowConnectionSpec declares how to reach a FerrFlow API instance and
// which organization the operator should scope its reads to. A single
// `FerrFlowConnection` object is typically shared by multiple `FerrFlowSecret`
// objects in the same namespace — users create one per (cluster, org) pair.
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
	// authentication token. Two formats are supported:
	//
	//   * `ffclust_<prefix>_<secret>` — **recommended**. A cluster identity
	//     created in the FerrFlow UI (`Clusters` page). Authorization is
	//     enforced per-(namespace, project, vault) so the blast radius stays
	//     within what you explicitly granted. The operator sends the CR's
	//     namespace as `X-FerrFlow-Namespace` on every reveal call, and the
	//     API checks it against `cluster_authorizations` rows.
	//
	//   * `fft_<prefix>_<secret>` — a user API token with `secrets:read`
	//     scope. Supported for back-compat but discouraged: blast radius is
	//     the whole org, no namespace scoping. Prefer cluster identities for
	//     anything beyond a quick local test.
	//
	// +kubebuilder:validation:Required
	TokenSecretRef SecretKeyRef `json:"tokenSecretRef"`
}

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
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
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
