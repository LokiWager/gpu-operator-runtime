package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	PhaseReady = "Ready"
	PhaseEmpty = "Empty"
)

type StockPoolSpec struct {
	SpecName string `json:"specName"`
	Replicas int32  `json:"replicas"`
}

type StockPoolStatus struct {
	Available          int32       `json:"available"`
	Allocated          int32       `json:"allocated"`
	Phase              string      `json:"phase,omitempty"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type StockPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StockPoolSpec   `json:"spec,omitempty"`
	Status StockPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type StockPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StockPool `json:"items"`
}

func (in *StockPool) DeepCopyInto(out *StockPool) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

func (in *StockPool) DeepCopy() *StockPool {
	if in == nil {
		return nil
	}
	out := new(StockPool)
	in.DeepCopyInto(out)
	return out
}

func (in *StockPool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *StockPoolList) DeepCopyInto(out *StockPoolList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]StockPool, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *StockPoolList) DeepCopy() *StockPoolList {
	if in == nil {
		return nil
	}
	out := new(StockPoolList)
	in.DeepCopyInto(out)
	return out
}

func (in *StockPoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
