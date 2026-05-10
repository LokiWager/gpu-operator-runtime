package domain

import (
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
	StockUnitCount               int                `json:"stockUnitCount"`
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

// OperatorJobStatus is the state of an asynchronous operator-side workflow.
type OperatorJobStatus string

const (
	OperatorJobPending   OperatorJobStatus = "pending"
	OperatorJobRunning   OperatorJobStatus = "running"
	OperatorJobSucceeded OperatorJobStatus = "succeeded"
	OperatorJobFailed    OperatorJobStatus = "failed"
)

// OperatorJob describes one asynchronous stock seeding request.
type OperatorJob struct {
	ID              string            `json:"id"`
	OperationID     string            `json:"operationID"`
	Type            string            `json:"type"`
	Status          OperatorJobStatus `json:"status"`
	Error           string            `json:"error,omitempty"`
	ObjectName      string            `json:"objectName,omitempty"`
	ObjectNamespace string            `json:"objectNamespace,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

// GPUUnitRuntime is the API-facing runtime view of a GPUUnit object.
type GPUUnitRuntime struct {
	Name                 string                                `json:"name"`
	Namespace            string                                `json:"namespace"`
	Lifecycle            string                                `json:"lifecycle"`
	SpecName             string                                `json:"specName"`
	SourceStockName      string                                `json:"sourceStockName,omitempty"`
	SourceStockNamespace string                                `json:"sourceStockNamespace,omitempty"`
	Image                string                                `json:"image,omitempty"`
	Memory               string                                `json:"memory,omitempty"`
	GPU                  int32                                 `json:"gpu,omitempty"`
	Template             runtimev1alpha1.GPUUnitTemplate       `json:"template,omitempty"`
	Access               runtimev1alpha1.GPUUnitAccess         `json:"access,omitempty"`
	SSH                  runtimev1alpha1.GPUUnitSSHSpec        `json:"ssh,omitempty"`
	Serverless           runtimev1alpha1.GPUUnitServerlessSpec `json:"serverless,omitempty"`
	StorageMounts        []runtimev1alpha1.GPUUnitStorageMount `json:"storageMounts,omitempty"`
	Phase                string                                `json:"phase"`
	ReadyReplicas        int32                                 `json:"readyReplicas"`
	ObservedGeneration   int64                                 `json:"observedGeneration"`
	LastSyncTime         time.Time                             `json:"lastSyncTime,omitempty"`
	ServiceName          string                                `json:"serviceName,omitempty"`
	AccessURL            string                                `json:"accessURL,omitempty"`
	SSHStatus            runtimev1alpha1.GPUUnitSSHStatus      `json:"sshStatus,omitempty"`
	Reason               string                                `json:"reason,omitempty"`
	Message              string                                `json:"message,omitempty"`
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
