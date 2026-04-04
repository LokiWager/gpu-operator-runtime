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

	DefaultGPUStorageClassName    = "rook-ceph-block"
	DefaultStorageAccessorPort    = 8080
	DefaultStorageAccessorImage   = "busybox:1.36"
	StorageAccessorContainerName  = "storage-accessor"
	StoragePrepareMountPath       = "/workspace"
	StoragePrepareSourceMountPath = "/source"

	ConditionPrepared      = "Prepared"
	ConditionAccessorReady = "AccessorReady"

	StoragePreparePhaseNotRequested = "NotRequested"
	StoragePreparePhasePending      = "Pending"
	StoragePreparePhaseRunning      = "Running"
	StoragePreparePhaseSucceeded    = "Succeeded"
	StoragePreparePhaseFailed       = "Failed"

	StorageAccessorPhaseDisabled = "Disabled"
	StorageAccessorPhasePending  = "Pending"
	StorageAccessorPhaseReady    = "Ready"
	StorageAccessorPhaseFailed   = "Failed"

	StorageRecoveryPhaseNone      = "None"
	StorageRecoveryPhaseRequired  = "Required"
	StorageRecoveryPhaseRunning   = "Running"
	StorageRecoveryPhaseSucceeded = "Succeeded"

	ReasonStorageReady                   = "StorageReady"
	ReasonStoragePending                 = "StoragePending"
	ReasonStorageClaimLost               = "StorageClaimLost"
	ReasonStoragePVCSyncFailed           = "StoragePVCSyncFailed"
	ReasonStorageInvalidSpec             = "StorageInvalidSpec"
	ReasonStoragePreparePending          = "StoragePreparePending"
	ReasonStoragePrepareRunning          = "StoragePrepareRunning"
	ReasonStoragePrepareReady            = "StoragePrepareReady"
	ReasonStoragePrepareFailed           = "StoragePrepareFailed"
	ReasonStorageAccessorPending         = "StorageAccessorPending"
	ReasonStorageAccessorReady           = "StorageAccessorReady"
	ReasonStorageAccessorFailed          = "StorageAccessorFailed"
	StatusMessageStorageReady            = "Persistent storage is ready."
	StatusMessageStoragePending          = "Waiting for the persistent volume claim to bind."
	StatusMessageStoragePrepared         = "Persistent storage content is ready."
	StatusMessageStoragePreparePending   = "Waiting for storage data preparation to start."
	StatusMessageStoragePrepareRunning   = "Storage data preparation is running."
	StatusMessageStoragePrepareFailed    = "Storage data preparation failed and requires recovery."
	StatusMessageStorageAccessorDisabled = "Storage accessor is disabled."
	StatusMessageStorageAccessorPending  = "Waiting for the storage accessor to become ready."
	StatusMessageStorageAccessorReady    = "Storage accessor is ready."

	AnnotationStorageRecoveryNonce = "runtime.lokiwager.io/storage-recovery-nonce"
)

// GPUStoragePrepareSpec defines how one storage object should be seeded with data.
type GPUStoragePrepareSpec struct {
	// FromImage is the image used by the data preparation job.
	FromImage string `json:"fromImage,omitempty"`

	// FromStorageName copies data from another storage object in the same namespace.
	FromStorageName string `json:"fromStorageName,omitempty"`

	// Command overrides the container command used by the data preparation job.
	Command []string `json:"command,omitempty"`

	// Args overrides the container args used by the data preparation job.
	Args []string `json:"args,omitempty"`
}

// GPUStorageAccessorSpec defines whether the platform should publish an HTTP accessor for one storage object.
type GPUStorageAccessorSpec struct {
	// Enabled turns on the built-in storage accessor deployment and service.
	Enabled bool `json:"enabled,omitempty"`
}

// GPUStorageSpec defines the desired state of one persistent storage object.
type GPUStorageSpec struct {
	// Size is the requested persistent volume size, for example "20Gi".
	Size string `json:"size"`

	// StorageClassName optionally selects the Kubernetes StorageClass backing the claim.
	// When omitted, the controller and API default to rook-ceph-block for RBD-backed workspace volumes.
	// +kubebuilder:default:=rook-ceph-block
	StorageClassName string `json:"storageClassName,omitempty"`

	// Prepare describes how the platform should seed the storage contents after the PVC is ready.
	Prepare GPUStoragePrepareSpec `json:"prepare,omitempty"`

	// Accessor controls the first built-in path for browsing storage through a controller-owned service.
	Accessor GPUStorageAccessorSpec `json:"accessor,omitempty"`
}

// GPUStoragePrepareStatus reports the current data-preparation state for one storage object.
type GPUStoragePrepareStatus struct {
	Phase          string `json:"phase,omitempty"`
	JobName        string `json:"jobName,omitempty"`
	ObservedDigest string `json:"observedDigest,omitempty"`
	RecoveryPhase  string `json:"recoveryPhase,omitempty"`
}

// GPUStorageAccessorStatus reports the current accessor state for one storage object.
type GPUStorageAccessorStatus struct {
	Phase       string `json:"phase,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	AccessURL   string `json:"accessURL,omitempty"`
}

// GPUStorageStatus defines the observed state of one persistent storage object.
type GPUStorageStatus struct {
	ClaimName          string                   `json:"claimName,omitempty"`
	Capacity           string                   `json:"capacity,omitempty"`
	Phase              string                   `json:"phase,omitempty"`
	Prepare            GPUStoragePrepareStatus  `json:"prepare,omitempty"`
	Accessor           GPUStorageAccessorStatus `json:"accessor,omitempty"`
	ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time              `json:"lastSyncTime,omitempty"`
	Conditions         []metav1.Condition       `json:"conditions,omitempty"`
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
