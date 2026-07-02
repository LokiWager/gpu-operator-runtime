# gpu-operator-runtime

Teaching-oriented Golang + Kubernetes project for building a GPU runtime control plane.

The current chapter keeps runtime instance creation on DRA-backed packages and adds an optional HAMi virtual GPU package path:

- create requests can pass a controlled `packageID`, for example `gpu-rtx3080-2x-cpu10-mem40g`
- the package expands into Kubernetes CPU/memory requests plus a DRA GPU allocation contract
- whole-GPU packages reference a normal GPU `DeviceClass`
- HAMi packages map `virtualGPU.memory` and `virtualGPU.cores` into DRA `capacity.requests`
- the controller creates a GPUUnit-owned `ResourceClaim` and mounts it into the Pod through `pod.spec.resourceClaims`
- the runtime does not set traditional `nvidia.com/gpu` requests on the container
- `GET /api/v1/operator/inventory` exposes DRA `ResourceClaim` and `ResourceSlice` visibility alongside node and quota context
- Kubernetes and the DRA driver own final allocation; runtime no longer counts `GPUUnit.spec.gpu` as the DRA inventory source of truth

The runtime still does not become a scheduler. It validates product-level intent, expands trusted packages, exposes inventory visibility, and creates Kubernetes objects. Kubernetes remains the final allocator through `ResourceSlice`, `ResourceClaim.status`, scheduler placement, and namespace policy.

## Prerequisites

- Go 1.26+
- a reachable Kubernetes cluster (`KUBECONFIG` or in-cluster config)

## GPU and DRA prerequisite

The main chapter path expects Kubernetes Dynamic Resource Allocation and a GPU DRA driver.

For the whole-GPU tutorial package configured in `config/local/runtime-api.yaml`, the cluster should expose a DRA `DeviceClass` named:

```text
nvidia-rtx-3080
```

The driver should also publish `ResourceSlice` objects that make matching devices visible to the scheduler.

`DeviceClass` is a Kubernetes API object and should be managed by ops through YAML/GitOps or a controlled admin API. `ResourceSlice` objects are driver-published capacity; the runtime should observe them, not create them for user requests.

For the HAMi virtual GPU package, install HAMi with native DRA support so the cluster exposes:

```text
hami-core-gpu.project-hami.io
```

The runtime package catalog then requests per-device consumable GPU capacity through DRA:

```yaml
packages:
  - id: "gpu-hami-10g-50c-cpu4-mem16g"
    specName: "gpu.hami.10g.50c.4c.16g"
    cpu: "4"
    memory: "16Gi"
    gpu: 1
    virtualGPU:
      provider: "hami"
      memory: "10Gi"
      cores: 50
    allocation:
      claimRequestName: "gpu"
```

During catalog normalization, this expands into a DRA allocation for `hami-core-gpu.project-hami.io` with `capacity.requests.memory=10Gi` and `capacity.requests.cores=50`. Kubernetes still owns CPU and Pod memory placement through ordinary container resource requests; HAMi owns GPU memory/core sharing underneath the DRA allocation contract.

### HAMi DRA cluster operations

The runtime repository does not install HAMi itself. In a real cluster, ops should manage HAMi DRA with Helm or GitOps before enabling HAMi packages in `runtime-api.yaml`.

Check cluster prerequisites first:

- Kubernetes version supports DRA consumable capacity
- CDI is enabled in the container runtime
- NVIDIA drivers and NVIDIA container runtime integration are ready
- `runtime-instance` and other runtime namespaces already exist

Install cert-manager because the HAMi DRA webhook uses TLS certificates:

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update jetstack

helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true
```

Install HAMi DRA:

```bash
helm repo add hami-dra https://project-hami.github.io/HAMi-DRA
helm repo update hami-dra

helm upgrade --install hami-dra hami-dra/hami-dra
```

If the NVIDIA driver is pre-installed on the host instead of managed by GPU Operator/container driver pods, install HAMi DRA with:

```bash
helm upgrade --install hami-dra hami-dra/hami-dra \
  --set drivers.nvidia.containerDriver=false
