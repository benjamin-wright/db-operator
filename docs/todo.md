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
- The controller resolves artifact tags on every reconcile, so a changed
  registry digest can request another Job without a resource change. The runner
  already rejects changes to SQL recorded as applied in the database ledger;
  a new Job does not authorize replaying or replacing applied migrations.
- [The SQL store](../internal/migrations/store/store.go) uses a pooled `*sql.DB`
  for session-scoped lock/unlock and migration operations. The existing fake
  store tests establish call order, not a retained-session guarantee.

## 1. Specify claim storage and controller recovery

- [x] Describe the agreed semantics and an initial status shape in the
  [design](migrations-design.md#implementation-readiness). Implementation and
  protocol verification remain below.
- [x] Specify the target encoding, Lease name, and immutable owner annotations
  in the [admission design](migrations-design.md#admission-time-reservation).
- [x] Settle target identity and initial-deployment scope in the
  [ownership design](migrations-design.md#responsibility-and-ownership): instance
  namespace/name plus logical database name; fresh development clusters require
  no bootstrap of pre-existing migration resources or Jobs.
- [x] Settle [claim storage and access](migrations-design.md#claim-storage-and-access):
  operator namespace, protected by standard namespace RBAC, with a namespaced
  Lease role for the operator service account.
- [x] Settle [webhook TLS and rollout](migrations-design.md#webhook-tls-and-deployment-ordering):
  cert-manager SelfSigned Issuer and Certificate, mounted TLS Secret, CA injection
  on the webhook configuration, and readiness checks with admission failing closed.
- [x] Agree the [controller lifecycle requirements](migrations-design.md#controller-lifecycle-and-orphan-cleanup):
  controller-owned orphan cleanup, outstanding-work checks, conditional release,
  missing-claim recovery, and coordination with scheduling. Implementation and
  verification remain below.
- [x] Settle [explicit retries](migrations-design.md#explicit-retries): timestamped
  `db-operator.benjamin-wright.github.com/retryAt` annotation, compared as an
  opaque token and acknowledged in `status.lastHandledRetryAt`.
- [x] Settle [rollback authorization](migrations-design.md#forward-and-rollback-authorization):
  the same resource-update authorization and operator-managed migration role
  apply to forward migration and rollback, with no additional approval flag.
- [x] Draft the [operation-status shape](migrations-design.md#proposed-operation-status):
  spec remains the latest request; retain the current/latest attempt and last
  successful attempt, with Job references and retry-token attribution.
- [x] Settle [artifact resolution and integrity](migrations-design.md#mutable-artifact-references):
  resolve for an attempt requested by the resource, including explicit retry
  after failure, and retain that digest through recovery. Registry changes alone
  do not request work; the database ledger keeps applied migration SQL immutable.

## 2. Implement admission and controller claim lifecycle

- [ ] Implement the shared claim encoding/naming helper and annotation constants
  defined in the design for use by admission and controller recovery.
- [ ] Add validating admission for migration-set creates and updates, including
  immutable database bindings and an atomic Lease reservation before acceptance.
- [ ] Implement Create/Get admission: derive the Lease name from the target and
  store it in the configured operator namespace; verify resource namespace/name,
  handle `AlreadyExists` races, and reject competing addresses. Retries and
  same-name recreation require no Lease updates.
- [ ] Implement controller orphan detection and removal with claim watches and
  follow-up reconciliation, including when the resource never persisted. Choose
  and document grace/recheck intervals; check outstanding execution, including
  previous resource UIDs, and use conditional Lease deletion.
- [ ] Gate migration scheduling on the current target-to-address association.
  Handle missing or invalid claims through controller recovery and preserve
  resource-UID attribution for Jobs, status, and cleanup.
- [ ] Implement and verify coordination between cleanup, missing-claim repair,
  and scheduling. A prior ownership check must not authorize a new Job after
  claim release or reassignment. Cover delayed persistence and lost responses;
  grace periods and conditional Lease deletion alone do not resolve these races.
- [ ] Add deletion/finalizer handling and safe claim release after outstanding
  execution finishes. Preserve database contents and migration history.

## 3. Wire admission into deployment

- [ ] Register the webhook server and handlers in
  [cmd/db-operator](../cmd/db-operator/main.go).
- [ ] Supply the operator namespace consistently to admission and claim recovery;
  restrict Lease watches to that namespace and preserve ownership across operator
  instance selectors and restarts.
- [ ] Extend `charts/db-operator/templates/operator/` with the webhook Service,
  namespaced SelfSigned Issuer and Certificate, TLS Secret mount, and
  `ValidatingWebhookConfiguration` annotated with `cert-manager.io/inject-ca-from`.
  Configure serving-certificate reload and verify `failurePolicy: Fail`,
  `sideEffects: NoneOnDryRun`, and rule scope from the design.
- [ ] Add the namespaced Lease Role and RoleBinding for the operator service
  account, granting get/list/watch/create/delete without a cluster-wide claim
  permission or custom claim-protection webhook.
- [ ] Document the cert-manager prerequisite and installation readiness checks;
  retain claims and webhook protection during upgrades and certificate renewal.
- [ ] Update the integration environment to exercise real API admission and
  cleanup, rather than relying solely on direct reconciler calls.

## 4. Reconcile intent independently of Job retention

- [ ] Replace the current Job-existence-driven decision in
  [the controller](../internal/operator/controller/postgresmigrationset_controller.go)
  with the execution/completion contract in the design.
- [ ] Implement the proposed operation record and last-successful record, with
  captured inputs, retry tokens, resolved digests, Job names/UIDs, and outcomes.
  Scope attempts to the resource UID and immutable
  database binding; keep status bounded rather than storing an attempt history.
- [ ] Persist the operation snapshot, resolved digest, chosen Job name, and
  retry acknowledgement before Job creation, using conditional status writes.
  Reuse the stored Job name on uncertain create outcomes and verify known Job
  and owner UIDs; no separate operation ID is needed.
- [ ] Derive current conditions from the latest desired state and attempt
  outcome; keep spec observation distinct from successful completion. Surface
  uncertain execution as recovery work, never as success or an automatic retry.
- [ ] Replace flat `observedArtifact`, `activeJob`, and `currentRevision` fields
  with operation fields and `lastSuccessfulOperation.targetRevision`; regenerate
  the CRDs and update printer columns and consumers. Do not present the last
  successful target as a live database revision after partial failure.
- [ ] Resolve artifacts only when preparing an attempt requested by creation,
  changed artifact reference or target revision, or explicit retry after failure.
  Persist the resolved digest and reuse it during ordinary reconciliation and
  recovery; do not use registry changes as execution triggers.
- [ ] Record a finished Job against its original operation; process the latest
  desired state after in-flight work completes. Returning to an earlier target
  after a rollback must not reuse an old successful Job as current completion.
- [ ] Implement the retry annotation and `lastHandledRetryAt` acknowledgement.
  Observe annotation-only updates, persist the association between token and
  attempt, and recover Job/status write failures without losing or duplicating
  a retry. Preserve pause and in-flight-work ordering.
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
- [ ] Tenant users can submit migration sets in their own namespaces but cannot
  create, edit, or delete claims in the operator namespace. The operator's claim
  Role permits the required lifecycle operations only in its namespace.
- [ ] A fresh installation becomes ready with cert-manager issuance and CA
  injection. Upgrades and certificate renewal preserve claims, reload serving
  certificates, and resume admission with no fail-open window.
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
- [ ] Recreating a `PostgresDatabase` at the same namespace/name preserves the
  target key for the same logical database. Resource UID and restored data do
  not introduce a different ownership key.
- [ ] Successful and failed Job cleanup, status updates, and controller restarts
  do not request new migrations. Explicit retry follows its settled contract.
- [ ] Republishing a tag without a new execution request causes no tag refresh
  or new Job, including during ordinary reconciliation and controller restarts.
  An explicit retry after failure may resolve the new digest; recovery of that
  attempt remains pinned to its recorded digest.
- [ ] An explicitly requested attempt with unchanged applied migration hashes
  and an already-reached target executes no migration SQL. Changed or missing
  applied SQL fails integrity checks, including when supplied through a new
  artifact or retry; adding a later revision preserves the existing history.
- [ ] A fresh retry token permits one additional attempt after failure; repeated
  events with the handled token and annotation removal do not request another.
  Lost responses and restarts preserve the attempt. Paused resources defer
  handling, active Jobs finish first, and successful desired operations are not
  rerun by a retry request.
- [ ] Apply revision 1, roll back to 0, then request 1 again while the original
  success Job is retained: SQL is reapplied and completion describes the new run.
  Forward migration and rollback use the same operator-managed credentials and
  resource-update authorization, without a rollback-specific approval step.
- [ ] Desired-state changes during execution report the completed operation
  accurately, then reconcile the latest request. Pausing retains ownership.
- [ ] A Job TTL edit advances observed generation without new execution; a retry
  annotation can create a new attempt without a generation change. Delayed Job
  events cannot overwrite the result of another attempt or resource UID.
- [ ] After a partially committed failure, retain the previous successful
  operation and the failed attempt separately. Conditions report failure or
  recovery accurately, even when an older successful target matches the spec.
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
