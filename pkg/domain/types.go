package domain

import "time"

type StockStatus string

const (
	StockStatusAvailable StockStatus = "available"
	StockStatusAllocated StockStatus = "allocated"
)

type VMStatus string

const (
	VMStatusPending VMStatus = "pending"
	VMStatusRunning VMStatus = "running"
	VMStatusStopped VMStatus = "stopped"
)

type StockSpec struct {
	Name    string `json:"name"`
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	GPUType string `json:"gpuType,omitempty"`
	GPUNum  int    `json:"gpuNum,omitempty"`
}

type Stock struct {
	ID        string      `json:"id"`
	Spec      StockSpec   `json:"spec"`
	Status    StockStatus `json:"status"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

type VM struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenantID,omitempty"`
	TenantName string    `json:"tenantName,omitempty"`
	Image      string    `json:"image,omitempty"`
	SpecName   string    `json:"specName"`
	StockID    string    `json:"stockID"`
	Status     VMStatus  `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt"`
}

type RuntimeSummary struct {
	TotalStocks     int `json:"totalStocks"`
	AvailableStocks int `json:"availableStocks"`
	AllocatedStocks int `json:"allocatedStocks"`
	TotalVMs        int `json:"totalVMs"`
	RunningVMs      int `json:"runningVMs"`
}

type HealthStatus struct {
	StartedAt           time.Time      `json:"startedAt"`
	UptimeSeconds       int64          `json:"uptimeSeconds"`
	KubernetesConnected bool           `json:"kubernetesConnected"`
	NodeCount           int            `json:"nodeCount"`
	KubeError           string         `json:"kubeError,omitempty"`
	Summary             RuntimeSummary `json:"summary"`
}