```

Verify that HAMi DRA is running and publishing DRA capacity:

```bash
kubectl get pods -n hami-system
kubectl get resourceslices
kubectl get deviceclasses.resource.k8s.io
```

The expected DRA driver and DeviceClass for this chapter are:

```text
hami-core-gpu.project-hami.io
```

After creating a runtime instance from `gpu-hami-10g-50c-cpu4-mem16g`, verify the generated claim and observed allocation:

```bash
kubectl get resourceclaims -n runtime-instance
kubectl get resourceclaim unit-demo-instance-gpu -n runtime-instance -o yaml
kubectl get gpuunit demo-instance -n runtime-instance -o yaml
```

The generated `ResourceClaim` should contain `capacity.requests.memory` and `capacity.requests.cores`. When the claim is allocated, `GPUUnit.status.dra.devices[*].consumedCapacity` should reflect the consumed virtual GPU capacity.

This chapter intentionally uses HAMi DRA native mode. Do not also add traditional `nvidia.com/gpu`, `nvidia.com/gpumem`, or `nvidia.com/gpucores` container requests to the same workload.

The runtime still reads `nvidia.com/gpu` node capacity for health and telemetry context. In practice, that requires a cluster that already provides:

- NVIDIA drivers
- container runtime integration
- the NVIDIA device plugin

You can verify that the cluster is ready with:

```bash
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}'
```

If the value is empty, node-level GPU health fields will be empty. If the DRA `DeviceClass` or `ResourceSlice` objects are missing, a package-backed request can be accepted by the runtime but remain pending in Kubernetes until the DRA driver reports capacity.

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

`make run` starts the controller manager with `config/local/controller-manager.yaml`.
If you want to point at a different local config file directly:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/controller-manager --config config/local/controller-manager.yaml
```

Start the runtime API in a second terminal:

```bash
make run-api
```

Or run it with an explicit config file:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/runtime-api --config config/local/runtime-api.yaml
```

Run the shared storage proxy in a second terminal:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/runtime-proxy --http-addr :8090
```

If you want to exercise the new queue-first serverless flow, deploy ScyllaDB inside the cluster:

```bash
kubectl apply -k config/scylla
kubectl -n runtime-data wait --for=condition=ready pod -l app.kubernetes.io/name=scylla --timeout=10m
```

The examples assume NATS JetStream is also exposed through an in-cluster Service:

```text
nats://nats.messaging.svc.cluster.local:4222
```

The dedicated activator has its own YAML config and should run where it can reach the Kubernetes API and the in-cluster NATS Service:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/activator --config config/local/activator.yaml
```

Start the result-store consumer where it can reach both NATS and ScyllaDB service DNS:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/result-store --config config/local/result-store.yaml
```

For out-of-cluster debugging, use `kubectl port-forward` and override the YAML hosts to `127.0.0.1`. Do not point production traffic at Pod IPs or public endpoints.

Start a minimal example framework in another terminal:

```bash
SERVERLESS_FRAMEWORK_SOCKET_PATH=/tmp/serverless-framework/framework.sock \
GOTOOLCHAIN=go1.26.0 go run ./cmd/framework-echo
```

Then start the worker sidecar loop in another terminal:

```bash
SERVERLESS_NATS_URL=nats://nats.messaging.svc.cluster.local:4222 \
SERVERLESS_SUBJECT_PREFIX=runtime.serverless \
SERVERLESS_STREAM_NAME=RUNTIME_SERVERLESS \
SERVERLESS_WORKER_NAME=sd-webui-template \
SERVERLESS_WORKER_NAMESPACE=runtime-instance \
SERVERLESS_REQUEST_ID=sd-webui \
SERVERLESS_FRAMEWORK_SOCKET_PATH=/tmp/serverless-framework/framework.sock \
SERVERLESS_FRAMEWORK_INVOKE_PATH=/invoke \
SERVERLESS_FRAMEWORK_HEALTH_PATH=/healthz \
GOTOOLCHAIN=go1.26.0 go run ./cmd/serverless-sidecar
```

Useful flags:

- `--config` optional process YAML config path; defaults to built-in values when omitted
- `--kubeconfig` optional standard controller-runtime flag
- zap logging flags such as `--zap-devel`
- `cmd/runtime-proxy` still accepts `--http-addr` and `--kubeconfig`

Optional serverless queue config now lives under `serverless:` in both split process configs:

```yaml
serverless:
  url: "nats://nats.messaging.svc.cluster.local:4222"
  subjectPrefix: "runtime.serverless"
  streamName: "RUNTIME_SERVERLESS"
  streamReplicas: 1
  streamMaxAge: "72h"
  connectTimeout: "5s"
  duplicatesWindow: "24h"
  networkPolicyTarget:
    namespace: "messaging"
    podLabels:
      app.kubernetes.io/name: "nats"
```

