package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

const unknownMetricsPhase = "Unknown"

// RuntimeMetricsSnapshot is the scrape-time view exported to Prometheus collectors.
type RuntimeMetricsSnapshot struct {
	KubernetesConnected bool
	NodeCount           int
	ReadyNodeCount      int
	TotalGPUCapacity    int64
	TotalGPUAllocatable int64
	GPUProducts         []domain.GPUProductHealth
	Nodes               []RuntimeNodeMetrics
	UnitCounts          map[RuntimeUnitPhaseKey]int
	StoragePhases       map[string]int
	StoragePrepare      map[string]int
	StorageAccessor     map[string]int
}

// RuntimeUnitPhaseKey groups unit counts by lifecycle and phase.
type RuntimeUnitPhaseKey struct {
	Lifecycle string
	Phase     string
}

// RuntimeNodeMetrics carries one node readiness and GPU inventory snapshot.
type RuntimeNodeMetrics struct {
	Name           string
	Ready          bool
	GPUProduct     string
	GPUCapacity    int64
	GPUAllocatable int64
}

type nodeInventorySnapshot struct {
	Connected           bool
	NodeCount           int
	ReadyNodeCount      int
	TotalGPUCapacity    int64
	TotalGPUAllocatable int64
	GPUProducts         []domain.GPUProductHealth
	Nodes               []RuntimeNodeMetrics
	KubeError           string
}

// RuntimeMetricsSnapshot returns a partial-but-useful snapshot even when one backend fails.
func (s *Service) RuntimeMetricsSnapshot(ctx context.Context) (RuntimeMetricsSnapshot, error) {
	snapshot := RuntimeMetricsSnapshot{
		UnitCounts:      map[RuntimeUnitPhaseKey]int{},
		StoragePhases:   map[string]int{},
		StoragePrepare:  map[string]int{},
		StorageAccessor: map[string]int{},
	}

	var resultErr error

	nodeInventory, err := s.collectNodeInventory(ctx)
	if err != nil {
		resultErr = errors.Join(resultErr, err)
	}
	snapshot.KubernetesConnected = nodeInventory.Connected
	snapshot.NodeCount = nodeInventory.NodeCount
	snapshot.ReadyNodeCount = nodeInventory.ReadyNodeCount
	snapshot.TotalGPUCapacity = nodeInventory.TotalGPUCapacity
	snapshot.TotalGPUAllocatable = nodeInventory.TotalGPUAllocatable
	snapshot.GPUProducts = append([]domain.GPUProductHealth(nil), nodeInventory.GPUProducts...)
	snapshot.Nodes = append([]RuntimeNodeMetrics(nil), nodeInventory.Nodes...)

	unitCounts, _, _, err := s.collectRuntimeUnitPhaseCounts(ctx)
	if err != nil {
		resultErr = errors.Join(resultErr, err)
	}
	for key, value := range unitCounts {
		snapshot.UnitCounts[key] = value
	}

	storagePhases, storagePrepare, storageAccessor, err := s.collectStoragePhaseCounts(ctx)
	if err != nil {
		resultErr = errors.Join(resultErr, err)
	}
	for key, value := range storagePhases {
		snapshot.StoragePhases[key] = value
	}
	for key, value := range storagePrepare {
		snapshot.StoragePrepare[key] = value
	}
	for key, value := range storageAccessor {
		snapshot.StorageAccessor[key] = value
	}

	return snapshot, resultErr
}

