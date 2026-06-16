package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalObjectReference is a namespaced reference with just a name, used to
// point at another CR living in the same namespace.
type LocalObjectReference struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// SecretSelector picks which keys to pull from the referenced FerrVault vault.
type SecretSelector struct {
	// Names is an explicit list of secret keys to sync. When empty, every
	// secret in the vault is synced.
	//
	// +optional
	Names []string `json:"names,omitempty"`
}

// SecretTarget controls the Kubernetes Secret the operator writes to.
type SecretTarget struct {
	// Name of the `Secret` to create or update in the same namespace as the
	// CR. Defaults to `metadata.name` of the secret CR itself when omitted.
	//
	// +optional
	Name string `json:"name,omitempty"`

	// Type of the generated Secret (e.g. `Opaque`, `kubernetes.io/tls`).
	// Defaults to `Opaque`.
	//
	// +optional
	Type string `json:"type,omitempty"`
}

// WorkloadRef identifies a workload whose pods should be rolled when secret
// values change. The operator adds an annotation to the pod template to
// trigger a rolling restart.
type WorkloadRef struct {
	// Kind of the workload. One of: Deployment, StatefulSet, DaemonSet.
	//
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet
	Kind string `json:"kind"`

	// Name of the workload within the same namespace as the CR.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// SecretTransform rewrites the `{key: value}` map returned by the FerrVault
// API before it lands in the target Secret. Transforms are applied in the
// order declared. Exactly one of the payload fields relevant to `type` must
// be set — the CRD enforces this via OpenAPI validation at admission time.
//
// Supported types:
//
//   - prefix: Stamp `value` in front of every key.
//   - suffix: Append `value` to every key.
//   - rename: Project `from` to `to`. No-op when `from` is missing.
//   - base64Decode: Decode the listed `keys` (or all when empty) from base64.
//   - jsonExpand: Flatten a JSON object under `key` into `<KEY>_<SUB>` entries.
//     Nested objects compose with `_`. The source key is dropped.
type SecretTransform struct {
	// Type identifies the transform kind.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=prefix;suffix;rename;base64Decode;jsonExpand
	Type string `json:"type"`

	// Value is the payload for `prefix` and `suffix`.
	//
	// +optional
	Value string `json:"value,omitempty"`

	// From is the source key for `rename`.
	//
	// +optional
	From string `json:"from,omitempty"`

	// To is the destination key for `rename`.
	//
	// +optional
	To string `json:"to,omitempty"`

	// Keys restricts `base64Decode` to a subset of keys. Empty = all keys.
	//
	// +optional
	Keys []string `json:"keys,omitempty"`

	// Key is the source key for `jsonExpand`.
	//
	// +optional
	Key string `json:"key,omitempty"`
}

// SecretSpec declares a sync from a FerrVault vault into a native Kubernetes
// Secret. The operator reconciles this CR on the configured `refreshInterval`,
// or immediately when the spec changes.
type SecretSpec struct {
	// ConnectionRef points at a connection in the same namespace.
	//
	// +kubebuilder:validation:Required
	ConnectionRef LocalObjectReference `json:"connectionRef"`

	// Project is the FerrVault project slug.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// Vault is the FerrVault vault name inside the project (often used as the
	// environment identifier: `production`, `staging`, …).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Vault string `json:"vault"`

	// Selector chooses which secrets to sync.
	//
	// +optional
	Selector SecretSelector `json:"selector,omitempty"`

	// Target controls the generated Secret.
	//
	// +optional
	Target SecretTarget `json:"target,omitempty"`

	// RefreshInterval is the cadence at which the operator polls FerrVault for
	// value changes, expressed in Go's `time.Duration` syntax (`30m`, `1h`).
	// Defaults to `1h`. Set to `0s` to disable scheduled refreshes (the
	// operator still reacts to spec changes).
	//
	// +optional
	// +kubebuilder:default="1h"
	RefreshInterval string `json:"refreshInterval,omitempty"`

	// RolloutRestart lists workloads whose pods should be rolled when secret
	// values change. Pattern: the operator patches the pod template's
	// restart annotation to force a rolling update.
	//
	// +optional
	RolloutRestart []WorkloadRef `json:"rolloutRestart,omitempty"`

	// Transforms rewrite the revealed key/value map before it's written to
	// the target Secret. Applied in order. See SecretTransform for the
	// supported operations.
	//
	// +optional
	Transforms []SecretTransform `json:"transforms,omitempty"`
}

// SecretStatus reports the last reconciliation outcome.
type SecretStatus struct {
	// Conditions conveys the health of this sync. The primary condition is
	// `Ready` — `True` when the target Secret mirrors the desired keys.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncedAt is the timestamp of the most recent successful reveal.
	//
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// SyncedKeys is the list of keys present in the target Secret after the
	// most recent reconciliation. Handy for `kubectl get` columns and for
	// diffing which keys disappeared upstream.
	//
	// +optional
	SyncedKeys []string `json:"syncedKeys,omitempty"`

	// MissingKeys lists keys that were requested in `spec.selector.names` but
	// were not present in the FerrVault vault at last reveal. When non-empty,
	// `Ready` is set to `False`.
	//
	// +optional
	MissingKeys []string `json:"missingKeys,omitempty"`

	// ObservedGeneration is the generation of the CR the controller has
	// successfully applied.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// DeepCopyInto for SecretSpec — slice fields need deep copies.
func (in *SecretSpec) DeepCopyInto(out *SecretSpec) {
	*out = *in
	out.ConnectionRef = in.ConnectionRef
	if in.Selector.Names != nil {
		out.Selector.Names = make([]string, len(in.Selector.Names))
		copy(out.Selector.Names, in.Selector.Names)
	}
	out.Target = in.Target
	if in.RolloutRestart != nil {
		out.RolloutRestart = make([]WorkloadRef, len(in.RolloutRestart))
		copy(out.RolloutRestart, in.RolloutRestart)
	}
	if in.Transforms != nil {
		out.Transforms = make([]SecretTransform, len(in.Transforms))
		for i := range in.Transforms {
			in.Transforms[i].DeepCopyInto(&out.Transforms[i])
		}
	}
}

// DeepCopyInto copies the receiver into out. Keys slice is copied
// element-wise; all other fields are scalars.
func (in *SecretTransform) DeepCopyInto(out *SecretTransform) {
	*out = *in
	if in.Keys != nil {
		out.Keys = make([]string, len(in.Keys))
		copy(out.Keys, in.Keys)
	}
}

// DeepCopyInto for SecretStatus — includes Time pointer + slices.
func (in *SecretStatus) DeepCopyInto(out *SecretStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.LastSyncedAt != nil {
		out.LastSyncedAt = in.LastSyncedAt.DeepCopy()
	}
	if in.SyncedKeys != nil {
		out.SyncedKeys = make([]string, len(in.SyncedKeys))
		copy(out.SyncedKeys, in.SyncedKeys)
	}
	if in.MissingKeys != nil {
		out.MissingKeys = make([]string, len(in.MissingKeys))
		copy(out.MissingKeys, in.MissingKeys)
	}
}
