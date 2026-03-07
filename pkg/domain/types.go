package domain

import "time"

type HealthStatus struct {
	StartedAt           time.Time `json:"startedAt"`
	UptimeSeconds       int64     `json:"uptimeSeconds"`
	KubernetesConnected bool      `json:"kubernetesConnected"`
	NodeCount           int       `json:"nodeCount"`
	KubeError           string    `json:"kubeError,omitempty"`
	StockPoolCount      int       `json:"stockPoolCount"`
}

type OperatorJobStatus string

const (
	OperatorJobPending   OperatorJobStatus = "pending"
	OperatorJobRunning   OperatorJobStatus = "running"
	OperatorJobSucceeded OperatorJobStatus = "succeeded"
	OperatorJobFailed    OperatorJobStatus = "failed"
)

type OperatorJob struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	Status          OperatorJobStatus `json:"status"`
	Error           string            `json:"error,omitempty"`
	ObjectName      string            `json:"objectName,omitempty"`
	ObjectNamespace string            `json:"objectNamespace,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

type StockPoolRuntime struct {
	Name               string    `json:"name"`
	Namespace          string    `json:"namespace"`
	SpecName           string    `json:"specName"`
	DesiredReplicas    int32     `json:"desiredReplicas"`
	AvailableReplicas  int32     `json:"availableReplicas"`
	AllocatedReplicas  int32     `json:"allocatedReplicas"`
	Phase              string    `json:"phase"`
	ObservedGeneration int64     `json:"observedGeneration"`
	LastSyncTime       time.Time `json:"lastSyncTime,omitempty"`
}
