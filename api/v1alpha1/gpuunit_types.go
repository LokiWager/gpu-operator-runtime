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

	LifecycleStock    = "stock"
	LifecycleInstance = "instance"

	ReasonInvalidSpec              = "InvalidSpec"
	ReasonPodStartupFailed         = "PodStartupFailed"
	ReasonPodStatusSyncFailed      = "PodStatusSyncFailed"
	ReasonAccessConfigInvalid      = "AccessConfigInvalid"
	ReasonStockNotReady            = "StockNotReady"
	ReasonStockReady               = "StockReady"
	ReasonUnitProgressing          = "UnitProgressing"
	ReasonUnitReady                = "UnitReady"
	ReasonUnitServiceSyncFailed    = "UnitServiceSyncFailed"
	ReasonUnitDeploymentSyncFailed = "UnitDeploymentSyncFailed"
	ReasonStockClaimed             = "StockClaimed"
	ReasonStockConsumed            = "StockConsumed"

	LabelAppNameKey   = "app.kubernetes.io/name"
	LabelManagedByKey = "app.kubernetes.io/managed-by"
	LabelUnitKey      = "runtime.lokiwager.io/unit"

	LabelAppNameValue   = "gpu-runtime-unit"
	LabelManagedByValue = "gpu-runtime-operator"

	EnvSpecName    = "SPEC_NAME"
	EnvUnitName    = "UNIT_NAME"
	EnvGPUCount    = "GPU_COUNT"
	EnvMemoryLimit = "MEMORY_LIMIT"

	DefaultAccessScheme     = "http"
	StatusMessageUnitReady  = "GPU unit runtime is ready."
	StatusMessageUnitWait   = "Waiting for the GPU unit runtime to become ready."
	StatusMessageStockReady = "Stock unit is ready to be consumed."
	StatusMessageStockWait  = "Waiting for the stock unit runtime to become ready."

	DefaultRuntimeImage        = "busybox:1.36"
	StockReservationImage      = DefaultRuntimeImage
	NVIDIAGPUResourceName      = "nvidia.com/gpu"
	RuntimeWorkerContainerName = "runtime-worker"
	RuntimeCommandShell        = "sh"
	RuntimeCommandShellFlag    = "-c"
	RuntimeCommandSleep        = "sleep 3600"

	GPUUnitNamePrefix        = "unit-"
	DefaultStockNamespace    = "runtime-stock"
	DefaultInstanceNamespace = "runtime-instance"

	AnnotationOperationID          = "runtime.lokiwager.io/operation-id"
	AnnotationRequestHash          = "runtime.lokiwager.io/request-hash"
	AnnotationStockClaimID         = "runtime.lokiwager.io/stock-claim-id"
	AnnotationStockReplicas        = "runtime.lokiwager.io/stock-replicas"
	AnnotationStockOrdinal         = "runtime.lokiwager.io/stock-ordinal"
	AnnotationSourceStockName      = "runtime.lokiwager.io/source-stock-name"
	AnnotationSourceStockNamespace = "runtime.lokiwager.io/source-stock-namespace"
)

// GPUUnitTemplate captures the runtime-facing slice of the pod spec.
type GPUUnitTemplate struct {
	Command []string          `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Envs    []GPUUnitEnvVar   `json:"envs,omitempty"`
	Ports   []GPUUnitPortSpec `json:"ports,omitempty"`
}

// GPUUnitEnvVar describes one environment variable injected into the runtime container.
type GPUUnitEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// GPUUnitPortSpec declares one named runtime port and its transport protocol.
type GPUUnitPortSpec struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port     int32           `json:"port"`
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// GPUUnitAccess defines how callers should reach the runtime once it is ready.
type GPUUnitAccess struct {
	PrimaryPort string `json:"primaryPort,omitempty"`
	Scheme      string `json:"scheme,omitempty"`
}

// GPUUnitSpec defines the desired state of GPUUnit.
type GPUUnitSpec struct {
	// SpecName is the requested runtime flavor, for example "g1.1".
	SpecName string `json:"specName"`

	// Image is the runtime image used by the unit workload.
	Image string `json:"image,omitempty"`

	// Memory is the memory request/limit for the unit workload.
	Memory string `json:"memory,omitempty"`

	// GPU is the number of GPUs requested by the unit workload.
	// +kubebuilder:validation:Minimum=0
	GPU int32 `json:"gpu,omitempty"`

	// Template is the runtime-facing pod slice owned by this unit.
	Template GPUUnitTemplate `json:"template,omitempty"`

	// Access describes the primary runtime endpoint.
	Access GPUUnitAccess `json:"access,omitempty"`
}

// GPUUnitStatus defines the observed state of GPUUnit.
type GPUUnitStatus struct {
	ReadyReplicas      int32              `json:"readyReplicas,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time        `json:"lastSyncTime,omitempty"`
	ServiceName        string             `json:"serviceName,omitempty"`
	AccessURL          string             `json:"accessURL,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Spec",type=string,JSONPath=`.spec.specName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.status.accessURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GPUUnit is the schema for one stock or active runtime unit.
type GPUUnit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPUUnitSpec   `json:"spec,omitempty"`
	Status GPUUnitStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GPUUnitList contains a list of GPUUnit objects.
type GPUUnitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUUnit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUUnit{}, &GPUUnitList{})
}
