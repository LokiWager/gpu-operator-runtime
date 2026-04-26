package metrics

import (
	"context"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/loki/gpu-operator-runtime/pkg/service"
)

const runtimeMetricsTimeout = 5 * time.Second

// RuntimeCollector scrapes live runtime health and inventory into Prometheus gauges.
type RuntimeCollector struct {
	service *service.Service
	timeout time.Duration

	scrapeSuccessDesc        *prometheus.Desc
	kubernetesUpDesc         *prometheus.Desc
	nodeTotalDesc            *prometheus.Desc
	nodeReadyTotalDesc       *prometheus.Desc
	nodeReadyDesc            *prometheus.Desc
	nodeGPUCapacityDesc      *prometheus.Desc
	nodeGPUAllocatableDesc   *prometheus.Desc
	nvidiaUpDesc             *prometheus.Desc
	nvidiaDeviceTotalDesc    *prometheus.Desc
	nvidiaMemoryUsedDesc     *prometheus.Desc
	nvidiaMemoryFreeDesc     *prometheus.Desc
	nvidiaMemoryTotalDesc    *prometheus.Desc
	nvidiaUtilizationDesc    *prometheus.Desc
	unitCountDesc            *prometheus.Desc
	storageCountDesc         *prometheus.Desc
	storagePrepareCountDesc  *prometheus.Desc
	storageAccessorCountDesc *prometheus.Desc
}

// RegisterRuntimeCollector registers the runtime collector on the shared controller-runtime registry.
func RegisterRuntimeCollector(svc *service.Service) error {
	err := ctrlmetrics.Registry.Register(NewRuntimeCollector(svc, runtimeMetricsTimeout))
	if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
		return nil
	}
	return err
}

// NewRuntimeCollector builds a live Prometheus collector backed by service snapshots.
func NewRuntimeCollector(svc *service.Service, timeout time.Duration) *RuntimeCollector {
	return &RuntimeCollector{
		service: svc,
		timeout: timeout,
		scrapeSuccessDesc: prometheus.NewDesc(
			"runtime_metrics_scrape_success",
			"Whether the most recent runtime metrics collection succeeded.",
			nil,
			nil,
		),
		kubernetesUpDesc: prometheus.NewDesc(
			"runtime_kubernetes_up",
			"Whether the runtime manager can currently reach the Kubernetes API.",
			nil,
			nil,
		),
		nodeTotalDesc: prometheus.NewDesc(
			"runtime_kubernetes_nodes_total",
			"Total Kubernetes nodes seen by the runtime manager.",
			nil,
			nil,
		),
		nodeReadyTotalDesc: prometheus.NewDesc(
			"runtime_kubernetes_nodes_ready",
			"Ready Kubernetes nodes seen by the runtime manager.",
			nil,
			nil,
		),
		nodeReadyDesc: prometheus.NewDesc(
			"runtime_kubernetes_node_ready",
			"Per-node readiness, where 1 means ready and 0 means not ready.",
			[]string{"node"},
			nil,
		),
		nodeGPUCapacityDesc: prometheus.NewDesc(
			"runtime_kubernetes_node_gpu_capacity",
			"Advertised GPU capacity on each node, grouped by Nvidia product label.",
			[]string{"node", "gpu_product"},
			nil,
		),
		nodeGPUAllocatableDesc: prometheus.NewDesc(
			"runtime_kubernetes_node_gpu_allocatable",
			"Allocatable GPU capacity on each node, grouped by Nvidia product label.",
			[]string{"node", "gpu_product"},
			nil,
		),
		nvidiaUpDesc: prometheus.NewDesc(
			"runtime_nvidia_metrics_up",
			"Whether the runtime manager can currently scrape Nvidia device metrics.",
			nil,
			nil,
		),
		nvidiaDeviceTotalDesc: prometheus.NewDesc(
			"runtime_nvidia_gpu_devices_total",
			"Total Nvidia GPU devices observed from the configured telemetry endpoint.",
			nil,
			nil,
		),
		nvidiaMemoryUsedDesc: prometheus.NewDesc(
			"runtime_nvidia_gpu_memory_used_mib",
			"Used GPU frame-buffer memory in MiB for each device.",
			[]string{"node", "gpu", "uuid", "gpu_product"},
			nil,
		),
		nvidiaMemoryFreeDesc: prometheus.NewDesc(
			"runtime_nvidia_gpu_memory_free_mib",
			"Free GPU frame-buffer memory in MiB for each device.",
			[]string{"node", "gpu", "uuid", "gpu_product"},
			nil,
		),
		nvidiaMemoryTotalDesc: prometheus.NewDesc(
			"runtime_nvidia_gpu_memory_total_mib",
			"Total GPU frame-buffer memory in MiB for each device.",
			[]string{"node", "gpu", "uuid", "gpu_product"},
			nil,
		),
		nvidiaUtilizationDesc: prometheus.NewDesc(
			"runtime_nvidia_gpu_utilization_percent",
			"GPU utilization percent for each device.",
			[]string{"node", "gpu", "uuid", "gpu_product"},
			nil,
		),
		unitCountDesc: prometheus.NewDesc(
			"runtime_gpu_units",
			"GPU units grouped by lifecycle and controller phase.",
			[]string{"lifecycle", "phase"},
			nil,
		),
		storageCountDesc: prometheus.NewDesc(
			"runtime_gpu_storages",
			"GPU storages grouped by controller phase.",
			[]string{"phase"},
			nil,
		),
		storagePrepareCountDesc: prometheus.NewDesc(
			"runtime_gpu_storage_prepare",
			"GPU storages grouped by prepare workflow phase.",
			[]string{"phase"},
			nil,
		),
		storageAccessorCountDesc: prometheus.NewDesc(
			"runtime_gpu_storage_accessor",
			"GPU storages grouped by accessor phase.",
			[]string{"phase"},
			nil,
		),
	}
}

