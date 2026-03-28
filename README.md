# gpu-operator-runtime

Teaching-oriented Golang + Kubernetes project for building a GPU runtime control plane.

The current chapter introduces the first persistent storage layer:

- `GPUUnit` still owns runtime lifecycle
- `GPUStorage` now owns PVC lifecycle
- `GPUStorage` defaults to an RBD-backed workspace volume (`rook-ceph-block`)
- stock still lives in `runtime-stock`
- active runtime and storage live in `runtime-instance`
- deleting a runtime no longer implies deleting its data

The operator API seeds stock units into `runtime-stock`. The runtime API consumes one ready stock unit and creates an active `GPUUnit`. The storage API creates RBD-backed `GPUStorage` objects, and active units mount them explicitly through `storageMounts`.

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

## Storage prerequisite

`GPUStorage` reconciles into a Kubernetes `PersistentVolumeClaim`.

This chapter assumes the default workspace volume is backed by Ceph RBD through the `rook-ceph-block` `StorageClass`.

That means the cluster should either:

- expose `rook-ceph-block`
- or you must pass a different RBD-compatible `storageClassName` explicitly when creating storage

You can verify the available storage classes with:

```bash
kubectl get storageclass
```

If you want to run this chapter against a real Ceph-backed storage layer, the two official starting
points are:

- Rook on Kubernetes: [Rook Ceph Quickstart](https://rook.io/docs/rook/latest/Getting-Started/quickstart/)
- Ceph installation overview: [Ceph Installing Ceph](https://docs.ceph.com/en/latest/install/)

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

## Install the CRDs

Create the working namespaces first:

```bash
kubectl create namespace runtime-stock
kubectl create namespace runtime-instance
```

Install the CRDs:

```bash
kubectl apply -f config/crd/bases/runtime.lokiwager.io_gpuunits.yaml
kubectl apply -f config/crd/bases/runtime.lokiwager.io_gpustorages.yaml
```

If you want direct manifest examples instead of using the API first:

```bash
kubectl apply -f config/samples/runtime_v1alpha1_gpustorage.yaml
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
    "memory":"16Gi",
    "gpu":1,
    "replicas":2
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

### 2. Create persistent storage

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-storages \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"model-cache",
    "namespace":"runtime-instance",
    "size":"20Gi",
    "storageClassName":"rook-ceph-block"
  }' | jq
```

Inspect storage:

```bash
curl -s 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache?namespace=runtime-instance' | jq
kubectl get gpustorages -n runtime-instance
kubectl get pvc -n runtime-instance
```

### 3. Consume one ready stock unit into an active runtime

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-units \
  -H 'Content-Type: application/json' \
  -d '{
    "operationID":"unit-demo-001",
    "name":"demo-instance",
    "namespace":"runtime-instance",
    "specName":"g1.1",
    "image":"python:3.12",
    "access":{
      "primaryPort":"http",
      "scheme":"http"
    },
    "template":{
      "command":["python"],
      "args":["-m","http.server","8080"],
      "ports":[{"name":"http","port":8080}]
    },
    "storageMounts":[
      {
        "name":"model-cache",
        "mountPath":"/workspace/cache"
      }
    ]
  }' | jq
```

### 4. Inspect the active runtime

```bash
kubectl get gpuunits -n runtime-instance
kubectl get gpuunit demo-instance -n runtime-instance -o yaml
kubectl get deploy,svc,pod,pvc -n runtime-instance | grep demo-instance
```

### 5. Update runtime or storage

Resize storage:

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache?namespace=runtime-instance' \
  -H 'Content-Type: application/json' \
  -d '{
    "size":"40Gi"
  }' | jq
```

Move the mount path on the runtime:

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance?namespace=runtime-instance' \
  -H 'Content-Type: application/json' \
  -d '{
    "storageMounts":[
      {
        "name":"model-cache",
        "mountPath":"/workspace/data"
      }
    ]
  }' | jq
```

### 6. Deletion semantics

Delete the runtime:

```bash
curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance?namespace=runtime-instance'
```

The storage object and PVC stay behind.

Delete the storage after it is no longer mounted:

```bash
curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache?namespace=runtime-instance'
```

## Operational notes

- Stock replenishment is still explicit. If you want more stock, call `POST /api/v1/operator/stock-units` again.
- Active runtime create is idempotent on `operationID`. Replaying the same request returns the same active unit instead of consuming stock twice.
- `GPUStorage` is a separate lifecycle object. Runtime deletion does not delete storage.
- Storage deletion is blocked by the API while an active `GPUUnit` still references that storage.
- `GPUUnit` mounts only reference storage by name and mount path. PVC naming, claim lifecycle, and status tracking stay controller-owned.
- The current storage path is intentionally RBD-shaped: one active runtime owns one workspace volume. Shared filesystem use cases belong to a later CephFS-style path, not this chapter.
- Reusing one `GPUStorage` from two active `GPUUnit` objects is rejected by the API with `409 Conflict`.

## Quality gates

```bash
make ci
```

`make ci` runs:

- CRD and RBAC generation
- deepcopy generation
- Swagger generation
- formatting checks
- `go vet`
- race-enabled tests
- binary build

## Project layout

- `cmd/main.go`: manager, HTTP server, and async job worker in one process
- `api/v1alpha1`: `GPUUnit` and `GPUStorage` API schemas
- `internal/controller`: runtime and storage reconcilers plus workload helper logic
- `pkg/api`: Echo HTTP handlers and Swagger annotations
- `pkg/service`: stock seeding, stock consumption, storage CRUD, idempotency, and API orchestration
- `pkg/jobs`: periodic status logging
- `config/`: generated CRDs, RBAC, and sample manifests

## Example app image

- `examples/open-webui`: Open WebUI packaged as a browser-facing runtime image. The recommended teaching path is to run it as `gpu: 0` and point it at a separate GPU backend such as `vLLM`.
