# Chapter 01: Environment Bootstrap and Minimal Runtime Skeleton

This chapter gets the project running first. The goal is not full feature completeness. The goal is to establish a solid engineering baseline that we can iterate on safely.

## 1. Scope and Boundaries

This chapter only covers the smallest single-cluster runtime loop:

- process can start and stop cleanly
- baseline REST APIs are available
- Stock/VM are the two core runtime objects
- one background status reporting job is running
- optional Kubernetes API integration (auto-detect mode)
- day-0 quality gates are available (tests + CI/CD)

Not included yet:

- full reconcile controller workflow
- PVC/Ceph operations
- multi-cluster state aggregation
- serverless workflow

## 2. Project Structure

```text
cmd/runtime
pkg/config
pkg/api
pkg/service
pkg/store
pkg/jobs
pkg/kube
deploy/k8s
docs/chapters
```

Layering rules:

- `api` handles transport and request validation only
- `service` orchestrates business use-cases
- `store` owns state storage operations
- `jobs` runs periodic background tasks
- `kube` handles Kubernetes client integration

## 3. Key Implementation Points

### 3.1 Runtime Entrypoint

- file: `cmd/runtime/main.go`
- responsibilities: config loading, signal handling, graceful shutdown

### 3.2 API Design (v1 baseline)

- `GET /api/v1/health`
- `POST/DELETE/GET /api/v1/stocks`
- `POST/GET /api/v1/vms`
- `GET/DELETE /api/v1/vms/{vmID}`

The initial response envelope is unified as `{data, error}` to reduce future breaking API changes.

### 3.3 Minimal Service Loop

- create Stock resources in batch by spec
- allocate one available Stock when creating a VM
- release Stock back to pool when deleting a VM
- summarize stock/vm runtime state in Health

### 3.4 Background Job

`pkg/jobs/status_reporter.go` periodically reads `Health` and emits runtime status logs.

This follows a common production pattern for status reporting and can later be replaced by event reporting or metrics pipelines.

### 3.5 Optional K8s Integration

`pkg/kube/client.go` supports three modes:

- `auto`: try in-cluster config first, then `~/.kube/config`
- `off`: run without Kubernetes connection
- `required`: Kubernetes connection is mandatory, otherwise startup fails

### 3.6 Testing and CI/CD from Init

From chapter 1, the project already includes:

- unit tests for config/store/service/api
- local quality command: `make ci`
- CI pipeline: format check + vet + race tests + build
- release pipeline: build and publish container image on version tags

This reduces refactor risk while the architecture is still evolving quickly.

## 4. How to Run

```bash
make tidy
make ci
make run
```

Start locally with `--kube-mode=off` first, then switch to `auto/required` to validate cluster connectivity behavior.

## 5. Why This Decomposition Works

This skeleton maps directly to future advanced capabilities:

- reconcile logic can evolve on top of `service + jobs`
- storage workflows can be expanded independently under `pkg/service/storage_*.go`
- multi-cluster support can be added via cluster adapters in `pkg/kube`
- serverless can be introduced as a parallel domain model in `service`

Chapter completion criteria:

- code compiles
- APIs are callable
- baseline runtime state is observable

The next chapter will implement stock-based fast startup simulation and VM lifecycle status progression.
