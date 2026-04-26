package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	dcgmFBUsedMetricName  = "DCGM_FI_DEV_FB_USED"
	dcgmFBFreeMetricName  = "DCGM_FI_DEV_FB_FREE"
	dcgmGPUUtilMetricName = "DCGM_FI_DEV_GPU_UTIL"
)

type RuntimeGPUDeviceMetrics struct {
	NodeName           string
	GPU                string
	UUID               string
	Product            string
	UsedMemoryMiB      float64
	FreeMemoryMiB      float64
	TotalMemoryMiB     float64
	UtilizationPercent float64
}

type nvidiaTelemetrySnapshot struct {
	Connected                    bool
	Error                        string
	DeviceCount                  int
	TotalMemoryMiB               float64
	UsedMemoryMiB                float64
	FreeMemoryMiB                float64
	AverageGPUUtilizationPercent float64
	Devices                      []RuntimeGPUDeviceMetrics
}

type gpuDeviceMetricKey struct {
	Node    string
	GPU     string
	UUID    string
	Product string
}

func (s *Service) collectNvidiaTelemetry(ctx context.Context) (nvidiaTelemetrySnapshot, error) {
	if strings.TrimSpace(s.nvidiaMetricsEndpoint) == "" {
		return nvidiaTelemetrySnapshot{}, nil
	}
	if s.httpClient == nil {
		s.httpClient = &http.Client{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.nvidiaMetricsEndpoint, nil)
	if err != nil {
		return nvidiaTelemetrySnapshot{}, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nvidiaTelemetrySnapshot{Error: err.Error()}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("scrape %s returned status %d", s.nvidiaMetricsEndpoint, resp.StatusCode)
		return nvidiaTelemetrySnapshot{Error: err.Error()}, err
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return nvidiaTelemetrySnapshot{Error: err.Error()}, err
	}

	devices := map[gpuDeviceMetricKey]*RuntimeGPUDeviceMetrics{}
	applyGaugeMetrics(devices, families[dcgmFBUsedMetricName], func(device *RuntimeGPUDeviceMetrics, value float64) {
		device.UsedMemoryMiB = value
	})
	applyGaugeMetrics(devices, families[dcgmFBFreeMetricName], func(device *RuntimeGPUDeviceMetrics, value float64) {
		device.FreeMemoryMiB = value
	})
	applyGaugeMetrics(devices, families[dcgmGPUUtilMetricName], func(device *RuntimeGPUDeviceMetrics, value float64) {
		device.UtilizationPercent = value
	})

	if len(devices) == 0 {
		return nvidiaTelemetrySnapshot{
			Connected: true,
		}, nil
	}

	snapshot := nvidiaTelemetrySnapshot{
		Connected: true,
		Devices:   make([]RuntimeGPUDeviceMetrics, 0, len(devices)),
	}

	for _, device := range devices {
		if device.TotalMemoryMiB == 0 && (device.UsedMemoryMiB > 0 || device.FreeMemoryMiB > 0) {
			device.TotalMemoryMiB = device.UsedMemoryMiB + device.FreeMemoryMiB
		}
		snapshot.DeviceCount++
		snapshot.TotalMemoryMiB += device.TotalMemoryMiB
		snapshot.UsedMemoryMiB += device.UsedMemoryMiB
		snapshot.FreeMemoryMiB += device.FreeMemoryMiB
		snapshot.AverageGPUUtilizationPercent += device.UtilizationPercent
		snapshot.Devices = append(snapshot.Devices, *device)
	}

	if snapshot.DeviceCount > 0 {
		snapshot.AverageGPUUtilizationPercent /= float64(snapshot.DeviceCount)
	}
	sort.Slice(snapshot.Devices, func(i, j int) bool {
		if snapshot.Devices[i].NodeName == snapshot.Devices[j].NodeName {
			return snapshot.Devices[i].GPU < snapshot.Devices[j].GPU
		}
		return snapshot.Devices[i].NodeName < snapshot.Devices[j].NodeName
	})
	return snapshot, nil
}

func applyGaugeMetrics(
	devices map[gpuDeviceMetricKey]*RuntimeGPUDeviceMetrics,
	family *dto.MetricFamily,
	apply func(device *RuntimeGPUDeviceMetrics, value float64),
) {
	if family == nil {
		return
	}
	for _, metric := range family.Metric {
		device := upsertGPUDeviceMetric(devices, metric)
		if metric.Gauge == nil {
			continue
		}
		apply(device, metric.Gauge.GetValue())
	}
}

func upsertGPUDeviceMetric(devices map[gpuDeviceMetricKey]*RuntimeGPUDeviceMetrics, metric *dto.Metric) *RuntimeGPUDeviceMetrics {
	labels := map[string]string{}
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}

	key := gpuDeviceMetricKey{
		Node:    firstNonEmptyLabel(labels, "Hostname", "hostname", "node", "instance"),
		GPU:     firstNonEmptyLabel(labels, "gpu", "minor_number", "device"),
		UUID:    firstNonEmptyLabel(labels, "UUID", "uuid"),
		Product: firstNonEmptyLabel(labels, "modelName", "product", "gpu_product"),
	}
	device := devices[key]
	if device != nil {
		return device
	}

	device = &RuntimeGPUDeviceMetrics{
		NodeName: key.Node,
		GPU:      key.GPU,
		UUID:     key.UUID,
		Product:  key.Product,
	}
	devices[key] = device
	return device
}

func firstNonEmptyLabel(labels map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(labels[name]); value != "" {
			return value
		}
	}
	return ""
}