func (s *Service) collectNodeInventory(ctx context.Context) (nodeInventorySnapshot, error) {
	if s.kube == nil {
		return nodeInventorySnapshot{}, nil
	}

	nodes, err := s.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nodeInventorySnapshot{KubeError: err.Error()}, err
	}

	snapshot := nodeInventorySnapshot{
		Connected: true,
		NodeCount: len(nodes.Items),
		Nodes:     make([]RuntimeNodeMetrics, 0, len(nodes.Items)),
	}

	productIndex := map[string]*domain.GPUProductHealth{}
	for i := range nodes.Items {
		item := nodes.Items[i]
		ready := isNodeReady(item)
		if ready {
			snapshot.ReadyNodeCount++
		}

		gpuCapacity := gpuQuantity(item.Status.Capacity)
		gpuAllocatable := gpuQuantity(item.Status.Allocatable)
		product := strings.TrimSpace(item.Labels[runtimev1alpha1.NVIDIAGPUProductLabelKey])

		snapshot.Nodes = append(snapshot.Nodes, RuntimeNodeMetrics{
			Name:           item.Name,
			Ready:          ready,
			GPUProduct:     product,
			GPUCapacity:    gpuCapacity,
			GPUAllocatable: gpuAllocatable,
		})
		snapshot.TotalGPUCapacity += gpuCapacity
		snapshot.TotalGPUAllocatable += gpuAllocatable

		if gpuCapacity <= 0 && gpuAllocatable <= 0 {
			continue
		}
		if product == "" {
			product = "unknown"
		}
		productHealth := productIndex[product]
		if productHealth == nil {
			productHealth = &domain.GPUProductHealth{Product: product}
			productIndex[product] = productHealth
		}
		productHealth.NodeCount++
		productHealth.Capacity += gpuCapacity
		productHealth.Allocatable += gpuAllocatable
	}

	if len(productIndex) > 0 {
		productKeys := make([]string, 0, len(productIndex))
		for key := range productIndex {
			productKeys = append(productKeys, key)
		}
		sort.Strings(productKeys)
		snapshot.GPUProducts = make([]domain.GPUProductHealth, 0, len(productKeys))
		for _, key := range productKeys {
			snapshot.GPUProducts = append(snapshot.GPUProducts, *productIndex[key])
		}
	}

	sort.Slice(snapshot.Nodes, func(i, j int) bool {
		return snapshot.Nodes[i].Name < snapshot.Nodes[j].Name
	})
	return snapshot, nil
}

func (s *Service) collectRuntimeUnitPhaseCounts(ctx context.Context) (map[RuntimeUnitPhaseKey]int, int, int, error) {
	counts := map[RuntimeUnitPhaseKey]int{}
	if s.operator == nil {
		return counts, 0, 0, nil
	}

	var units runtimev1alpha1.GPUUnitList
	if err := s.operator.List(ctx, &units); err != nil {
		return counts, 0, 0, err
	}

	stockUnits := 0
	activeUnits := 0
	for i := range units.Items {
		item := &units.Items[i]
		lifecycle := lifecycleForUnit(item)
		phase := normalizeMetricsPhase(item.Status.Phase)
		counts[RuntimeUnitPhaseKey{Lifecycle: lifecycle, Phase: phase}]++
		if isStockGPUUnit(item) {
			stockUnits++
			continue
		}
		activeUnits++
	}

	return counts, stockUnits, activeUnits, nil
}

func (s *Service) collectStoragePhaseCounts(ctx context.Context) (map[string]int, map[string]int, map[string]int, error) {
	phases := map[string]int{}
	prepare := map[string]int{}
	accessor := map[string]int{}
	if s.operator == nil {
		return phases, prepare, accessor, nil
	}

	var storages runtimev1alpha1.GPUStorageList
	if err := s.operator.List(ctx, &storages); err != nil {
		return phases, prepare, accessor, err
	}

	for i := range storages.Items {
		phases[normalizeMetricsPhase(storages.Items[i].Status.Phase)]++
		prepare[normalizeMetricsPhase(storages.Items[i].Status.Prepare.Phase)]++
		accessor[normalizeMetricsPhase(storages.Items[i].Status.Accessor.Phase)]++
	}

	return phases, prepare, accessor, nil
}

func isNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func gpuQuantity(resources corev1.ResourceList) int64 {
	if len(resources) == 0 {
		return 0
	}
	qty, ok := resources[corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName)]
	if !ok {
		return 0
	}
	return qty.Value()
}

func normalizeMetricsPhase(phase string) string {
	trimmed := strings.TrimSpace(phase)
	if trimmed == "" {
		return unknownMetricsPhase
	}
	return trimmed
}
