# Chapter 02 - Kubebuilder Baseline + First Real Reconcile

This chapter migrates the project to a standard `kubebuilder` scaffold and keeps the architecture as one process:

- HTTP server handles user request
- request becomes async job
- job creates `StockPool` CR
- controller reconciles CR to Deployment and status
- runtime parameters such as `image`, `memory`, and `gpu` flow from API to CR spec

## Why we switched to kubebuilder

Manual operator wiring is okay for prototyping, but it becomes hard to maintain quickly:

- structure is non-standard
- CRD/RBAC/codegen are easy to drift
- onboarding cost is higher

`kubebuilder` gives us a shared baseline that most Go/Kubernetes engineers can recognize immediately.

## What changed in this iteration

1. Initialize standard project skeleton:
   - `PROJECT`
   - `api/v1alpha1`
   - `internal/controller`
   - `config/*`
2. Keep a single binary entry:
   - `cmd/main.go`
3. Keep HTTP + operator manager in one process:
   - no runtime/operator split mode
4. Remove obsolete bootstrap-only paths:
   - in-memory stock/vm runtime simulation

## Main files

- API type: `api/v1alpha1/stockpool_types.go`
- Controller: `internal/controller/stockpool_controller.go`
- Entry: `cmd/main.go`
- HTTP layer: `pkg/api/server.go`
- Service/job flow: `pkg/service/service.go`

## Run

```bash
make manifests generate
kubectl apply -f config/crd/bases/runtime.lokiwager.io_stockpools.yaml
make run
```

Create resource via HTTP:

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/operator/stockpools \
  -H 'Content-Type: application/json' \
  -d '{"name":"pool-g1","namespace":"default","specName":"g1.1","image":"nginx:1.27","memory":"16Gi","gpu":1,"replicas":2}' | jq
```

Verify controller output:

```bash
kubectl get stockpools.runtime.lokiwager.io pool-g1 -o yaml
kubectl get deploy -n default
```
