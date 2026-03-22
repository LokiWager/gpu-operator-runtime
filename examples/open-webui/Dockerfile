ARG OPEN_WEBUI_IMAGE=ghcr.io/open-webui/open-webui:v0.7.2
FROM ${OPEN_WEBUI_IMAGE}

# These are safe defaults for this teaching project.
# Override them through the GPUUnit template envs in real deployments.
ENV ENABLE_OLLAMA_API=false \
    ENABLE_OPENAI_API=true \
    OPENAI_API_BASE_URL=http://vllm.default.svc.cluster.local:8000/v1 \
    OPENAI_API_KEY=dummy \
    WEBUI_URL=http://open-webui.default.svc.cluster.local:8080

EXPOSE 8080

VOLUME ["/app/backend/data"]
