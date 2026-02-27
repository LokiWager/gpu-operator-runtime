# gpu-operator-runtime

A teaching-oriented Golang + Kubernetes GPU runtime project.

This repository is built from desensitized engineering patterns extracted from real operator projects:

- single-cluster runtime baseline (GPU stock + VM lifecycle)
- multi-cluster and serverless extensions (planned in later chapters)

## Chapter Progress

- [x] Chapter 01: environment bootstrap and minimal runtime skeleton
- [ ] Chapter 02+: lifecycle, reconcile/job model, storage, security, observability, multi-cluster/serverless

## Quick Start

```bash
make tidy
make run
```

Then call:

```bash
curl -s http://127.0.0.1:8080/api/v1/health | jq
```

### Create stocks

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/stocks \
  -H 'Content-Type: application/json' \
  -d '{"number":2,"specName":"g1.1","cpu":"4","memory":"16Gi","gpuType":"RTX4090","gpuNum":1}' | jq
```

### Start a VM from stock spec

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"tenantID":"t-demo","tenantName":"demo","specName":"g1.1"}' | jq
```

## Runtime Flags

- `--http-addr`: default `:8080`
- `--report-interval`: default `30s`
- `--kube-mode`: `auto|off|required`
- `--kubeconfig`: optional kubeconfig path

## Project Layout

- `cmd/runtime`: process entrypoint
- `pkg/api`: REST API layer
- `pkg/service`: business use-cases
- `pkg/store`: in-memory state for chapter bootstrap
- `pkg/jobs`: background status reporting job
- `pkg/kube`: kubernetes client bootstrap
- `deploy/k8s`: minimal manifests
- `docs/chapters`: article chapters

## Desensitization Notes

This repository intentionally removes internal identifiers, registry/domain details, and environment-specific secrets while preserving engineering structure and runtime patterns.
