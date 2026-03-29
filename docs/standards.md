# Project Standards

## Documentation

Each documentation file has a single responsibility. Content must live in exactly one place — if a statement would be equally true in two files, it belongs in whichever file owns that concern.

| File | Responsibility |
|------|---------------|
| `README.md` | Project shape and route map — structure, components, build/test commands, local dev setup. Enables discovery. |
| `docs/standards.md` | Generalised decisions — conventions for code, tests, docs, and Kubernetes controllers. Ensures consistency without cross-comparing older files. |
| `cmd/*/spec.md` | Observable behaviour and interfaces for a single component. No project-wide standards, no implementation detail. |
| `.github/copilot-instructions.md` | Navigation guide for AI agents — where to find documentation and how to use it efficiently. |

### spec.md files

Every compilable `cmd/XXX` must include a `spec.md` detailing the features and interfaces that component provides.

Every line in a spec must pass these checks:

- **Concise** — no redundant phrasing, no filler words, no over-explanation. If a shorter phrasing carries the same meaning, use it.
- **Clear** — language must be unambiguous. Describe observable behaviour, not intent. Avoid vague terms like "handles", "manages", or "supports" unless the meaning is self-evident from context.
- **No duplication** — each feature, interface, or constraint is stated in exactly one spec file.
- **No project-wide standards** — specs must not restate information already covered by `docs/standards.md`. If a line would be equally true of any component in the project, it does not belong in the spec.
- **No implementation detail** — describe observable behaviour and interfaces, not internal structure.
- **No conflicts** — specs must not make contradictory claims across or within files.

## General

### Technologies

- **Language:** Go 1.25+
- **Databases:** PostgreSQL (`lib/pq`) and Redis 8 (`go-redis/v9`).
- **Kubernetes operator framework:** `controller-runtime` — all controllers, reconcilers, and CRD scaffolding use this library.
- **Packaging:** Helm charts under `charts/`.
- **Local development:** Tilt (`Tiltfile` in the repo root).
- **Testing:** Ginkgo v2 (test runner) and Gomega (assertions).

### Package Layout
- `cmd/XXX/` — compilable component entry points.
- `internal/` — shared packages that are only imported within this module.
- `pkg/` — externally importable packages; external Go modules may import these. CRD API types live here (`pkg/api/v1alpha1/`) so that client applications can interact with the Kubernetes API using the same typed struct definitions.

### Reuse Over Reinvention
Before writing anything new — utility, pattern, convention, or routine — check whether an equivalent already exists in the project, a sibling module, or an existing dependency. If it does, use it. If it does not, create it in the appropriate shared location (`tools/`) so others can reuse it.

- Never duplicate a helper inline across files; parameterise a shared function to cover variant contexts.
- Follow the conventions established in sibling applications (libraries, structure, naming). Consistency takes priority over local preference.
- Prefer library-provided functions over hand-rolled logic for sanitisation, encoding, serialisation, etc.

### Code Clarity
- Names (functions, variables, types) must be descriptive enough to make their purpose obvious without a comment.
- Comments must add information the code cannot express — explain *why*, not *what*. Never write a comment that just restates the line it sits next to.
- Prefer fewer, meaningful comments over many redundant ones.

### Single Responsibility Principle
- Code must be well composed, with clear responsibilities for each component. This applies both in terms of subject matter (separate controllers for separate CRD resource types) and in terms of clients (How something is done) and orchestrators (When something is done).
- Responsibility boundaries must be structural — enforced by distinct types or packages — not cosmetic. Reorganising code into separate files without changing ownership does not reduce a component's scope.

### External Dependency Ownership
All interaction with an external system (database, message broker, HTTP service) must be encapsulated in a single package behind an exported interface. Other packages depend on the interface, never on the external system directly. This ensures that consumers can be unit-tested with fakes and that external-system concerns (connection handling, transactions, retries) live in one place.

## Kubernetes

### Controllers

**Governing principle:** Assume all data is eventually consistent and may be stale — even data read directly from the API server.

- Use the informer cache for all reads. Fall back to a direct read only when caching is infeasible (high-churn objects, memory pressure); in that case, filter by namespace/labels and prefer metadata-only calls.
- Guard status writes with a state check — skip the write if nothing has changed.
- Use deterministic names for child objects so that optimistic locking detects conflicts naturally.
- Send updates/patches with the last-known `resourceVersion`. On conflict, return an error and let the work queue retry with backoff.
- After a write (Create/Update/Patch/Delete), use the object returned by the API server — do not re-read from the cache, which may contain an older `resourceVersion`.
- If using `generateName`, track outstanding creates in memory and retry if the expected watch event does not arrive within a reasonable timeout.
- Reuse existing caches (e.g. controller watches) rather than adding direct reads for the same object type.

## Testing

**Governing principle:** Test at the highest level that exercises the code path efficiently. Drop to a lower level only when combinatorial complexity makes the higher level impractical.

### End-to-End Tests
- Test all user workflows through the same interface the user would use.

### Integration Tests
- Deploy the application into a dedicated test namespace and test against real services (database, cluster, etc.).
- Aim for the majority of test coverage here — prefer shared resources (e.g. one database instance for multiple assertions) over per-test isolation.
- Access services the way a real consumer would (e.g. port-forward and connect over the network) rather than using cluster-internal shortcuts like pod exec. This keeps tests representative of actual usage and lets them run from the host against a development cluster.

### Unit Tests
- Reserve for complex logic with many input permutations and minimal external dependencies.

### Test Design Rules
- Test through exported entry points. Never export a function solely for testability — exercise it indirectly via the public API.
- Every test double must be exercised. An unused fake indicates a missing code path — delete it or rewrite the test.
- Use `gomega` (`Expect(...).To(...)` with `RegisterTestingT(t)`) for assertions. No raw `if … { t.Errorf }`.
- If a component can't be unit-tested without its external dependency, refactor the dependency behind an interface that a fake can replace.
