# Runtime Tutorial Roadmap

## Published Chapters

1. Environment bootstrap and minimal runtime skeleton
2. Minimal Operator skeleton (CRD + reconcile + status)
3. Inventory-based fast start simulation
4. VM lifecycle and state machine
5. Scheduling and resource orchestration
6. API contracts and idempotency
7. Single GPUUnit model and handoff semantics
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
18. Split runtime control plane with separate controller-manager and runtime-api processes
19. DRA-backed package allocation with ResourceClaim status as the allocation source of truth
20. GPU virtualization with HAMi on top of DRA

## Planned Chapters

### 21. Reliable Serverless Execution

Goal: make the serverless queue path reliable under retries, failures, duplicate delivery, and worker pressure.

Code scope:

- Add NATS consumer settings for `ackWait`, `maxDeliver`, and retry backoff.
- Add dead-letter subjects such as `runtime.serverless.dlq.*`.
- Define serverless invocation states such as `queued`, `dispatching`, `running`, `succeeded`, `failed`, and `expired`.
- Persist state transitions in the result store or a companion state table.
- Add timeout classification for activator worker creation, sidecar framework calls, and result-store writes.
- Add idempotency rules for duplicate invocation messages, duplicate dispatches, and duplicate results.
- Add basic backpressure so activator does not create unlimited GPU workers when queues grow faster than capacity.

Blog focus:

- Queue-first means at-least-once delivery, not exactly-once execution.
- Retry, timeout, DLQ, and idempotency are one design, not separate patches.
- Backpressure protects the GPU cluster from turning queue pressure into uncontrolled Pod creation.

### 22. Security and Auditability

Goal: make runtime actions attributable, least-privileged, and safe for multi-tenant operation.

Code scope:

- Add tenant and actor context to API requests without turning runtime into the full control plane.
- Add append-only audit records for create, update, delete, storage, proxy, SSH, and serverless operations.
- Add Secret-backed configuration references for NATS and ScyllaDB credentials instead of plain YAML values.
- Add TLS/auth settings for NATS and ScyllaDB clients.
- Tighten RBAC for controller-manager, runtime-api, activator, result-store, and worker sidecar.
- Refine NetworkPolicy boundaries between API, controller, NATS, ScyllaDB, worker Pods, and user containers.

Blog focus:

- Audit log is not application log.
- Runtime records who did what to which resource, while the control plane owns user identity, policy, and billing decisions.
- Credential and network boundaries must match process boundaries.

### 23. High Availability and Operations

Goal: make the runtime deployable and observable as multiple independent production services.

Code scope:

- Enable controller-manager leader election in the split deployment.
- Run runtime-api as a stateless multi-replica Deployment.
- Define result-store and activator high-availability behavior around durable NATS consumers.
- Add readiness, liveness, startup probes, and graceful shutdown to each process.
- Expand metrics for API latency, reconcile latency, queue lag, consumer errors, worker ready latency, result write latency, and DLQ counts.
- Add Prometheus `ServiceMonitor` examples and alert rule examples.
- Document backup and restore expectations for ScyllaDB-backed runtime state.

Blog focus:

- Which components can scale horizontally and which require leader election or durable-consumer coordination.
- How to debug request path failures from API to queue to worker to result store.
- Which metrics matter for SLOs and on-call operations.

### 24. Usage Accounting, Not Billing

Goal: let runtime produce trusted usage facts while keeping pricing and billing in the control plane.

Code scope:

- Add usage events for resource lifecycle transitions such as GPUUnit start/stop/delete, GPUStorage bind/release, and serverless invocation start/finish.
- Add usage records that convert lifecycle events into measurable intervals.
- Track dimensions such as tenant ID, project ID, resource type, GPU model, GPU count, CPU, memory, storage size, invocation duration, result bytes, and timestamps.
- Add idempotency keys for usage events so controller retries do not duplicate accounting.
- Add runtime usage APIs or export subjects for raw usage events and finalized usage records.
- Avoid price, invoice, discount, package, credit, payment, or billing-cycle logic in runtime.

Control-plane boundary:

- Runtime produces usage facts.
- Control plane applies pricing, discounts, quotas, packages, invoices, payments, and customer-facing billing policy.

Blog focus:

- Runtime accounting is not billing.
- Billing depends on trusted measurement, but business pricing must stay above the runtime layer.
- Usage records should be append-friendly, auditable, and reconstructable from resource lifecycle facts.

### 25. Multi-Cluster Scheduling and Federation

Goal: extend the runtime model beyond one Kubernetes cluster without collapsing cluster-local control into a global scheduler.

Code scope:

- Define a cluster registration and capability report model.
- Report GPU capacity, GPU classes, storage classes, runtime version, and health signals to the control plane.
- Keep each cluster running its own controller-manager, runtime-api, activator, sidecars, and result-store path.
- Let the control plane choose a target cluster based on policy, quota, locality, and available capacity.
- Add cross-cluster status aggregation without making one runtime directly control another cluster.

Blog focus:

- Federation should not turn the runtime into a global scheduler.
- Cluster-local Kubernetes remains responsible for final allocation.
- The control plane chooses where to send work; each runtime owns execution inside its cluster.
