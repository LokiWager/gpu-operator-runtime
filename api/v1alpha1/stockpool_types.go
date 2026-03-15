/*
Copyright 2026.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PhaseReady       = "Ready"
	PhaseEmpty       = "Empty"
	PhaseProgressing = "Progressing"
	PhaseFailed      = "Failed"

	ConditionReady = "Ready"

	ReasonScaledToZero          = "ScaledToZero"
	ReasonDeploymentProgressing = "DeploymentProgressing"
	ReasonDeploymentReady       = "DeploymentReady"
	ReasonInvalidSpec           = "InvalidSpec"
	ReasonPodStartupFailed      = "PodStartupFailed"
	ReasonPodStatusSyncFailed   = "PodStatusSyncFailed"
	ReasonServiceSyncFailed     = "ServiceSyncFailed"
	ReasonDeploymentSyncFailed  = "DeploymentSyncFailed"

	LabelAppNameKey                    = "app.kubernetes.io/name"
	LabelManagedByKey                  = "app.kubernetes.io/managed-by"
	LabelPoolKey                       = "runtime.lokiwager.io/pool"
	LabelAppNameValue                  = "gpu-runtime-stockpool"
	LabelManagedByValue                = "gpu-runtime-operator"
	EnvSpecName                        = "SPEC_NAME"
	EnvPoolName                        = "POOL_NAME"
	EnvGPUCount                        = "GPU_COUNT"
	EnvMemoryLimit                     = "MEMORY_LIMIT"
	StatusMessageScaledToZero          = "StockPool is scaled to zero replicas."
	StatusMessageDeploymentReady       = "Deployment has enough available replicas."
	StatusMessageDeploymentProgressing = "Waiting for runtime workers to become available."
	DefaultRuntimeImage                = "busybox:1.36"
	NVIDIAGPUResourceName              = "nvidia.com/gpu"
	RuntimeWorkerContainerName         = "runtime-worker"
	RuntimeCommandShell                = "sh"
	RuntimeCommandShellFlag            = "-c"
	RuntimeCommandSleep                = "sleep 3600"
	StockPoolResourceNamePrefix        = "pool-"
	AnnotationOperationID              = "runtime.lokiwager.io/operation-id"
	AnnotationRequestHash              = "runtime.lokiwager.io/request-hash"
)

type StockPoolTemplate struct {
	Command []string            `json:"command,omitempty"`
	Args    []string            `json:"args,omitempty"`
	Envs    []StockPoolEnvVar   `json:"envs,omitempty"`
	Ports   []StockPoolPortSpec `json:"ports,omitempty"`
}

type StockPoolEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type StockPoolPortSpec struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port     int32           `json:"port"`
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

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

	// Template is the first runtime-facing slice of pod configuration that we expose through the API.
	Template StockPoolTemplate `json:"template,omitempty"`
}

// StockPoolStatus defines the observed state of StockPool.
type StockPoolStatus struct {
	Available          int32              `json:"available,omitempty"`
	Allocated          int32              `json:"allocated,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time        `json:"lastSyncTime,omitempty"`
	ServiceName        string             `json:"serviceName,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
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
