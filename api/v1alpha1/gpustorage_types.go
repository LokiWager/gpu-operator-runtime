/*
Copyright 2026.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	StoragePhaseReady   = "Ready"
	StoragePhasePending = "Pending"
	StoragePhaseFailed  = "Failed"

	DefaultGPUStorageClassName = "rook-ceph-block"

	ReasonStorageReady          = "StorageReady"
	ReasonStoragePending        = "StoragePending"
	ReasonStorageClaimLost      = "StorageClaimLost"
	ReasonStoragePVCSyncFailed  = "StoragePVCSyncFailed"
	ReasonStorageInvalidSpec    = "StorageInvalidSpec"
	StatusMessageStorageReady   = "Persistent storage is ready."
	StatusMessageStoragePending = "Waiting for the persistent volume claim to bind."
)

// GPUStorageSpec defines the desired state of one persistent storage object.
type GPUStorageSpec struct {
	// Size is the requested persistent volume size, for example "20Gi".
	Size string `json:"size"`

	// StorageClassName optionally selects the Kubernetes StorageClass backing the claim.
	// When omitted, the controller and API default to rook-ceph-block for RBD-backed workspace volumes.
	// +kubebuilder:default:=rook-ceph-block
	StorageClassName string `json:"storageClassName,omitempty"`
}

// GPUStorageStatus defines the observed state of one persistent storage object.
type GPUStorageStatus struct {
	ClaimName          string             `json:"claimName,omitempty"`
	Capacity           string             `json:"capacity,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time        `json:"lastSyncTime,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.spec.size`
// +kubebuilder:printcolumn:name="Capacity",type=string,JSONPath=`.status.capacity`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GPUStorage is the schema for one persistent storage object used by runtime units.
type GPUStorage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPUStorageSpec   `json:"spec,omitempty"`
	Status GPUStorageStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GPUStorageList contains a list of GPUStorage objects.
type GPUStorageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUStorage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUStorage{}, &GPUStorageList{})
}
