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

	UnitSSHPhaseDisabled = "Disabled"
	UnitSSHPhasePending  = "Pending"
	UnitSSHPhaseReady    = "Ready"
	UnitSSHPhaseFailed   = "Failed"

	UnitServerlessPhaseDisabled = "Disabled"
	UnitServerlessPhasePending  = "Pending"
	UnitServerlessPhaseReady    = "Ready"
	UnitServerlessPhaseFailed   = "Failed"

	ConditionReady = "Ready"

	LifecycleInstance = "instance"

	ReasonInvalidSpec                 = "InvalidSpec"
	ReasonPodStartupFailed            = "PodStartupFailed"
	ReasonPodStatusSyncFailed         = "PodStatusSyncFailed"
	ReasonAccessConfigInvalid         = "AccessConfigInvalid"
	ReasonSSHConfigInvalid            = "SSHConfigInvalid"
	ReasonServerlessConfigInvalid     = "ServerlessConfigInvalid"
	ReasonUnitProgressing             = "UnitProgressing"
	ReasonUnitReady                   = "UnitReady"
	ReasonUnitSSHPending              = "UnitSSHPending"
	ReasonUnitSSHReady                = "UnitSSHReady"
	ReasonUnitSSHFailed               = "UnitSSHFailed"
	ReasonUnitServerlessPending       = "UnitServerlessPending"
	ReasonUnitServerlessReady         = "UnitServerlessReady"
	ReasonUnitServerlessFailed        = "UnitServerlessFailed"
	ReasonUnitServiceSyncFailed       = "UnitServiceSyncFailed"
	ReasonUnitNetworkPolicySyncFailed = "UnitNetworkPolicySyncFailed"
	ReasonUnitDeploymentSyncFailed    = "UnitDeploymentSyncFailed"
	ReasonUnitDRAClaimSyncFailed      = "UnitDRAClaimSyncFailed"
	ReasonStorageMountInvalid         = "StorageMountInvalid"
	ReasonStorageNotReady             = "StorageNotReady"

	LabelAppNameKey   = "app.kubernetes.io/name"
	LabelManagedByKey = "app.kubernetes.io/managed-by"
	LabelUnitKey      = "runtime.lokiwager.io/unit"
	LabelStorageKey   = "runtime.lokiwager.io/storage"

	LabelAppNameValue   = "gpu-runtime-unit"
	LabelManagedByValue = "gpu-runtime-operator"

	EnvSpecName    = "SPEC_NAME"
	EnvUnitName    = "UNIT_NAME"
	EnvGPUCount    = "GPU_COUNT"
	EnvMemoryLimit = "MEMORY_LIMIT"

	DefaultAccessScheme                 = "http"
	StatusMessageUnitReady              = "GPU unit runtime is ready."
	StatusMessageUnitWait               = "Waiting for the GPU unit runtime to become ready."
	StatusMessageUnitStorage            = "Waiting for attached storage to become ready."
	StatusMessageUnitSSHReady           = "GPU unit SSH access is ready."
	StatusMessageUnitSSHPending         = "Waiting for the GPU unit SSH access to become ready."
	StatusMessageUnitSSHDisabled        = "GPU unit SSH access is disabled."
	StatusMessageUnitServerlessReady    = "GPU unit serverless sidecar is ready."
	StatusMessageUnitServerlessPending  = "Waiting for the GPU unit serverless sidecar to become ready."
	StatusMessageUnitServerlessDisabled = "GPU unit serverless sidecar is disabled."

	DefaultRuntimeImage        = "busybox:1.36"
	NVIDIAGPUResourceName      = "nvidia.com/gpu"
	NVIDIAGPUProductLabelKey   = "nvidia.com/gpu.product"
	RuntimeWorkerContainerName = "runtime-worker"
	RuntimeCommandShell        = "sh"
	RuntimeCommandShellFlag    = "-c"
	RuntimeCommandSleep        = "sleep 3600"

	DefaultUnitSSHUsername  = "runtime"
	DefaultUnitSSHPort      = 2222
	DefaultUnitSSHFRPPort   = 7000
	DefaultUnitSSHProxyPort = 1337
	DefaultUnitSSHImage     = "lscr.io/linuxserver/openssh-server:10.2_p1-r0-ls220"
	DefaultUnitSSHFRPImage  = "docker.io/fatedier/frpc:v0.68.0"
	UnitSSHContainerName    = "ssh-server"
	UnitSSHFRPContainerName = "ssh-frpc"

	ConditionSSHReady        = "SSHReady"
	ConditionServerlessReady = "ServerlessReady"

	UnitDRAClaimName        = "gpu"
	UnitDRAClaimRequestName = "gpu"

	ServerlessSidecarContainerName = "serverless-sidecar"

	DefaultServerlessFrameworkSocketDir  = "/tmp/serverless-framework"
	DefaultServerlessFrameworkSocketPath = "/tmp/serverless-framework/framework.sock"
	DefaultServerlessFrameworkInvokePath = "/invoke"
	DefaultServerlessFrameworkHealthPath = "/healthz"

	GPUUnitNamePrefix        = "unit-"
	DefaultInstanceNamespace = "runtime-instance"

	AnnotationOperationID = "runtime.lokiwager.io/operation-id"
	AnnotationRequestHash = "runtime.lokiwager.io/request-hash"
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