// Describe emits the static metric descriptors.
func (c *RuntimeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.scrapeSuccessDesc
	ch <- c.kubernetesUpDesc
	ch <- c.nodeTotalDesc
	ch <- c.nodeReadyTotalDesc
	ch <- c.nodeReadyDesc
	ch <- c.nodeGPUCapacityDesc
	ch <- c.nodeGPUAllocatableDesc
	ch <- c.nvidiaUpDesc
	ch <- c.nvidiaDeviceTotalDesc
	ch <- c.nvidiaMemoryUsedDesc
	ch <- c.nvidiaMemoryFreeDesc
	ch <- c.nvidiaMemoryTotalDesc
	ch <- c.nvidiaUtilizationDesc
	ch <- c.unitCountDesc
	ch <- c.storageCountDesc
	ch <- c.storagePrepareCountDesc
	ch <- c.storageAccessorCountDesc
}

// Collect scrapes the service and emits one Prometheus sample per metric series.
func (c *RuntimeCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	snapshot, err := c.service.RuntimeMetricsSnapshot(ctx)
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccessDesc, prometheus.GaugeValue, boolToFloat64(err == nil))
	ch <- prometheus.MustNewConstMetric(c.kubernetesUpDesc, prometheus.GaugeValue, boolToFloat64(snapshot.KubernetesConnected))
	ch <- prometheus.MustNewConstMetric(c.nodeTotalDesc, prometheus.GaugeValue, float64(snapshot.NodeCount))
	ch <- prometheus.MustNewConstMetric(c.nodeReadyTotalDesc, prometheus.GaugeValue, float64(snapshot.ReadyNodeCount))
	ch <- prometheus.MustNewConstMetric(c.nvidiaUpDesc, prometheus.GaugeValue, boolToFloat64(snapshot.NvidiaMetricsConnected))
	ch <- prometheus.MustNewConstMetric(c.nvidiaDeviceTotalDesc, prometheus.GaugeValue, float64(snapshot.GPUDeviceCount))

	for _, node := range snapshot.Nodes {
		ch <- prometheus.MustNewConstMetric(c.nodeReadyDesc, prometheus.GaugeValue, boolToFloat64(node.Ready), node.Name)
		if node.GPUCapacity <= 0 && node.GPUAllocatable <= 0 {
			continue
		}
		product := node.GPUProduct
		if product == "" {
			product = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(c.nodeGPUCapacityDesc, prometheus.GaugeValue, float64(node.GPUCapacity), node.Name, product)
		ch <- prometheus.MustNewConstMetric(c.nodeGPUAllocatableDesc, prometheus.GaugeValue, float64(node.GPUAllocatable), node.Name, product)
	}
	for _, device := range snapshot.GPUDevices {
		product := device.Product
		if product == "" {
			product = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(c.nvidiaMemoryUsedDesc, prometheus.GaugeValue, device.UsedMemoryMiB, device.NodeName, device.GPU, device.UUID, product)
		ch <- prometheus.MustNewConstMetric(c.nvidiaMemoryFreeDesc, prometheus.GaugeValue, device.FreeMemoryMiB, device.NodeName, device.GPU, device.UUID, product)
		ch <- prometheus.MustNewConstMetric(c.nvidiaMemoryTotalDesc, prometheus.GaugeValue, device.TotalMemoryMiB, device.NodeName, device.GPU, device.UUID, product)
		ch <- prometheus.MustNewConstMetric(c.nvidiaUtilizationDesc, prometheus.GaugeValue, device.UtilizationPercent, device.NodeName, device.GPU, device.UUID, product)
	}

	unitKeys := make([]service.RuntimeUnitPhaseKey, 0, len(snapshot.UnitCounts))
	for key := range snapshot.UnitCounts {
		unitKeys = append(unitKeys, key)
	}
	sort.Slice(unitKeys, func(i, j int) bool {
		if unitKeys[i].Lifecycle == unitKeys[j].Lifecycle {
			return unitKeys[i].Phase < unitKeys[j].Phase
		}
		return unitKeys[i].Lifecycle < unitKeys[j].Lifecycle
	})
	for _, key := range unitKeys {
		ch <- prometheus.MustNewConstMetric(
			c.unitCountDesc,
			prometheus.GaugeValue,
			float64(snapshot.UnitCounts[key]),
			key.Lifecycle,
			key.Phase,
		)
	}

	collectPhaseMap(ch, c.storageCountDesc, snapshot.StoragePhases)
	collectPhaseMap(ch, c.storagePrepareCountDesc, snapshot.StoragePrepare)
	collectPhaseMap(ch, c.storageAccessorCountDesc, snapshot.StorageAccessor)
}

func collectPhaseMap(ch chan<- prometheus.Metric, desc *prometheus.Desc, values map[string]int) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(values[key]), key)
	}
}

func boolToFloat64(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
