# Ecosystem Constraints

## Package Boundary Integrity
- **Reject generic catch-alls:** `common`, `util`, `base`, `models`, etc. packages are forbidden. They accumulate unrelated code, obscure ownership, and become fragile dependency hubs.
- **Domain-driven packaging:** Packages must reflect business domains or bounded contexts, not technical layers. A `user` package is fine; a shared `models` package spanning domains is not.
- **Enforce strict boundaries:** Domain packages must not import transport or infrastructure libraries (HTTP frameworks, ORM/DB drivers, etc.). Crossing these lines (e.g. domain code using HTTP or persistence APIs) indicates inverted dependencies.
- **No “god” modules:** Any package that mixes concerns (handlers, DB access, business logic) or is imported by many unrelated parts is a red flag. Such central hubs concentrate coupling and hinder modularity.
- **Avoid fake layering:** Do not insert trivial service/repository/controller layers that merely pass calls along. Unless they encapsulate real differences, these layers add indirection without benefit.

## Interface Usage and Abstraction Pressure
- **Use interfaces sparingly:** Define an interface only when multiple implementations or clear extension points exist. An interface with one implementation (especially if only for testing) adds unnecessary indirection.
- **Consumer-owned interfaces:** Let callers specify the interface they need. Do not force interfaces in a package whose only implementation is concrete; this reverses Go’s idiom of “interfaces for the consumer, not the provider.”
- **No cargo-cult layers:** Avoid patterns where every component has a matching interface (`UserService`, `OrderRepository`, etc.) with no genuine abstraction. These mimic Java/C# architecture and generate boilerplate without clarity.
- **Favor concrete types internally:** Internal modules should use concrete structs; introduce interfaces when the abstraction is actual. Keep interfaces small (often a single method) and place them at API boundaries or for external integration.

## Concurrency Ownership and Lifecycle Integrity
- **Explicit goroutine ownership:** Every goroutine must be supervised by the service. Untracked or “fire-and-forget” goroutines (e.g. launched in request handlers without synchronization) are forbidden.
- **Context-driven execution:** All blocking or I/O-bound work must take a `context.Context`. Never spawn background work from `context.Background` deep in the call stack—always propagate the incoming context for cancellation.
- **Cancellation and backpressure:** Long-running loops, retries, or workers must observe `<-ctx.Done()`. An infinite retry or unbounded loop without context cancellation and backoff is an anti-pattern that can exhaust resources【38†L254-L263】.
- **Bounded concurrency:** Explicitly cap parallelism using worker pools, semaphores, or `errgroup`. Unregulated fan-out (e.g. spawning a goroutine per item in a large slice) will lead to tail latency, GC spikes, and OOM under load【47†L125-L134】.
- **Coordinated shutdown:** On service shutdown (SIGTERM, etc.), all components (HTTP servers, background workers, queues) must stop via a shared root context. If any goroutines ignore cancellation and linger, that’s a critical flaw【38†L323-L331】.
- **Safe concurrency patterns:** Prefer structured concurrency (`errgroup`, `sync.WaitGroup`) over ad-hoc `go func`. Beware common errors like capturing loop variables in closures and unsynchronized access to shared data (maps/slices); these hidden races signal deeper coupling.

## Runtime and Operational Integrity
- **No hidden control flow:** Avoid reflection, code generation, or annotation-based frameworks that obscure how code executes. Business logic must be explicit in the call graph, not injected by magic (init-time registration, tag parsing, etc.).
- **Explicit wiring:** Dependencies should be passed explicitly (constructors, parameters) rather than hidden in globals or opaque containers. Global state or implicit singletons for core logic are disallowed.
- **Error transparency:** Errors must propagate and be visible. Swallowing errors or relying on panics for flow control is forbidden. All failures should be returned or logged with context to facilitate debugging.
- **Full observability:** Key subsystems (queues, caches, worker pools, sharded components) must export metrics and tracing spans. Blind spots in telemetry (no counters/gauges/logs for internal stages) make root-cause analysis infeasible【47†L167-L178】.
- **Clear resource ownership:** Each resource (memory buffers, file handles, connections, timers) must have a clear owner and cleanup path. Leaked resources (timers never stopped, contexts never cancelled) represent hidden operational debt.
- **Fail-fast design:** The system should detect and report faults early. Hidden side-effects or silent retries that mask errors will be flagged as they erode reliability.

## Structural Coupling and Architectural Drift
- **Keep layers clean:** Transport or handler code should not contain business rules. HTTP handlers, RPC methods, and CLI commands must delegate to domain services. Mixing policy or transaction logic into handlers is a smell.
- **Transport-agnostic domain:** Core packages must not import transport or DB libraries. Likewise, domain models and services should have no knowledge of HTTP/JSON tags or persistence details; those belong at the edges.
- **No “god” service objects:** Beware service packages that do everything for a business flow. If a service spans multiple unrelated features, refactor into smaller, focused services. A bloated “manager” or “processor” often hides collapsing boundaries.
- **Persistence boundaries:** Data-access packages (repositories/stores) should only handle I/O. If complex validation or workflow logic appears in repositories, that’s a violation—such logic belongs in the domain layer.
- **Separate data models:** Do not reuse the same struct across layers. For example, a DB entity, a domain object, and an API DTO should be distinct. Sharing one model everywhere leads to hidden coupling and migration headaches.
- **Correct dependency direction:** Package imports should flow inward (API → service → domain → infra). Any reverse or cyclic dependency pattern indicates erosion of architecture. The domain should not depend on higher-level or sideways modules.

## Organizational Scaling Pressure
- **Single-owner modules:** Assign each package or service to one team. Packages with unclear or “team-wide” ownership (especially shared libraries) become coordination bottlenecks.
- **Localized change impact:** Ideally, implementing a feature or fix affects only one bounded context. If a small change cascades across many modules or services, boundaries are misaligned and will impede scaling.
- **Avoid central levers:** Heavy reliance on a central orchestrator (event bus, task queue, “god” utility) means all teams must coordinate through that channel. Promote decentralized, purpose-driven components instead.
- **Explicit contracts:** Teams should communicate via well-defined interfaces/APIs. Hidden channels (shared DB tables by convention, magic environment variables) are sources of brittleness as the organization grows.
- **Minimize cognitive load:** Structure code so engineers rarely need system-wide knowledge. If understanding or modifying functionality requires mastering numerous unrelated packages, the architecture will not scale.
- **Prefer simplicity for teams:** Over-engineered frameworks or patterns that require global understanding are anti-patterns. A clear, minimal code structure improves productivity as more developers join the project.
