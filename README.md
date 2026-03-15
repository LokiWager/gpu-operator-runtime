# gpu-operator-runtime

Teaching-oriented Golang + Kubernetes GPU runtime project.

This repository now follows a standard `kubebuilder` layout and runs as a single process:

- HTTP API server receives user requests
- async job creates `StockPool` custom resources
- controller reconciles `StockPool` into Deployments and status

## Prerequisites

- Go 1.26+
- A reachable Kubernetes cluster (`KUBECONFIG` or in-cluster config)

## GPU Prerequisite

This project maps GPU requests to the standard Kubernetes resource name `nvidia.com/gpu`.

That means the cluster must already expose NVIDIA GPU resources before a `StockPool` with `gpu > 0` can schedule successfully.

In practice, the simplest setup is:

- install the NVIDIA GPU Operator on the cluster

Equivalent setups also work, as long as the cluster already has the required NVIDIA pieces installed and `nvidia.com/gpu` appears on the nodes:

- NVIDIA drivers
- container runtime integration
- NVIDIA device plugin

You can verify that the cluster is ready with:

```bash
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}'
```

If that value is empty, a request like `"gpu": 1` will not run, and the reconciled pods will stay pending.

For API and controller development on a cluster without GPU support, use `gpu: 0`.

## Run locally

```bash
make tidy
make run
```

Flags:

- `--http-addr` (default `:8080`)
- `--kubeconfig` (optional, standard kubebuilder/controller-runtime flag)
- `--report-interval` (default `30s`)
- `--metrics-bind-address` (default `0`, disabled)
- `--health-probe-bind-address` (default `:8081`)

## Install CRD

```bash
kubectl apply -f config/crd/bases/runtime.lokiwager.io_stockpools.yaml
kubectl apply -f config/samples/runtime_v1alpha1_stockpool.yaml
```

## API examples

Create StockPool job:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/operator/stockpools \
  -H 'Content-Type: application/json' \
  -d '{"operationID":"stock-g1-demo-001","name":"pool-g1","namespace":"default","specName":"g1.1","image":"nginx:1.27","memory":"16Gi","gpu":1,"replicas":2}' | jq
```

Query StockPools:

```bash
curl -s http://127.0.0.1:8080/api/v1/operator/stockpools?namespace=default | jq
```

Health:

```bash
curl -s http://127.0.0.1:8080/api/v1/health | jq
```

## Quality gates

```bash
make ci
```

`make ci` runs:

- CRD/RBAC generation (`make manifests generate`)
- formatting checks
- `go vet`
- race-enabled tests
- build

## Project layout

- `cmd/main.go`: unified entrypoint (manager + HTTP + jobs)
- `api/v1alpha1`: CRD API types (`StockPool`)
- `internal/controller`: reconcile logic
- `pkg/api`: Echo-based REST handlers
- `pkg/service`: business orchestration and async job flow
- `pkg/jobs`: periodic status reporter
- `config/`: kubebuilder manifests (CRD/RBAC/manager/samples)
