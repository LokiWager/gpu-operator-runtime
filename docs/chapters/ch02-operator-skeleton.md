# Chapter 02 - Minimal Operator Skeleton

This chapter introduces the first operator loop for the runtime project.

Scope:

- define one CRD (`StockPool`)
- run a `controller-runtime` manager
- implement one reconcile flow that writes status
- keep business state simple and deterministic

Out of scope:

- real provisioning jobs
- final scheduling policy
- storage and network integration

## Why this chapter exists

Chapter 01 proved the runtime service can run and expose stable APIs.
Now we need a Kubernetes-native control loop so the project can move from request-driven behavior to desired-state behavior.

## What we add

1. CRD API type:
   - `pkg/operator/apis/runtime/v1alpha1/stockpool_types.go`
2. Reconciler:
   - `pkg/operator/controllers/stockpool_controller.go`
3. Operator runner and entrypoint integration:
   - `pkg/operator/runner.go`
   - `cmd/runtime/main.go` (`--mode=operator`)
4. CRD and sample manifest:
   - `deploy/operator/crd.yaml`
   - `deploy/operator/sample-stockpool.yaml`
5. Unit test:
   - `pkg/operator/controllers/stockpool_controller_test.go`

## Reconcile behavior in this chapter

Input:

- `spec.specName`
- `spec.replicas`

Output (status):

- `available`
- `allocated`
- `phase`
- `observedGeneration`
- `lastSyncTime`

Current policy:

- `available = max(replicas, 0)`
- `allocated = 0`
- `phase = Ready` when replicas > 0, else `Empty`

This keeps the first reconcile deterministic and easy to test.

## How to run

```bash
make tidy
make test
```

```bash
kubectl apply -f deploy/operator/crd.yaml
kubectl apply -f deploy/operator/sample-stockpool.yaml
go run ./cmd/runtime --mode=operator
```

```bash
kubectl get stockpools.runtime.lokiwager.io pool-g1 -o yaml
```

## What comes next

The next chapter will connect reconcile to runtime lifecycle actions (stock acquisition and VM progression) instead of status mirroring only.
