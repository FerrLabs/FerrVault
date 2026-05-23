package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=fvc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Org",type=string,JSONPath=`.spec.organization`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type FerrVaultConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ffv1alpha1.FerrFlowConnectionSpec   `json:"spec,omitempty"`
	Status ffv1alpha1.FerrFlowConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type FerrVaultConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FerrVaultConnection `json:"items"`
}

func (in *FerrVaultConnection) DeepCopyInto(out *FerrVaultConnection) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *FerrVaultConnection) DeepCopy() *FerrVaultConnection {
	if in == nil {
		return nil
	}
	out := new(FerrVaultConnection)
	in.DeepCopyInto(out)
	return out
}

func (in *FerrVaultConnection) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *FerrVaultConnectionList) DeepCopyInto(out *FerrVaultConnectionList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]FerrVaultConnection, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *FerrVaultConnectionList) DeepCopy() *FerrVaultConnectionList {
	if in == nil {
		return nil
	}
	out := new(FerrVaultConnectionList)
	in.DeepCopyInto(out)
	return out
}

func (in *FerrVaultConnectionList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
