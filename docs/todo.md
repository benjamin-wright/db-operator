# PostgresMigrationSet implementation plan

The [migration ownership design](migrations-design.md) is the source of truth for
the planned admission and ownership protocol. Work starts in db-operator;
wasm-platform design and integration changes follow once this contract is settled
and verified here.

## Implemented baseline

Reviewed against upstream `93affcb` on 2026-09-25. The existing migration rollout
is implemented; the ownership and execution changes in the design are follow-on
work, not missing pieces of that rollout.

- The controller calls `ensureMigrationDatabase` and compiles. API types, OCI
  artifact fetching, the internal migrations role, Jobs, controller registration,
  and deployment RBAC are present. The operator chart now contains the optional
  MCP deployment under `templates/mcp/` and operator resources under
  `templates/operator/`.
- Credential re-reconciliation after migration success, sequence-grant fixes,
  migration-role password synchronization, rollback to revision zero, and
  migration-file discovery fixes are present. The runner has advisory-lock call
  ordering tests. Existing integration scenarios cover apply, rollback, mutable
  tags, pausing, in-flight work, and credentials waiting for migrated tables.
- `go test ./...` passes, and the migration/controller integration suites compile
  with `-tags=integration -run '^$'`. Live integration tests were not rerun during
  this review: neither the current kubeconfig nor the suite's fallback
  `~/.scratch/db-operator.yaml` has a current context. Upstream's completed
  checklist reports previous integration verification.

The remaining assumptions were checked against the implementation:

- There is no migration admission webhook, ownership Lease, immutable-binding
  validation, or migration-set finalizer. These remain new work.
- [Job selection](../internal/operator/controller/postgresmigrationset_controller.go)
  still determines whether to execute. Successful Job deletion can trigger
  another attempt. Returning to an earlier target while its successful Job is
  retained can reuse stale success instead of migrating back to that target.
  Failed Jobs have no TTL cleanup in the failure handler.
- [Job lookup](../internal/operator/controller/postgresmigrationset_client.go)
  filters by migration-set name, without verifying the owner UID. The Job key
  contains artifact digest and target revision but omits the database binding.
- [The SQL store](../internal/migrations/store/store.go) uses a pooled `*sql.DB`
  for session-scoped lock/unlock and migration operations. The existing fake
  store tests establish call order, not a retained-session guarantee.

## 1. Specify claim storage and controller recovery

