/*
Copyright 2026.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PhaseReady       = "Ready"
	PhaseEmpty       = "Empty"
	PhaseProgressing = "Progressing"
)

// StockPoolSpec defines the desired state of StockPool.
type StockPoolSpec struct {
	// SpecName describes the target GPU flavor requested by users, for example "g1.1".
	SpecName string `json:"specName"`

	// Image is the runtime image used by the reconciled workload.
	Image string `json:"image,omitempty"`

	// Memory is the memory request/limit for each runtime worker, for example "16Gi".
	Memory string `json:"memory,omitempty"`

	// GPU is the number of GPUs requested per runtime worker.
	// +kubebuilder:validation:Minimum=0
	GPU int32 `json:"gpu,omitempty"`

	// Replicas controls how many runtime workers should exist for this pool.
	// +kubebuilder:validation:Minimum=0
	Replicas int32 `json:"replicas"`
}

// StockPoolStatus defines the observed state of StockPool.
type StockPoolStatus struct {
	Available          int32       `json:"available,omitempty"`
	Allocated          int32       `json:"allocated,omitempty"`
	Phase              string      `json:"phase,omitempty"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Spec",type=string,JSONPath=`.spec.specName`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.available`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StockPool is the Schema for the stockpools API.
type StockPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StockPoolSpec   `json:"spec,omitempty"`
	Status StockPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StockPoolList contains a list of StockPool.
type StockPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StockPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StockPool{}, &StockPoolList{})
}