// GPUUnitSSHSpec defines the optional user-facing SSH access contract for one runtime unit.
type GPUUnitSSHSpec struct {
	Enabled        bool     `json:"enabled,omitempty"`
	Username       string   `json:"username,omitempty"`
	AuthorizedKeys []string `json:"authorizedKeys,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
	// ServerAddr is the FRP server address that the injected sidecar dials.
	ServerAddr string `json:"serverAddr,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServerPort int32 `json:"serverPort,omitempty"`
	// ConnectHost is the user-facing HTTP CONNECT proxy host used in the ready-to-run SSH command.
	ConnectHost string `json:"connectHost,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ConnectPort int32 `json:"connectPort,omitempty"`
	// DomainSuffix is combined with name + namespace when ClientDomain is omitted.
	DomainSuffix string `json:"domainSuffix,omitempty"`
	ClientName   string `json:"clientName,omitempty"`
	ClientDomain string `json:"clientDomain,omitempty"`
	Token        string `json:"token,omitempty"`
	Image        string `json:"image,omitempty"`
	FRPImage     string `json:"frpImage,omitempty"`
}

// GPUUnitServerlessSpec records the serverless control-plane contract associated with one runtime unit.
type GPUUnitServerlessSpec struct {
	Enabled bool `json:"enabled,omitempty"`
	// RequestID is the control-plane generated serverless request identifier shared by queued invocations and worker pools.
	RequestID string `json:"requestID,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MinAvailableCount int32 `json:"minAvailableCount,omitempty"`
	// +kubebuilder:validation:Minimum=0
	IdleTimeoutSeconds int32 `json:"idleTimeoutSeconds,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MinRequestCount int32                          `json:"minRequestCount,omitempty"`
	Framework       GPUUnitServerlessFrameworkSpec `json:"framework,omitempty"`
}

// GPUUnitServerlessFrameworkSpec defines the pod-local UDS-backed HTTP contract served by the user framework container.
type GPUUnitServerlessFrameworkSpec struct {
	// SocketPath is the shared unix domain socket path used by the sidecar and framework containers.
	SocketPath string `json:"socketPath,omitempty"`
	// InvokePath is the local HTTP path received over the unix domain socket from the sidecar.
	InvokePath string `json:"invokePath,omitempty"`
	// HealthPath is the local HTTP path polled over the unix domain socket before dispatch consumption starts.
	HealthPath string `json:"healthPath,omitempty"`
}

// GPUUnitStorageMount declares one named storage attachment for the runtime container.
type GPUUnitStorageMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// GPUUnitAllocationSpec records the DRA claim that Kubernetes should allocate for this unit.
type GPUUnitAllocationSpec struct {
	// DeviceClassName is the DRA DeviceClass used for package-backed allocation.
	DeviceClassName string `json:"deviceClassName,omitempty" yaml:"deviceClassName,omitempty"`
	// ClaimName is the controller-owned DRA ResourceClaim name.
	ClaimName string `json:"claimName,omitempty" yaml:"claimName,omitempty"`
	// ClaimRequestName is the request name inside the DRA ResourceClaim.
	ClaimRequestName string `json:"claimRequestName,omitempty" yaml:"claimRequestName,omitempty"`
	// Count is the exact number of devices requested from the DeviceClass.
	// +kubebuilder:validation:Minimum=0
	Count int64 `json:"count,omitempty" yaml:"count,omitempty"`
	// Capacity contains DRA consumable capacity requests per device, for example memory or cores.
	Capacity map[string]string `json:"capacity,omitempty" yaml:"capacity,omitempty"`
	// Selectors contains control-plane managed CEL selectors for DRA devices.
	Selectors []string `json:"selectors,omitempty" yaml:"selectors,omitempty"`
}

// GPUUnitSpec defines the desired state of GPUUnit.
type GPUUnitSpec struct {
	// PackageID is the control-plane package that was expanded into this runtime contract.
	PackageID string `json:"packageID,omitempty"`

	// SpecName is the requested runtime flavor, for example "g1.1".
	SpecName string `json:"specName"`

	// Image is the runtime image used by the unit workload.
	Image string `json:"image,omitempty"`

	// CPU is the CPU request/limit for the unit workload.
	CPU string `json:"cpu,omitempty"`

	// Memory is the memory request/limit for the unit workload.
	Memory string `json:"memory,omitempty"`

	// GPU is the number of GPUs requested by the unit workload.
	// +kubebuilder:validation:Minimum=0
	GPU int32 `json:"gpu,omitempty"`

	// Allocation describes the Kubernetes-native allocation path used for this unit.
	Allocation GPUUnitAllocationSpec `json:"allocation,omitempty"`

	// Template is the runtime-facing pod slice owned by this unit.
	Template GPUUnitTemplate `json:"template,omitempty"`

	// Access describes the primary runtime endpoint.
	Access GPUUnitAccess `json:"access,omitempty"`

	// SSH declares whether the platform should inject a shell sidecar and FRP tunnel for user access.
	SSH GPUUnitSSHSpec `json:"ssh,omitempty"`

	// Serverless records the control-plane serverless policy associated with this unit.
	Serverless GPUUnitServerlessSpec `json:"serverless,omitempty"`

	// StorageMounts declares the persistent storage attachments mounted into the runtime.
	StorageMounts []GPUUnitStorageMount `json:"storageMounts,omitempty"`
}

// GPUUnitSSHStatus reports the current SSH exposure state for one active runtime unit.
type GPUUnitSSHStatus struct {
	Phase         string `json:"phase,omitempty"`
	Username      string `json:"username,omitempty"`
	TargetHost    string `json:"targetHost,omitempty"`
	ConnectHost   string `json:"connectHost,omitempty"`
	ConnectPort   int32  `json:"connectPort,omitempty"`
	AccessCommand string `json:"accessCommand,omitempty"`
}

// GPUUnitServerlessStatus reports the observed worker-side queue boundary for one runtime unit.
type GPUUnitServerlessStatus struct {
	Phase           string `json:"phase,omitempty"`
	DispatchSubject string `json:"dispatchSubject,omitempty"`
	SocketPath      string `json:"socketPath,omitempty"`
	InvokePath      string `json:"invokePath,omitempty"`
	HealthPath      string `json:"healthPath,omitempty"`
}

// GPUUnitDRADeviceStatus reports one DRA-allocated device from ResourceClaim status.
type GPUUnitDRADeviceStatus struct {
	Request          string            `json:"request,omitempty"`
	Driver           string            `json:"driver,omitempty"`
	Pool             string            `json:"pool,omitempty"`
	Device           string            `json:"device,omitempty"`
	ShareID          string            `json:"shareID,omitempty"`
	ConsumedCapacity map[string]string `json:"consumedCapacity,omitempty"`
}

// GPUUnitDRAStatus reports the DRA ResourceClaim observed for one runtime unit.
type GPUUnitDRAStatus struct {
	ClaimName string                   `json:"claimName,omitempty"`
	Allocated bool                     `json:"allocated,omitempty"`
	Devices   []GPUUnitDRADeviceStatus `json:"devices,omitempty"`
}

// GPUUnitStatus defines the observed state of GPUUnit.
type GPUUnitStatus struct {
	ReadyReplicas      int32                   `json:"readyReplicas,omitempty"`
	Phase              string                  `json:"phase,omitempty"`
	ObservedGeneration int64                   `json:"observedGeneration,omitempty"`
	LastSyncTime       metav1.Time             `json:"lastSyncTime,omitempty"`
	ServiceName        string                  `json:"serviceName,omitempty"`
	AccessURL          string                  `json:"accessURL,omitempty"`
	SSH                GPUUnitSSHStatus        `json:"ssh,omitempty"`
	Serverless         GPUUnitServerlessStatus `json:"serverless,omitempty"`
	DRA                GPUUnitDRAStatus        `json:"dra,omitempty"`
	Conditions         []metav1.Condition      `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Spec",type=string,JSONPath=`.spec.specName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.status.accessURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GPUUnit is the schema for one active runtime unit.
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
