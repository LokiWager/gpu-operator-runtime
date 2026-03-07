# gpu-operator-runtime

Teaching-oriented Golang + Kubernetes GPU runtime project.

This repository now follows a standard `kubebuilder` layout and runs as a single process:

- HTTP API server receives user requests
- async job creates `StockPool` custom resources
- controller reconciles `StockPool` into Deployments and status

## Prerequisites

- Go 1.26+
- A reachable Kubernetes cluster (`KUBECONFIG` or in-cluster config)

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
  -d '{"name":"pool-g1","namespace":"default","specName":"g1.1","image":"nginx:1.27","memory":"16Gi","gpu":1,"replicas":2}' | jq
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
