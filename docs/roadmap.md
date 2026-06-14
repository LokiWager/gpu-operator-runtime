# Runtime Tutorial Roadmap

1. Environment bootstrap and minimal runtime skeleton
2. Minimal Operator skeleton (CRD + reconcile + status)
3. Stock-based fast start simulation
4. VM lifecycle and state machine
5. Scheduling and resource orchestration
6. API contracts and idempotency
7. Single GPUUnit model and stock handoff
8. Storage architecture I (PVC-backed storage resources)
9. Storage architecture II (accessors, data jobs, and recovery)
10. Shared proxy access and SSH entrypoints
11. Metrics, events, and alerting
12. Image acceleration tool for cold-start optimization
13. Serverless contract and queue-first invocation with NATS
14. Dedicated activator service and worker dispatch subjects
15. Worker sidecar and local framework contract
16. Worker lifecycle management (prewarm, idle scale-down, and metrics-driven worker state)
17. ScyllaDB-backed invocation result store, Kubernetes ScyllaDB stack, and control-plane result consumer
18. Reliability and performance engineering
19. Multi-cluster scheduling and serverless federation
