# PostgresMigrationSet implementation plan

The [migration ownership design](migrations-design.md) is the source of truth for
the planned admission and ownership protocol. Work starts in db-operator;
wasm-platform design and integration changes follow once this contract is settled
and verified here.

API types, artifact fetching, the internal migrations role, Job client/builder,
controller wiring, RBAC, and integration scenarios already exist. Their presence
does not establish completion: the current controller calls an undefined
`reconcileMigrationsDatabase`, and the admission protocol is not implemented.
The phases below replace the earlier checklist that treated missing Jobs as
authorization to execute and delegated migration ownership to wasm-platform.

## 1. Restore and verify the baseline

- [ ] Reconcile `reconcileMigrationsDatabase` and `ensureMigrationDatabase` in
  [the migration controller](../internal/operator/controller/postgresmigrationset_controller.go).
  Restore compilation while preserving the intended database and schema grants.
- [ ] Run `go test ./...` and the existing migration-set integration suite.
  Record which lifecycle scenarios pass before changing their semantics.
- [ ] Review existing tests and APIs against the design; retain useful coverage
  without treating old unchecked tasks as missing implementations.

## 2. Specify claim storage and controller recovery

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

## 3. Implement admission and controller claim lifecycle

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

## 4. Wire admission into deployment

- [ ] Register the webhook server and handlers in
  [cmd/db-operator](../cmd/db-operator/main.go).
- [ ] Add Helm webhook configuration, Service, certificate provisioning, and
  installation/upgrade ordering. Verify `failurePolicy: Fail`,
  `sideEffects: NoneOnDryRun`, and rule scope from the design.
- [ ] Add narrowly scoped Lease permissions and any admission-related RBAC;
  preserve ownership across operator instance selectors and restarts.
- [ ] Update the integration environment to exercise real API admission and
  cleanup, rather than relying solely on direct reconciler calls.

## 5. Reconcile intent independently of Job retention

- [ ] Replace the current Job-existence-driven decision in
  [the controller](../internal/operator/controller/postgresmigrationset_controller.go)
  with the execution/completion contract in the design.
- [ ] Include the database binding in execution identity; distinguish resolved,
  running, and successfully applied operations in status.
- [ ] Record a finished Job against its original operation; process the latest
  desired state after in-flight work completes.
- [ ] Retain success and failure outcomes after Job cleanup. Recover uncertain
  execution through the database ledger rather than assuming a missing Job
  authorizes another attempt.
- [ ] Verify credential re-reconciliation after successful migration, including
  credentials previously blocked on `WaitingForTable`.
- [ ] Verify advisory-lock acquisition and release on the intended database
  session, including success, plan errors, and execution errors.

## 6. Acceptance coverage

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
- [ ] Desired-state changes during execution report the completed operation
  accurately, then reconcile the latest request. Pausing retains ownership.
- [ ] Standalone db-operator usage passes without wasm-platform being deployed.

## 7. Align shipped documentation

- [ ] Update [the operator spec](../cmd/db-operator/spec.md) with verified
  `PostgresMigrationSet` behavior and remove obsolete `databaseOwner` claims.
  Keep protocol implementation details in the design document.
- [ ] Update [the runner spec](../cmd/db-migrations/spec.md) for artifact mode,
  file integrity, supported revision targets, and advisory-lock behavior.
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

The older per-application `databaseOwner` proposal and platform-owned migration
Job plan are superseded context, not implementation tasks for this operator.
