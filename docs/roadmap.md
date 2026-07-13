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
21. Reliable serverless execution with retry policy, DLQ, timeout classification, idempotent queue writes, and activator backpressure
22. Runtime security and operations boundaries with Secret/TLS-backed dependency configuration, process credential isolation, NetworkPolicy/RBAC design notes, background consumer restart semantics, runtime-api metrics, and production deployment shape

## Planned Chapters

### 23. Usage Accounting, Not Billing

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

### 24. Multi-Cluster Scheduling and Federation

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
