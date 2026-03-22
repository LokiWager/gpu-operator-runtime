# gpu-operator-runtime

Teaching-oriented Golang + Kubernetes project for building a GPU runtime control plane.

The current chapter keeps the model deliberately small:

- one custom resource: `GPUUnit`
- one reconciler: `GPUUnitReconciler`
- one runtime spec shared by stock units and active units
- one stock namespace: `runtime-stock`
- one active namespace: `runtime-instance`

The operator API seeds stock units into `runtime-stock`. The runtime API consumes one ready stock unit and creates an active `GPUUnit` in `runtime-instance`. The controller reconciles both shapes with the same logic and only changes exposure behavior by namespace:

- stock units reconcile a `Deployment`, but no `Service`
- active units reconcile both `Deployment` and `Service`

## Prerequisites

- Go 1.26+
- a reachable Kubernetes cluster (`KUBECONFIG` or in-cluster config)

## GPU prerequisite

This project maps GPU requests to the standard Kubernetes resource name `nvidia.com/gpu`.

That means the cluster must already expose NVIDIA GPU resources before a `GPUUnit` with `gpu > 0` can schedule successfully.

In practice, the simplest setup is:

- install the NVIDIA GPU Operator on the cluster

Equivalent setups also work, as long as the cluster already provides:

- NVIDIA drivers
- container runtime integration
- the NVIDIA device plugin

You can verify that the cluster is ready with:

```bash
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}'
```

If the value is empty, a request like `"gpu": 1` will stay pending. For API and controller development on a non-GPU cluster, use `gpu: 0`.

## Run locally

```bash
make tidy
make run
```

Useful flags:

- `--http-addr` default `:8080`
- `--kubeconfig` optional standard controller-runtime flag
- `--report-interval` default `30s`
- `--metrics-bind-address` default `0`
- `--health-probe-bind-address` default `:8081`

Swagger UI is served at:

```text
http://127.0.0.1:8080/swagger/index.html
```

## Install the CRD

Create the working namespaces first:

```bash
kubectl create namespace runtime-stock
kubectl create namespace runtime-instance
```

Install the CRD:

```bash
kubectl apply -f config/crd/bases/runtime.lokiwager.io_gpuunits.yaml
```

If you want a direct manifest example instead of using the API first:

```bash
kubectl apply -f config/samples/runtime_v1alpha1_gpuunit.yaml
```

## Quick start

### 1. Seed stock units

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/operator/stock-units \
  -H 'Content-Type: application/json' \
  -d '{
    "operationID":"stock-g1-demo-001",
    "specName":"g1.1",
    "image":"python:3.12",
    "memory":"16Gi",
    "gpu":1,
    "replicas":2,
    "access":{
      "primaryPort":"http",
      "scheme":"http"
    },
    "template":{
      "command":["python"],
      "args":["-m","http.server","8080"],
      "ports":[{"name":"http","port":8080}]
    }
  }' | jq
```

Check the asynchronous job:

```bash
curl -s http://127.0.0.1:8080/api/v1/operator/jobs/stock-g1-demo-001 | jq
```

Inspect stock units:

```bash
kubectl get gpuunits -n runtime-stock
kubectl get gpuunit -n runtime-stock
```

### 2. Consume one ready stock unit into an active runtime

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-units \
  -H 'Content-Type: application/json' \
  -d '{
    "operationID":"unit-demo-001",
    "name":"demo-instance",
    "namespace":"runtime-instance",
    "specName":"g1.1"
  }' | jq
```

The create request is intentionally narrow. It does not accept image, memory, GPU, template, or access overrides. Those fields come from the consumed stock unit.

### 3. Inspect the active runtime

```bash
kubectl get gpuunits -n runtime-instance
kubectl get gpuunit demo-instance -n runtime-instance -o yaml
kubectl get deploy,svc,pod -n runtime-instance | grep demo-instance
```

### 4. Update the active runtime

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance?namespace=runtime-instance' \
  -H 'Content-Type: application/json' \
  -d '{
    "image":"pytorch:2.6",
    "template":{
      "command":["python"],
      "args":["-m","http.server","7860"],
      "ports":[{"name":"web","port":7860}]
    },
    "access":{
      "primaryPort":"web",
      "scheme":"http"
    }
  }' | jq
```

### 5. Query and delete active units

```bash
curl -s 'http://127.0.0.1:8080/api/v1/gpu-units?namespace=runtime-instance' | jq
curl -s 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance?namespace=runtime-instance' | jq
curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance?namespace=runtime-instance'
```

## Operational notes

- Stock replenishment is explicit in this chapter. If you want more stock, call `POST /api/v1/operator/stock-units` again.
- The controller derives stock versus active behavior from namespace placement, not from a second spec.
- Active runtime create is idempotent on `operationID`. Replaying the same request returns the same active unit instead of consuming stock twice.
- A stock handoff is `claim -> delete stock -> create active -> restore stock on failure`. This matches the reality that warm GPU stock is already holding scarce capacity.

## Quality gates

```bash
make ci
```

`make ci` runs:

- CRD and RBAC generation
- Swagger generation
- formatting checks
- `go vet`
- race-enabled tests
- binary build

## Project layout

- `cmd/main.go`: manager, HTTP server, and async job worker in one process
- `api/v1alpha1`: `GPUUnit` API schema
- `internal/controller`: shared `GPUUnit` reconciler and workload helper logic
- `pkg/api`: Echo HTTP handlers and Swagger annotations
- `pkg/service`: stock seeding, stock consumption, idempotency, and CRUD orchestration
- `pkg/jobs`: periodic status logging
- `config/`: generated CRDs, RBAC, and sample manifests

## Example app image

- `examples/open-webui`: Open WebUI packaged as a browser-facing runtime image. The recommended teaching path is to run it as `gpu: 0` and point it at a separate GPU backend such as `vLLM`.
