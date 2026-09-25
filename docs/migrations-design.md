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

The PostgreSQL instance's namespace/name is its identity for ownership purposes.
Deleting and recreating a `PostgresDatabase` at that address preserves the target
identity for each logical database name. Its Kubernetes UID and database contents,
including restored data, do not contribute to the claim key.

Initial deployment assumes a fresh development Kubernetes cluster. Existing
clusters can be torn down and recreated to adopt this design. Bootstrapping
pre-existing migration sets, resolving their duplicate bindings, and adopting
their in-flight Jobs when admission is first enabled are out of scope. Ordinary
controller restarts and orphan cleanup within an installation remain supported.

db-operator owns admission, migration execution, history, and credential grant
reconciliation. Application frameworks own release sequencing and activation.

## Admission-time reservation

Admission enforces one immutable association: target database to migration-set
namespace/name. Kubernetes enforces uniqueness of the migration resource at that
address. Admission does not distinguish individual create attempts or resource
incarnations at the same address.

Use an internal `coordination.k8s.io/v1` Lease with this fixed record format:

- **Namespace:** the operator's own namespace (`.Release.Namespace` in the Helm
  deployment), supplied consistently to admission and controller recovery.
- **Target encoding:** the UTF-8 bytes returned by Go's
  `encoding/json.Marshal([3]string{namespace, databaseRef, database})`, in that
  order, with no trailing newline. Use the field values unchanged, with no case
  folding or Unicode normalization. For example: `["my-app","postgres","orders"]`.
  Here `namespace` is the target instance's namespace, currently the migration
  set's `metadata.namespace`; it is independent of the Lease storage namespace.
- **Name:** `pgms-` followed by the full lowercase hexadecimal SHA-256 digest of
  those bytes. Revision, artifact, migration-set name, resource UID, and operator
  instance label are excluded.
- **Owner name annotation:**
  `db-operator.benjamin-wright.github.com/migration-set`, containing the migration
  set's `metadata.name`.
- **Owner namespace annotation:**
  `db-operator.benjamin-wright.github.com/migration-set-namespace`, containing its
  `metadata.namespace`.
- **Optional diagnostic annotation:**
  `db-operator.benjamin-wright.github.com/migration-target`, containing the encoded
  target string. This helps identify an orphan's target when its resource never
  persisted; it does not participate in the admission ownership check.

The owner annotations remain immutable while the Lease exists. The Lease name is
an implementation detail; users retain free choice of migration-set names.
Admission and controller recovery must use the same encoding and naming helper.

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

## Claim storage and access

Store all claims in the operator namespace. Standard namespace RBAC provides
the protection: tenant users and workload service accounts have no write access
to that namespace. No additional admission policy for protecting claims is
required. Cluster administrators and operator-namespace administrators remain
trusted. All operator instances managing the same target must use the same
claim namespace; separate installations in different namespaces must manage
disjoint targets.

Grant the operator service account a namespaced Role and RoleBinding for
`coordination.k8s.io` `leases`, with `get`, `list`, `watch`, `create`, and `delete`.
Admission uses get/create; controller recovery also watches and deletes claims.
The claim protocol needs no update/patch permission or cluster-wide Lease grant.
Keep any leader-election Lease permissions separate from this claim role.
Scope claim watches to the operator namespace and identify migration claims
using their reserved name prefix and ownership annotations.

## Webhook TLS and deployment ordering

Require cert-manager, including its CA injector, to be installed and ready before
installing db-operator. The operator chart provisions a namespaced `Issuer` with
`spec.selfSigned: {}` and a serving `Certificate` in the operator namespace. The
Certificate names the webhook Service in its DNS SANs, including
`<service>.<operator-namespace>.svc`, and writes its certificate and private key
to a TLS Secret. Mount that Secret read-only into the webhook pod and configure
the server to reload renewed certificates.

Put `cert-manager.io/inject-ca-from: <operator-namespace>/<certificate-name>` on
the `ValidatingWebhookConfiguration`. The CA injector fills its `caBundle` from
the Certificate. This annotation belongs on the webhook configuration; the pod
consumes the Secret through its volume mount.

