# Migration ownership and admission

Status: agreed design direction; implementation and protocol verification remain
tracked in [todo.md](todo.md). This document describes planned behavior, not a
claim that the current operator implements it. Consumer changes, including
wasm-platform integration, follow the db-operator work.

## Responsibility and ownership

`PostgresMigrationSet` expresses the desired migration state of one logical
PostgreSQL database. Its name is chosen freely by the user; users do not construct
names from database properties or manage ownership Leases themselves.

One migration set owns the database's entire migration history. The target key
is the PostgreSQL instance namespace, instance name (`spec.databaseRef`), and
logical database name (`spec.database`). The current API references an instance
in the migration set's own namespace. Revision is not part of the key: resources
targeting revisions 10 and 11 of the same database would otherwise express
conflicting desired states. The database binding is immutable for the lifetime
of the migration set. Operator instance labels must not create independent
ownership domains for the same target database.

db-operator owns admission, migration execution, history, and credential grant
reconciliation. Application frameworks own release sequencing and activation.

## Admission-time reservation

Admission enforces one immutable association: target database to migration-set
namespace/name. Kubernetes enforces uniqueness of the migration resource at that
address. Admission does not distinguish individual create attempts or resource
incarnations at the same address.

Use an internal `coordination.k8s.io/v1` Lease in the target instance's namespace.
Its name is a hash of a canonical, unambiguous encoding of the target key, with an
operator-specific prefix. Record the migration set's name and namespace in
operator-owned annotations that remain immutable while the Lease exists. The
unhashed target may also be recorded for diagnosis, especially when an orphan's
resource never persisted; it is not part of the admission ownership check. The
Lease name is an implementation detail; users retain free choice of
migration-set names.

The validating webhook processes real creates and updates as follows:

1. Validate the requested binding, rejecting changes to an existing resource's
   binding, and derive the target key and Lease location.
2. Read the Lease. If absent, atomically create it with the owner annotations.
   Successful creation establishes the reservation. Observing absence alone
   does not: if creation returns `AlreadyExists`, read the winning Lease and
   apply the same checks as for any existing claim.
3. For an existing Lease at that target's location, allow only when the owning
   migration-set namespace/name matches. Reject a different owner with an error
   identifying it. Missing or malformed ownership annotations require controller
   recovery; admission must not overwrite them.
4. Return `allowed: true` only after successful creation or verification of a
   matching claim. On an uncertain write outcome, verify the stored claim or
   return an error. Retain the reservation after the webhook returns.

Repeated webhook calls, client retries, and ordinary owner updates read the same
claim without changing it. Same-name recreation is eligible for the same claim;
execution and cleanup of each persisted resource incarnation belong to the
controller. There is no candidate list, Lease phase transition, or UID-binding
update in admission. AdmissionReview request UIDs are for diagnostics, not
ownership. Admission never updates or deletes a Lease and never launches a Job.

Configure the webhook with `failurePolicy: Fail` and
`sideEffects: NoneOnDryRun`. Dry runs validate and report known conflicts without
writing a Lease; a successful dry run does not reserve ownership. Scope webhook
rules to avoid intercepting the Lease writes needed to perform admission.

## Controller lifecycle and orphan cleanup

The controller owns orphan detection and removal, execution checks, and claim
release. The Lease is a durable reservation for a resource namespace/name;
controller restarts and missed renewals do not transfer it. There is no
heartbeat-based expiry or leader-election takeover.

Before scheduling, verify that the persisted migration set's target and address
match the current Lease. Establish finalizer protection before execution. Use
the resource's Kubernetes UID for Job ownership, status attribution, and cleanup
so a recreated resource cannot accidentally inherit an earlier incarnation's
work. This does not require binding or confirming the resource UID on the Lease.

Deletion stops new scheduling. The finalizer retains the claim until outstanding
execution has finished, then the controller releases it. Release does not roll
back migrations or delete the logical database. Automatic garbage collection
must not bypass this ordering.