If `serverless.url` points to a Kubernetes `*.svc` hostname and `networkPolicyTarget` is missing, the runtime controller now treats that as a configuration error instead of silently creating a Pod that cannot reach NATS.

The dedicated activator has its own local YAML config:

```bash
cat config/local/activator.yaml
```

Example queue-first invocation request:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/serverless/invocations \
  -H 'Content-Type: application/json' \
  -d '{
    "serverlessRequestID": "sd-webui",
    "mode": "async",
    "attributes": {
      "path": "/generate",
      "method": "POST"
    },
    "payload": {
      "prompt": "draw a robot"
    }
  }'
```

At this stage, the manager, activator, worker sidecar, and local framework contract cover the full execution handoff from ingress queue to worker-local invocation.

The activator reconciles `serverless.minAvailableCount` and `serverless.idleTimeoutSeconds` from the GPUUnit serverless spec. The result-store process consumes durable worker results and writes them to ScyllaDB so the control plane can serve async result lookup without asking activator or workers.

Build the standalone userspace image acceleration tool:

```bash
GOTOOLCHAIN=go1.26.0 go build -o bin/image-accelerator ./cmd/image-accelerator
```

The new `image-accelerator` command is a thin wrapper around the official
`containerd/accelerated-container-image` standalone userspace convertor. It keeps
the official conversion engine and only adds local YAML config, flag overrides,
and friendlier validation.

It is intentionally an offline tool for CI, release, or control-plane workflows.
The runtime manager does not call it on the request path.

Important: this command follows the official overlaybd toolchain layout. It does
not need a full overlaybd snapshotter or containerd installation, but it does
expect these files to already exist:

- `/opt/overlaybd/bin/overlaybd-create`
- `/opt/overlaybd/bin/overlaybd-commit`
- `/opt/overlaybd/bin/overlaybd-apply`
- `/opt/overlaybd/bin/turboOCI-apply` when `engine: turbo-oci`
- `/opt/overlaybd/baselayers/ext4_64` when `overlaybd.mkfs: false`

Example config:

```bash
cat config/local/image-accelerator.yaml
```

Example run:

```bash
GOTOOLCHAIN=go1.26.0 go run ./cmd/image-accelerator --config config/local/image-accelerator.yaml
```

Swagger UI is served at:

```text
http://127.0.0.1:8080/swagger/index.html
```

## Install the CRDs

Create the runtime namespaces and quota guardrails first:

```bash
kubectl apply -k config/runtime
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

### 1. Inspect runtime inventory

```bash
curl -s http://127.0.0.1:8080/api/v1/operator/inventory | jq
```

### 2. Create prepared storage

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-storages \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"model-cache",
    "size":"20Gi",
    "storageClassName":"rook-ceph-block",
    "prepare":{
      "fromImage":"busybox:1.36",
      "command":["sh","-c"],
      "args":["mkdir -p /workspace/model && echo seeded > /workspace/model/README.txt"]
    },
    "accessor":{
      "enabled":true
    }
  }' | jq
```

Inspect storage, the prepare job, and the accessor:

```bash
curl -s 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache' | jq
kubectl get gpustorages -n runtime-instance
kubectl get pvc -n runtime-instance
kubectl get jobs -n runtime-instance -l runtime.lokiwager.io/storage=model-cache
kubectl get deploy,svc -n runtime-instance | grep storage-accessor-model-cache
```

If `runtime-proxy` is running, the same storage will be available through:

```text
http://127.0.0.1:8090/storage/runtime-instance/model-cache/
```

### 3. Create an active runtime from a DRA package

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-units \
  -H 'Content-Type: application/json' \
  -d '{
    "operationID":"unit-demo-001",
    "name":"demo-instance",
    "packageID":"gpu-rtx3080-2x-cpu10-mem40g",
    "image":"python:3.12",
    "access":{
      "primaryPort":"http",
      "scheme":"http"
    },
    "ssh":{
      "enabled":true,
      "username":"runtime",
      "serverAddr":"frps.internal",
      "serverPort":7000,
      "connectHost":"ssh.example.com",
      "connectPort":1337,
      "domainSuffix":"ssh.example.com",
      "authorizedKeys":[
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== demo@example"
      ]
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

The package is loaded from `runtime-api.yaml`:

```yaml
packages:
  - id: "gpu-rtx3080-2x-cpu10-mem40g"
    specName: "gpu.rtx3080.2x.10c.40g"
    cpu: "10"
    memory: "40Gi"
    gpu: 2
    allocation:
      deviceClassName: "nvidia-rtx-3080"
      claimRequestName: "gpu"
      count: 2
  - id: "gpu-hami-10g-50c-cpu4-mem16g"
    specName: "gpu.hami.10g.50c.4c.16g"
    cpu: "4"
    memory: "16Gi"
    gpu: 1
    virtualGPU:
      provider: "hami"
      memory: "10Gi"
      cores: 50
    allocation:
      claimRequestName: "gpu"
