package domain

import (
	"encoding/json"
	"time"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

// HealthStatus summarizes process and cluster health for the HTTP health endpoint.
type HealthStatus struct {
	StartedAt                    time.Time          `json:"startedAt"`
	UptimeSeconds                int64              `json:"uptimeSeconds"`
	KubernetesConnected          bool               `json:"kubernetesConnected"`
	NodeCount                    int                `json:"nodeCount"`
	ReadyNodeCount               int                `json:"readyNodeCount"`
	KubeError                    string             `json:"kubeError,omitempty"`
	ActiveUnitCount              int                `json:"activeUnitCount"`
	TotalGPUCapacity             int64              `json:"totalGPUCapacity"`
	TotalGPUAllocatable          int64              `json:"totalGPUAllocatable"`
	GPUProducts                  []GPUProductHealth `json:"gpuProducts,omitempty"`
	NvidiaMetricsConnected       bool               `json:"nvidiaMetricsConnected"`
	NvidiaMetricsError           string             `json:"nvidiaMetricsError,omitempty"`
	GPUDeviceCount               int                `json:"gpuDeviceCount"`
	TotalGPUMemoryMiB            float64            `json:"totalGpuMemoryMiB"`
	UsedGPUMemoryMiB             float64            `json:"usedGpuMemoryMiB"`
	FreeGPUMemoryMiB             float64            `json:"freeGpuMemoryMiB"`
	AverageGPUUtilizationPercent float64            `json:"averageGpuUtilizationPercent"`
}

// GPUProductHealth summarizes GPU inventory grouped by Nvidia product label.
type GPUProductHealth struct {
	Product     string `json:"product"`
	NodeCount   int    `json:"nodeCount"`
	Capacity    int64  `json:"capacity"`
	Allocatable int64  `json:"allocatable"`
}

// RuntimeInventoryStatus is the API-facing allocation view derived from Kubernetes state.
type RuntimeInventoryStatus struct {
	KubernetesConnected bool                     `json:"kubernetesConnected"`
	NodeCount           int                      `json:"nodeCount"`
	ReadyNodeCount      int                      `json:"readyNodeCount"`
	TotalGPUCapacity    int64                    `json:"totalGpuCapacity"`
	TotalGPUAllocatable int64                    `json:"totalGpuAllocatable"`
	DRAClaimCount       int                      `json:"draClaimCount"`
	DRAAllocatedClaims  int                      `json:"draAllocatedClaims"`
	DRAAllocatedDevices int                      `json:"draAllocatedDevices"`
	DRAResourceSlices   int                      `json:"draResourceSlices"`
	DRADevices          []RuntimeDRADeviceStatus `json:"draDevices,omitempty"`
	GPUProducts         []GPUProductHealth       `json:"gpuProducts,omitempty"`
	ResourceQuotas      []RuntimeQuotaStatus     `json:"resourceQuotas,omitempty"`
}

// RuntimeQuotaStatus summarizes ResourceQuota hard/used values relevant to runtime allocation.
type RuntimeQuotaStatus struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Hard      map[string]string `json:"hard,omitempty"`
	Used      map[string]string `json:"used,omitempty"`
}

// RuntimeDRADeviceStatus summarizes one observed DRA allocation from ResourceClaim.status.
type RuntimeDRADeviceStatus struct {
	ClaimName        string            `json:"claimName"`
	Namespace        string            `json:"namespace"`
	Request          string            `json:"request,omitempty"`
	Driver           string            `json:"driver,omitempty"`
	Pool             string            `json:"pool,omitempty"`
	Device           string            `json:"device,omitempty"`
	ShareID          string            `json:"shareID,omitempty"`
	ConsumedCapacity map[string]string `json:"consumedCapacity,omitempty"`
}