After cert-manager is ready, install the Issuer, Certificate, Service, operator
Deployment, RBAC, and webhook configuration. These may be applied together, but
wait for the Certificate to be Ready, the Secret to be mounted and served, the
Service to have ready webhook endpoints, and the CA bundle to be injected before
applying migration sets. Verify admission through the Kubernetes API as the
installation readiness check. The webhook's rules must allow its own supporting
resources to be created during this startup sequence.

During upgrades, retain the claims, certificate resources, and registered webhook
while rolling the serving pods. Keep `failurePolicy: Fail` throughout startup,
upgrades, and certificate renewal. A temporarily unavailable endpoint or TLS
trust mismatch rejects admission until it recovers. Verify certificate reload
and CA injection during renewal; uninterrupted admission during rotation is not
a requirement for these development installations.

## Controller lifecycle and orphan cleanup

The controller owns orphan detection and removal, execution checks, and claim
release. The Lease is a durable reservation for a resource namespace/name;
controller restarts and missed renewals do not transfer it. There is no
heartbeat-based expiry or leader-election takeover.

The behavior in this section is agreed. Concrete grace/recheck intervals and
the reconciliation algorithm are implementation choices that must satisfy these
requirements, rather than additional ownership-semantics decisions.

Before scheduling, verify that the persisted migration set's target and address
match the current Lease. Establish finalizer protection before execution. Use
the resource's Kubernetes UID for Job ownership, status attribution, and cleanup
so a recreated resource cannot accidentally inherit an earlier incarnation's
work. This does not require binding or confirming the resource UID on the Lease.

Deletion stops new scheduling. The finalizer retains the claim until outstanding
execution has finished, then the controller releases it. Release does not roll
back migrations or delete the logical database. Automatic garbage collection
must not bypass this ordering. Claims have no owner reference to the migration
set: it can live in a different namespace, and controller cleanup explicitly
controls claim release.

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
no updates to ordinary admission. The implementation must also coordinate
cleanup with scheduling: a prior matching-claim read must not authorize a new
Job after the claim has been released or reassigned. Conditional Lease deletion
alone does not provide this coordination. Cleanup and scheduling races and forced
deletion within an installation must be covered by the controller recovery rules
and tests. Recreating the entire Kubernetes cluster starts a fresh installation
without a bootstrap or claim-reconstruction procedure. Retain the PostgreSQL
advisory lock to serialize actual migration execution; a Kubernetes claim does
not terminate an already-running Job or database session.

## Execution intent and completion

Resource state is the source of desired migration intent. Reconciliation must
work after restarts and repeated events; it is not an event log of commands.
Determine whether execution is requested from the migration resource and its
recorded outcome before resolving an artifact. Creation, changes to
`spec.artifact` or `spec.targetRevision`, and an explicit retry of a failed
operation can request an attempt. Record its bound database, resolved artifact
digest, and requested target revision. The digest identifies the content used
by that attempt; discovering different registry content is not an execution
trigger. Ordinary metadata, status, and Job retention changes do not request
another migration; the explicit retry annotation below is an intentional
exception. Pausing prevents new work without releasing ownership.

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

### Proposed operation status

Use `spec.artifact`, `spec.targetRevision`, and the retry annotation as the latest
request. Status needs an immutable snapshot of the attempt being processed, not
a second desired-state object or a queue. A first implementation can use:

```yaml
status:
  observedGeneration: 7
  phase: Running
  conditions: [] # Standard metav1.Condition entries, including Ready.
  lastHandledRetryAt: "2026-09-25T15:00:00Z"
  operation:
    generation: 7
    artifact: "registry.example.com/migrations:dev"
    targetRevision: 10
    retryAt: "2026-09-25T15:00:00Z"
    resolvedArtifact: "registry.example.com/migrations@sha256:<digest>"
    phase: Running
    jobRef:
      name: "<persisted Job name>"
      uid: "<persisted Job UID>"
    startedAt: "2026-09-25T15:00:05Z"
  lastSuccessfulOperation:
    generation: 5
    artifact: "registry.example.com/migrations:v9"
    targetRevision: 9
    resolvedArtifact: "registry.example.com/migrations@sha256:<previous-digest>"
    phase: Succeeded
    jobRef:
      name: "<previous Job name>"
      uid: "<previous Job UID>"
    finishedAt: "2026-09-25T14:00:00Z"
```