```

The first package expands into `cpu: "10"`, `memory: "40Gi"`, `gpu: 2`, and a DRA allocation that references `DeviceClass` `nvidia-rtx-3080`.

The second package expands into ordinary Kubernetes `cpu: "4"` and `memory: "16Gi"` container resources plus a HAMi DRA allocation equivalent to:

```yaml
devices:
  requests:
    - name: gpu
      exactly:
        deviceClassName: hami-core-gpu.project-hami.io
        allocationMode: ExactCount
        count: 1
        capacity:
          requests:
            memory: 10Gi
            cores: "50"
```

The controller creates a per-unit `ResourceClaim` and the Pod references that claim. There is no non-DRA create fallback in this chapter; callers must use a configured package or an internally trusted DRA allocation.

### 4. Inspect the active runtime

```bash
kubectl get gpuunits -n runtime-instance
kubectl get gpuunit demo-instance -n runtime-instance -o yaml
kubectl get deploy,svc,pod,pvc -n runtime-instance | grep demo-instance
```

If the create request included SSH settings, `status.ssh.accessCommand` will contain a ready-to-run command similar to:

```bash
ssh -o ProxyCommand='nc -X connect -x ssh.example.com:1337 %h %p' runtime@demo-instance.runtime-instance.ssh.example.com
```

### 5. Update runtime or storage

Resize storage:

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache' \
  -H 'Content-Type: application/json' \
  -d '{
    "size":"40Gi"
  }' | jq
```

Disable the accessor later:

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache' \
  -H 'Content-Type: application/json' \
  -d '{
    "size":"40Gi",
    "accessor":{
      "enabled":false
    }
  }' | jq
```

Move the mount path on the runtime:

```bash
curl -s -X PUT 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance' \
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
curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/gpu-units/demo-instance'
```

The storage object and PVC stay behind.

Delete the storage after it is no longer mounted:

```bash
curl -i -X DELETE 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache'
```

If a prepare job fails and you want the controller to start a new attempt:

```bash
curl -s -X POST 'http://127.0.0.1:8080/api/v1/gpu-storages/model-cache/recover' | jq
```

## Operational notes

- Runtime create is idempotent on `operationID`. Replaying the same request returns the same active unit instead of creating a duplicate workload.
- `GPUStorage` is a separate lifecycle object. Runtime deletion does not delete storage.
- Storage deletion is blocked by the API while an active `GPUUnit` still references that storage.
- `GPUUnit` mounts only reference storage by name and mount path. PVC naming, claim lifecycle, and status tracking stay controller-owned.
- The current storage path is intentionally RBD-shaped: one active runtime owns one workspace volume. Shared filesystem use cases belong to a later CephFS-style path, not this chapter.
- Reusing one `GPUStorage` from two active `GPUUnit` objects is rejected by the API with `409 Conflict`.
- Storage data preparation is asynchronous and controller-owned. The API persists the contract immediately, and `GPUStorage.status` reports prepare job and recovery state.
- The first built-in storage accessor is intentionally small: a controller-owned read-only HTTP path for browsing prepared data, not a full data gateway.

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

- `cmd/controller-manager`: reconciler-only controller process for `GPUUnit` and `GPUStorage`
- `cmd/runtime-api`: HTTP API, status reporter, and serverless ingress publisher
- `api/v1alpha1`: `GPUUnit` and `GPUStorage` API schemas
- `internal/controller`: runtime and storage reconcilers plus workload helper logic
- `pkg/api`: Echo HTTP handlers and Swagger annotations
- `pkg/config`: local process configuration loaded from YAML
- `pkg/service`: package expansion, DRA-aware inventory, storage CRUD, recovery actions, idempotency, and API orchestration
- `pkg/jobs`: periodic status logging
- `config/`: generated CRDs, RBAC, and sample manifests

## Example app image

- `examples/open-webui`: Open WebUI packaged as a browser-facing runtime image. The recommended teaching path is to run it as `gpu: 0` and point it at a separate GPU backend such as `vLLM`.