- [ ] Resolve the [open protocol details](migrations-design.md#details-to-settle-before-implementation),
  including controller orphan cleanup, target replacement, and bootstrap of
  existing resources.
- [ ] Specify the canonical target encoding, Lease name, and immutable resource
  namespace/name annotations. The design requires no candidate tracking or UID
  confirmation on the Lease.
- [ ] Define controller grace/recheck timing, outstanding-work checks,
  conditional deletion, missing-claim repair, and cleanup/scheduling coordination.
- [ ] Define operation-status and retry fields; settle rollback authorization and
  artifact refresh policy before changing their existing behavior.

## 2. Implement admission and controller claim lifecycle

- [ ] Add validating admission for migration-set creates and updates, including
  immutable database bindings and an atomic Lease reservation before acceptance.
- [ ] Implement Create/Get admission: derive the Lease location from the target,
  verify resource namespace/name, handle `AlreadyExists` races, and reject
  competing addresses. Retries and same-name recreation require no Lease updates.
- [ ] Implement controller orphan detection and removal with claim watches and
  follow-up reconciliation, including when the resource never persisted.
- [ ] Gate migration scheduling on the current target-to-address association.
  Handle missing or invalid claims through controller recovery and preserve
  resource-UID attribution for Jobs, status, and cleanup.
- [ ] Add deletion/finalizer handling and safe claim release after outstanding
  execution finishes. Preserve database contents and migration history.

## 3. Wire admission into deployment

- [ ] Register the webhook server and handlers in
  [cmd/db-operator](../cmd/db-operator/main.go).
- [ ] Add Helm webhook configuration, Service, certificate provisioning, and
  installation/upgrade ordering. Verify `failurePolicy: Fail`,
  `sideEffects: NoneOnDryRun`, and rule scope from the design.
- [ ] Add narrowly scoped Lease permissions and any admission-related RBAC;
  extend the existing `charts/db-operator/templates/operator/` resources and
  preserve ownership across operator instance selectors and restarts.
- [ ] Update the integration environment to exercise real API admission and
  cleanup, rather than relying solely on direct reconciler calls.

## 4. Reconcile intent independently of Job retention

- [ ] Replace the current Job-existence-driven decision in
  [the controller](../internal/operator/controller/postgresmigrationset_controller.go)
  with the execution/completion contract in the design.
- [ ] Include the database binding in execution identity; distinguish resolved,
  running, and successfully applied operations in status.
- [ ] Record a finished Job against its original operation; process the latest
  desired state after in-flight work completes. Returning to an earlier target
  after a rollback must not reuse an old successful Job as current completion.
- [ ] Verify the Job's controller owner UID when associating it with a migration
  set; keep detection of older in-flight work separate from success attribution.
- [ ] Retain success and failure outcomes after Job cleanup. Recover uncertain
  execution through the database ledger rather than assuming a missing Job
  authorizes another attempt. Implement consistent retention for failed Jobs.
- [ ] Preserve the existing credential re-reconciliation and sequence grants;
  rerun their integration coverage after changing migration completion handling.
- [ ] Ensure advisory-lock acquisition, migration work, and release retain the
  intended database session. Keep the existing runner call-order tests and add
  coverage that exercises the actual store/session behavior.

## 5. Acceptance coverage

- [ ] Concurrent creates with different user-chosen names targeting the same
  logical database, including different revisions: exactly one reservation
  wins, the competing request is rejected, and only the owner schedules work.
- [ ] Independent target databases remain independently claimable.
- [ ] Concurrent creates that both observe an absent Lease resolve through
  atomic creation; the loser checks the winning claim after `AlreadyExists`.
- [ ] Repeated admission calls and client retries at the same resource address
  reuse the claim without updating it; ordinary owner updates succeed and
  binding changes are rejected before reserving another target.
- [ ] Dry runs create no reservation. Webhook failure prevents real admission.
- [ ] The controller discovers and removes orphan claims after later admission
  rejection, storage failure, or uncertain responses, even with no resource event
  and across controller restarts. Pending creates receive the documented grace
  period; live resources and unresolved execution prevent orphan removal.
- [ ] Delayed persistence and cleanup/scheduling races obey the execution guard.
  A missing or differently assigned claim blocks work until controller recovery;
  a matching current claim permits the named resource under the agreed semantics.
- [ ] Stale cleanup cannot delete a changed or recreated Lease; conditional
  deletion failures cause re-evaluation.
- [ ] Controller restarts retain live claims. Same-name recreation passes
  admission but cannot accidentally inherit previous-UID Jobs or status.
  Deletion during execution does not release the claim early.
- [ ] Missing claims, target replacement, and pre-existing duplicate resources
  follow the chosen bootstrap/recovery procedure without overlapping execution.
- [ ] Successful and failed Job cleanup, status updates, and controller restarts
  do not request new migrations. Explicit retry follows its settled contract.
- [ ] Apply revision 1, roll back to 0, then request 1 again while the original
  success Job is retained: SQL is reapplied and completion describes the new run.
- [ ] Desired-state changes during execution report the completed operation
  accurately, then reconcile the latest request. Pausing retains ownership.
- [ ] Standalone db-operator usage passes without wasm-platform being deployed.

## 6. Align shipped documentation

- [ ] Update [the operator spec](../cmd/db-operator/spec.md) with verified
  ownership and execution behavior. Its `PostgresMigrationSet` section already
  exists, but claims about permanent Job deduplication and failed-Job TTL do not
  match the code. Keep protocol implementation details in the design document.
- [ ] Remove obsolete `PostgresCredential.spec.databaseOwner` examples and
  claims from README and the operator spec; the field is absent from the API.
  Correct the claim that the internal migrations role owns the logical database:
  the current controller creates it via the admin connection and grants schema
  access to the role without transferring database ownership.
- [ ] Align [the runner spec](../cmd/db-migrations/spec.md) with its implemented
  artifact workflow, revision-zero semantics, and verified advisory-lock
  behavior; artifact mode and integrity checking are already documented.
- [ ] Update [README migration examples](../README.md#migrations) to match the
  implemented artifact, retry, ownership, and rollback contract; remove the
  planned-feature notice only after admission is verified.
- [ ] Document the standalone recovery workflow and refresh this checklist with
  verified completion rather than inferring completion from file presence.

## Later: consumer alignment

After the db-operator design is settled and its implementation is verified:

- [ ] Revisit wasm-platform's migration design and dependency versions to consume
  the verified db-operator contract instead of creating its own migration Jobs.
- [ ] Define application activation against the requested migration completion
  and credential readiness, then verify the cross-repository e2e workflow.
- [ ] Carry forward upstream's wasm-platform credential-ordering follow-up
  ("wp-operator Fix C") under that contract. Verify sequence access end to end
  with the existing db-operator grant fixes; do not reimplement those fixes.
- [ ] Run the wasm-platform e2e suite after updating the consumer. Its current
  Go dependency and deployed chart still predate this operator baseline.

The older per-application `databaseOwner` proposal and platform-owned migration
Job plan are superseded context, not implementation tasks for this operator.