// GPUUnitRuntime is the API-facing runtime view of a GPUUnit object.
type GPUUnitRuntime struct {
	Name               string                                  `json:"name"`
	Namespace          string                                  `json:"namespace"`
	Lifecycle          string                                  `json:"lifecycle"`
	PackageID          string                                  `json:"packageID,omitempty"`
	SpecName           string                                  `json:"specName"`
	Image              string                                  `json:"image,omitempty"`
	CPU                string                                  `json:"cpu,omitempty"`
	Memory             string                                  `json:"memory,omitempty"`
	GPU                int32                                   `json:"gpu,omitempty"`
	Allocation         runtimev1alpha1.GPUUnitAllocationSpec   `json:"allocation,omitempty"`
	Template           runtimev1alpha1.GPUUnitTemplate         `json:"template,omitempty"`
	Access             runtimev1alpha1.GPUUnitAccess           `json:"access,omitempty"`
	SSH                runtimev1alpha1.GPUUnitSSHSpec          `json:"ssh,omitempty"`
	Serverless         runtimev1alpha1.GPUUnitServerlessSpec   `json:"serverless,omitempty"`
	StorageMounts      []runtimev1alpha1.GPUUnitStorageMount   `json:"storageMounts,omitempty"`
	Phase              string                                  `json:"phase"`
	ReadyReplicas      int32                                   `json:"readyReplicas"`
	ObservedGeneration int64                                   `json:"observedGeneration"`
	LastSyncTime       time.Time                               `json:"lastSyncTime,omitempty"`
	ServiceName        string                                  `json:"serviceName,omitempty"`
	AccessURL          string                                  `json:"accessURL,omitempty"`
	SSHStatus          runtimev1alpha1.GPUUnitSSHStatus        `json:"sshStatus,omitempty"`
	ServerlessStatus   runtimev1alpha1.GPUUnitServerlessStatus `json:"serverlessStatus,omitempty"`
	DRAStatus          runtimev1alpha1.GPUUnitDRAStatus        `json:"draStatus,omitempty"`
	Reason             string                                  `json:"reason,omitempty"`
	Message            string                                  `json:"message,omitempty"`
}

// ServerlessInvocationAck is the API-facing acknowledgement for one queued serverless invocation.
type ServerlessInvocationAck struct {
	InvocationID        string    `json:"invocationID"`
	ServerlessRequestID string    `json:"serverlessRequestID"`
	Mode                string    `json:"mode"`
	Subject             string    `json:"subject"`
	ResultSubject       string    `json:"resultSubject"`
	MetricsSubject      string    `json:"metricsSubject"`
	Stream              string    `json:"stream"`
	Sequence            uint64    `json:"sequence"`
	Duplicate           bool      `json:"duplicate"`
	AcceptedAt          time.Time `json:"acceptedAt"`
}

// ServerlessInvocationResult is the API-facing sync execution result returned once the worker-side reply path publishes a result.
type ServerlessInvocationResult struct {
	InvocationID        string            `json:"invocationID"`
	ServerlessRequestID string            `json:"serverlessRequestID"`
	Mode                string            `json:"mode"`
	State               string            `json:"state,omitempty"`
	FailureClass        string            `json:"failureClass,omitempty"`
	WorkerName          string            `json:"workerName,omitempty"`
	WorkerNamespace     string            `json:"workerNamespace,omitempty"`
	StatusCode          int               `json:"statusCode,omitempty"`
	ContentType         string            `json:"contentType,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Body                json.RawMessage   `json:"body,omitempty"`
	Error               string            `json:"error,omitempty"`
	StartedAt           time.Time         `json:"startedAt,omitempty"`
	CompletedAt         time.Time         `json:"completedAt"`
}

// GPUStorageRuntime is the API-facing view of a GPUStorage object.
type GPUStorageRuntime struct {
	Name               string                                   `json:"name"`
	Namespace          string                                   `json:"namespace"`
	Size               string                                   `json:"size"`
	StorageClassName   string                                   `json:"storageClassName,omitempty"`
	Prepare            runtimev1alpha1.GPUStoragePrepareSpec    `json:"prepare,omitempty"`
	Accessor           runtimev1alpha1.GPUStorageAccessorSpec   `json:"accessor,omitempty"`
	ClaimName          string                                   `json:"claimName,omitempty"`
	Capacity           string                                   `json:"capacity,omitempty"`
	MountedBy          []string                                 `json:"mountedBy,omitempty"`
	Phase              string                                   `json:"phase"`
	PrepareStatus      runtimev1alpha1.GPUStoragePrepareStatus  `json:"prepareStatus,omitempty"`
	AccessorStatus     runtimev1alpha1.GPUStorageAccessorStatus `json:"accessorStatus,omitempty"`
	ObservedGeneration int64                                    `json:"observedGeneration"`
	LastSyncTime       time.Time                                `json:"lastSyncTime,omitempty"`
	Reason             string                                   `json:"reason,omitempty"`
	Message            string                                   `json:"message,omitempty"`
}
