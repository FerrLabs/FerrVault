package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=fvs
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.project`
// +kubebuilder:printcolumn:name="Vault",type=string,JSONPath=`.spec.vault`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="LastSynced",type=date,JSONPath=`.status.lastSyncedAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type FerrVaultSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ffv1alpha1.FerrFlowSecretSpec   `json:"spec,omitempty"`
	Status ffv1alpha1.FerrFlowSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type FerrVaultSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FerrVaultSecret `json:"items"`
}

func (in *FerrVaultSecret) DeepCopyInto(out *FerrVaultSecret) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *FerrVaultSecret) DeepCopy() *FerrVaultSecret {
	if in == nil {
		return nil
	}
	out := new(FerrVaultSecret)
	in.DeepCopyInto(out)
	return out
}

func (in *FerrVaultSecret) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *FerrVaultSecretList) DeepCopyInto(out *FerrVaultSecretList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]FerrVaultSecret, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *FerrVaultSecretList) DeepCopy() *FerrVaultSecretList {
	if in == nil {
		return nil
	}
	out := new(FerrVaultSecretList)
	in.DeepCopyInto(out)
	return out
}

func (in *FerrVaultSecretList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