`operation` is optional until the controller accepts an attempt. It holds the
current attempt, then retains its terminal outcome until another attempt is
accepted. Its fields are:

- `generation`: the resource generation from which the request was accepted,
  retained for attribution. Compare the captured artifact reference and target
  revision to detect changed migration intent; a generation change alone, such
  as a Job TTL edit, does not request another attempt.
- `artifact` and `targetRevision`: the captured request inputs. The database
  binding is inherited from the resource's immutable spec and namespace.
- `retryAt`: the token handled with this attempt, omitted for an ordinary
  request without a new retry token. It is independent of generation. A new
  spec request and retry token observed together can be handled by one attempt.
- `resolvedArtifact`: the pinned reference, absent until resolution succeeds.
  Once recorded for an attempt, it cannot change during that attempt's recovery.
- `phase`: `Pending`, `Running`, `Succeeded`, `Failed`, or `Unknown`.
  `Unknown` means an attempted execution's outcome cannot yet be established;
  it requires recovery, not an automatic new attempt or assumed success.
- `jobRef`: the Job name chosen and persisted before creation and, once
  observed, its Kubernetes UID. The Job is in the migration set's namespace.
  Retain this reference after completion for attribution even if the Job is
  subsequently removed. No separate operation UUID is needed.
- `startedAt` and `finishedAt`: optional execution timestamps, populated only
  when known. They support diagnostics and retention, not request identity.
- `reason` and `message`: optional details of the attempt's state or failure.
  Keep these with the attempt when overall conditions change to describe a
  newer desired state.

`lastSuccessfulOperation` is a copy of the most recent successful record,
including its request, digest, Job reference, and completion time. It remains
available while a newer attempt is pending, running, or failed. It records the last
successful target; it is not an assertion that a later failed attempt left the
database unchanged. Retaining these two records keeps status bounded. Earlier
Job history and the database migration ledger do not need to be copied into the
resource status.

Persist a new operation and any `lastHandledRetryAt` acknowledgement together
using a conditional status write before launching its Job. Persist the resolved
digest and chosen Job name before Job creation. Repeated create requests use
that exact name; a lost response recovers the same Job. A genuinely new attempt
gets a new Job name, while recovery retains the stored name and checks any
known Job UID and the migration-set owner UID. Never issue work from a status
write that failed or whose outcome has not been established. A retry token
acknowledged without execution
because the desired operation already succeeded changes only the acknowledgement.

The runner decides whether to migrate forward, roll back, or do nothing from
the requested target and database ledger. It needs neither an operation UUID
nor a stored direction. The controller tracks the current Job and its outcome
so it does not mistake a historical successful Job for current completion. A
return to revision 10 after rollback requests a new Job; that Job consults the
ledger to determine the SQL required.

When spec changes during execution, keep the running operation's snapshot and
report that the latest desired state is waiting. Record the old operation's
result before starting the latest request. Do not replace an uncertain attempt
or infer its result from Job absence. Retain terminal failure/success after Job
cleanup; a terminal record is not permission to recreate that Job.

Keep the existing top-level `observedGeneration`, `phase`, and `conditions` as
the summary of the latest desired state. `observedGeneration` means the spec
was evaluated, not that it was successfully applied. `Ready=True` requires a
successful operation matching the current migration inputs and no unresolved
attempt or ownership block. An older matching success must not hide a newer
failure. Consumers check the Ready condition's observed generation and, when
requesting a retry, the handled token. A TTL or pause-field edit may advance
observed generation without changing the operation's recorded generation.

Replace the ambiguous flat `observedArtifact` and `activeJob` fields with
`operation.resolvedArtifact` and `operation.jobRef`. Likewise, expose the last
successful target as `lastSuccessfulOperation.targetRevision` instead of calling
it `currentRevision`. The runner commits individual migration steps, so an
attempt from 8 to 10 can fail after committing 9. Reporting the actual database
revision after partial failure would require a separate ledger observation;
it is not needed for the agreed contract. This is an API presentation choice,
not a new execution policy. Update printer columns and consumers with the API.

### Explicit retries

Follow Flux's changed-annotation-token convention with the operator's own
`db-operator.benjamin-wright.github.com/retryAt` annotation and
`status.lastHandledRetryAt` acknowledgement. Users normally supply a fresh
timestamp, for example:

```yaml
metadata:
  annotations:
    db-operator.benjamin-wright.github.com/retryAt: "2026-09-25T15:00:00Z"
```

Treat the non-empty value as an opaque request token, comparing equality with
the last handled value rather than interpreting its time or ordering clocks.
A different value requests one additional attempt if the current desired
operation has failed. Repeated events with the same handled value do not request
further attempts. Removing the annotation does not request a retry or clear its
acknowledgement. Callers use a fresh value for each intentional retry.

Annotation changes must trigger reconciliation even though the spec generation
does not change. Persist the token's association with the operation and attempt
so a controller restart or lost Job-create/status-write response cannot lose
the request or create duplicate attempts. `lastHandledRetryAt` acknowledges
handling, not successful execution; the operation status reports the outcome.

Pausing delays retry handling, and an active Job finishes before a pending retry
is handled. If the current desired operation has already succeeded, acknowledge
the token without rerunning it. Process the latest desired state; annotations
are not a queue of every intermediate request. A terminally failed attempt
requires another new token or changed desired operation to run again.

### Forward and rollback authorization

Forward migration and rollback use the same authorization and execution path.
A user allowed to update a migration set's target revision may raise or lower
it, including rollback to zero, without a separate approval annotation or
rollback permission. The operator provisions and manages the internal migration
role and its credentials for both directions. Application-user credentials and
grants also remain operator-managed; consumers need no separate rollback user.
The ownership check, execution lock, and migration integrity checks apply in
both directions.

### Mutable artifact references

A tag such as `registry.example.com/migrations:dev` can resolve to different
digests over time even though `spec.artifact` is unchanged. Resolve the tag when
preparing a requested attempt: initially, after a change to the artifact
reference or target revision, or for an explicit retry of a failed operation.
An explicit retry may therefore pick up a republished tag. Persist the resolved
digest with the attempt and pass that pinned reference to its Job.

Routine reconciliation, controller restarts, status updates, unrelated metadata
changes, and Job cleanup reuse the recorded resolution and outcome. They neither
refresh a tag nor request a new attempt. A registry tag changing by itself has
no effect. Pausing/resuming retains any already resolved pending attempt, and a
retry annotation on a successful operation does not refresh the artifact.
Transient resolution or download failures can retry preparation for the same
request; once a digest is recorded, recovery uses that digest. Fetching pinned
content to execute or recover an attempt does not mean refreshing its tag.

The database migration ledger determines which SQL has already been applied.
For every recorded applied migration, the runner requires the migration to be
present with matching apply and rollback hashes. A newer artifact, spec change,
or retry does not authorize changing or replaying those applied migrations:
missing or modified SQL is an integrity failure. If revision 10 is already
applied and its hashes match, asking for revision 10 again performs no migration
SQL. An artifact adding revision 11 can advance to 11 while preserving the
recorded history. Explicit rollback continues to use the agreed target-revision
semantics.

The existing runner already enforces these integrity checks. The controller's
current resolution-on-every-reconcile behavior must change to follow resource
requests as described above; artifact digests must not independently trigger
retries.

## Implementation readiness

The ownership, retry, rollback, and artifact semantics are specified above. The
proposed operation-status shape expresses those decisions without introducing
another execution policy. Field naming and grouping can be refined during
implementation; controller coordination, recovery, and acceptance coverage
remain work tracked in [todo.md](todo.md).

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
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
  defines namespaced Role and RoleBinding permissions.
- [Kubernetes garbage collection](https://kubernetes.io/docs/concepts/architecture/garbage-collection/)
  disallows cross-namespace owner references.
- [cert-manager SelfSigned issuers](https://cert-manager.io/docs/configuration/selfsigned/)
  provision the serving certificate without an external certificate authority.
- [cert-manager CA injection](https://cert-manager.io/docs/concepts/ca-injector/)
  documents the annotation on the webhook configuration and Certificate source.
- [Flux reconcile and retry requests](https://fluxcd.io/flux/components/helm/helmreleases/#resetting-remediation-retries)
  use changed annotation values and last-handled status fields. db-operator
  adopts that request-token pattern with its own retry annotation and semantics.
- [Kubernetes API status conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties)
  describe observed generation and conditions for reporting current observations.