The Lease write and migration-resource write are separate transactions. Later
admission or storage failure can leave a claim whose resource never persists.
The controller must watch operator claims and schedule follow-up checks so it
can detect and remove these orphans even when no migration-set event occurs,
including after a controller restart. It checks the referenced resource's
existence and binding and accounts for outstanding migration execution,
including work associated with previous resource UIDs, before removal. Uncertain
reads or unresolved execution must not be treated as proof that cleanup is safe.

Define a grace/recheck policy for pending creates in the implementation. Age or
a missing-resource read cannot prove that an admitted request will never persist.
Cleanup must therefore work with the execution guard: a late resource whose
claim is absent or assigned to a different address must not schedule work.
Requeue for controller recovery; any repair of a missing claim must obey the
same atomic target-to-address rule. A late resource matching the current claim
is eligible under the same-name semantics; there is no attempt-level history to
reconstruct. Recovery may leave conflicting resource objects present, but they
must not gain concurrent authority to execute.

Delete only the inspected Lease incarnation, using UID and resourceVersion
preconditions so a delayed cleanup cannot delete a replacement claim. Reconcile
again on a conflict. These cleanup conditions are controller concerns and add
no updates to ordinary admission. Cleanup and scheduling races, forced deletion,
target-instance replacement, and recovery after loss of Kubernetes state must
be covered by the controller recovery rules and tests. Retain the PostgreSQL
advisory lock to serialize actual migration execution; a Kubernetes claim does
not terminate an already-running Job or database session.

## Execution intent and completion

Resource state is the source of desired migration intent. Reconciliation must
work after restarts and repeated events; it is not an event log of commands.
The execution identity includes the bound database, resolved artifact digest,
and requested target revision. Metadata and Job retention changes do not request
another migration. Pausing prevents new work without releasing ownership.

Record requested, running, and successfully applied operations distinctly.
Job completion is attributed to the operation the Job executed, even if the
resource has since changed. Finish in-flight work before acting on the latest
desired state. The database ledger supports idempotent recovery when the outcome
of an attempt is uncertain.

Persist completion independently of Job retention. Cleaning up a successful or
failed Job does not authorize a fresh attempt. Missing downstream objects alone
are not evidence that new migration work was requested. Consumers must match
completion to their requested operation and separately wait for required
credential grants to become ready.

## Details to settle before implementation

- Specify the canonical target encoding, hash/prefix, and annotation keys for
  the agreed immutable target-to-resource-address claim.
- Specify controller orphan detection and removal: grace/recheck timing,
  outstanding-work checks, conditional deletion, missing-claim repair, and
  coordination with scheduling. Cover delayed persistence and lost responses;
  admission has no candidate-correlation or orphan-cleanup protocol.
- Define target identity across `PostgresDatabase` deletion/recreation and
  restored data, and the bootstrap procedure for existing migration sets,
  duplicate bindings, and in-flight Jobs when admission is first enabled.
- Specify the operation-status fields and explicit retry mechanism. Resolve
  rollback authorization and mutable-artifact refresh policy separately; this
  ownership decision does not settle those API choices.
- Specify webhook certificate provisioning, startup/upgrade ordering, narrowly
  scoped RBAC, and protection of internal claims from ordinary user edits.

## References

- [Kubernetes admission side effects](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#side-effects)
  explain why successful admission does not guarantee persistence and requires
  reconciliation of external reservations.
- [Kubernetes object names and IDs](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)
  distinguish a unique resource address from its successive UID-bearing
  incarnations.
- [Kubernetes deletion preconditions](https://kubernetes.io/docs/reference/kubernetes-api/definitions/delete-options-v1-meta/)
  guard controller cleanup against deleting a changed or replacement claim.
- [client-go leader election](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection)
  documents the absence of a guarantee that a former leader has stopped acting.
