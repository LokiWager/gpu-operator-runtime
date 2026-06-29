# Open WebUI Example

This example shows how to use Open WebUI as an application image in `gpu-operator-runtime`.

The recommended shape is:

1. run the actual GPU inference backend as one `GPUUnit`, for example `vLLM`
2. run Open WebUI as a separate browser-facing `GPUUnit`
3. connect Open WebUI to the backend through the backend service URL

That gives the project a much cleaner teaching story:

- the GPU budget is spent on inference
- the UI stays replaceable
- the runtime contract stays explicit

## Why This Fits the Current Model

In the current chapter, the runtime model is intentionally small:

- the runtime API persists active `GPUUnit` objects directly in `runtime-instance`
- each create request carries its own image, memory, GPU count, template, access settings, and optional storage mounts
- Kubernetes performs the final allocation through extended resources, scheduler placement, and namespace quotas

That means Open WebUI should be prepared exactly like any other app image:

- create one active unit for the UI runtime
- set `gpu: 0` for the UI so GPU budget stays with the inference backend

## Dockerfile

The [Dockerfile](/Users/haotingyi/Documents/workspaces/loki/gpu-operator-runtime/examples/open-webui/Dockerfile) is a thin wrapper over the official image.

It sets defaults that match this project:

- disable the Ollama path
- enable the OpenAI-compatible backend path
- point to a default in-cluster backend URL
- expose port `8080`

Build it with:

```bash
docker build -t loki/open-webui-runtime:part19 /Users/haotingyi/Documents/workspaces/loki/gpu-operator-runtime/examples/open-webui
```

## Recommended Runtime Layout

### 1. Run the inference backend as the GPU workload

For example:

- `specName: g1.1`
- `gpu: 1`
- `image: vllm/vllm-openai`
- port `8000`

That backend will eventually publish a service URL like:

```text
http://unit-vllm-chat.runtime-instance.svc.cluster.local:8000/v1
```

### 2. Create the Open WebUI runtime

```bash
curl -s -X POST http://127.0.0.1:8080/api/v1/gpu-units \
  -H 'Content-Type: application/json' \
  -d '{
    "operationID":"unit-open-webui-001",
    "name":"open-webui",
    "specName":"ui.1",
    "image":"loki/open-webui-runtime:part19",
    "memory":"2Gi",
    "gpu":0,
    "access":{
      "primaryPort":"http",
      "scheme":"http"
    },
    "template":{
      "ports":[{"name":"http","port":8080}],
      "envs":[
        {"name":"ENABLE_OLLAMA_API","value":"false"},
        {"name":"ENABLE_OPENAI_API","value":"true"},
        {"name":"OPENAI_API_BASE_URL","value":"http://unit-vllm-chat.runtime-instance.svc.cluster.local:8000/v1"},
        {"name":"OPENAI_API_KEY","value":"dummy"},
        {"name":"WEBUI_URL","value":"http://unit-open-webui.runtime-instance.svc.cluster.local:8080"}
      ]
    }
  }' | jq
```

Wait until the active UI unit is ready:

```bash
kubectl get gpuunits -n runtime-instance
```

In this layout:

- the Open WebUI pod does not request GPU
- the Open WebUI service becomes the browser entrypoint
- the vLLM service stays the model backend
- the active unit stores the exact runtime spec submitted by the API

## How This Maps to the Project

This is the cleanest way to use Open WebUI in the current runtime:

- one active GPUUnit for the GPU inference backend
- one active GPUUnit for the CPU UI runtime
- one shared `GPUUnit` schema, with resource requests making the allocation explicit

That helps readers understand a real platform concern:

- not every user-facing runtime should request `nvidia.com/gpu`
- service-to-service addressing belongs in runtime configuration
- the control plane should keep runtime shape explicit instead of hiding it behind UI convenience

## Operational Note

Open WebUI stores some environment-derived settings as local state.

That means changing env vars after first boot may not behave the way you expect if the app already wrote data under `/app/backend/data`.

For this project, that means:

- keep the data directory ephemeral while iterating
- once persistent storage is introduced, document how config changes are applied or reset

This is exactly the kind of issue that starts to matter once the runtime stops being a placeholder.

## If You Really Want a Single GPU Pod

You can swap the base image in the Dockerfile to a GPU-capable variant and run it with `gpu: 1`, for example:

```dockerfile
ARG OPEN_WEBUI_IMAGE=ghcr.io/open-webui/open-webui:v0.7.2-cuda
```

That is possible, but it is not the default teaching path here. It blurs three concerns that are more useful to keep separate:

- UI runtime
- inference runtime
- model backend contract

## References

- [Open WebUI Docker image docs](https://docs.openwebui.com/getting-started/advanced-topics/docker/)
- [Open WebUI environment variable docs](https://docs.openwebui.com/getting-started/env-configuration/)
- [Open WebUI OpenAI connection guide](https://docs.openwebui.com/getting-started/quick-start/starting-with-openai-compatible/)
- [Open WebUI release page](https://github.com/open-webui/open-webui/releases)
