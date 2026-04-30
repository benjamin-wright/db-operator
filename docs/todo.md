# PostgresMigrationSet CRD — remaining phases

Phases 1–3 of the [`PostgresMigrationSet`](../README.md#migrations) work are complete:
API types, internal migrations role/Secret on `PostgresDatabase`, and the
`db-migrations` artifact-fetch mode. The remaining work is operator wiring.

## Phase 4 — PostgresMigrationSet client + builder

- [ ] `internal/operator/controller/postgresmigrationset_client.go`:
  k8s helpers (get/list/patch CR + status, list owned `batch/v1.Job`s by label,
  list `core/v1.Pod`s for failure reason) plus an ORAS resolver wrapping
  `remote.Repository.Resolve` so `spec.artifact` is digest-pinned every reconcile.
- [ ] `internal/operator/controller/postgresmigrationset_builder.go`:
  build the `Job` with `backoffLimit: 0`, env vars from `<pgdb>-migrations-internal`
  Secret + `--artifact <digest-ref>` + `--target <revision>`, OwnerRef to the CR,
  deterministic key label `db-operator.benjamin-wright.github.com/migration-key =
  short(sha256(observedArtifact|targetRevision))`, `generateName` for the actual
  Job name, ServiceAccount = operator's namespace SA.

## Phase 5 — PostgresMigrationSet controller + tests

- [ ] `internal/operator/controller/postgresmigrationset_controller.go`. Reconcile loop:
  resolve artifact → digest (write to `status.observedArtifact`); compute desired
  Job key; list owned Jobs:
  - matching key Running → `phase = Running`, requeue.
  - matching key Succeeded → set `currentRevision = spec.targetRevision`,
    `phase = Ready`; enqueue every `PostgresCredential` in the same namespace
    whose `databaseRef == migrationset.databaseRef` and whose
    `permissions[*].databases` includes `spec.database`; schedule Job deletion
    at `completionTime + jobTTL` (controller-managed, default 1h).
  - matching key Failed → `phase = Failed` with reason from Pod; leave Job for TTL.
  - non-matching key in-flight → `phase = Pending`, reason `WaitingForInFlightJob`,
    do not delete.
  - no matching Job and not paused → create one.
  - `spec.paused` short-circuits before create with reason `Paused`.
- [ ] `internal/operator/controller/postgresmigrationset_controller_test.go`
  (Ginkgo integration, build tag `integration`). Cover: apply → Ready;
  bump `targetRevision` to a lower ID → rollback Job runs; re-pushing same tag
  with new digest re-runs Job; in-flight Job blocks new desired-state Job;
  `paused: true` short-circuits; sibling `PostgresCredential` created before
  migration becomes Ready post-Job (Fix C).

## Phase 6 — cmd/db-operator wiring + RBAC

- [ ] [cmd/db-operator/main.go](../cmd/db-operator/main.go): register
  `&controller.PostgresMigrationSetReconciler{}` and add
  `&v1alpha1.PostgresMigrationSet{}` to the cache `ByObject` map.
- [ ] [internal/operator/controller/suite_test.go](../internal/operator/controller/suite_test.go):
  register the new scheme + reconciler.
- [ ] [charts/db-operator/templates/clusterrole.yaml](../charts/db-operator/templates/clusterrole.yaml):
  add verbs for `batch/jobs` (create/get/list/watch/delete),
  `core/pods` (get/list/watch — for failure-reason surfacing),
  and `postgresmigrationsets` + `/status` + `/finalizers`.

## Phase 7 — Specs + README + todo

- [ ] [cmd/db-operator/spec.md](cmd/db-operator/spec.md): add `PostgresMigrationSet`
  section; remove `databaseOwner` semantics; document the internal migrations role,
  post-migration credential re-reconciliation, `jobTTL` default, `paused`, and
  "wait for in-flight Job before applying new desired state".
- [ ] [cmd/db-migrations/spec.md](cmd/db-migrations/spec.md): document
  `--artifact` mode, expected media type
  `application/vnd.db-operator.migrations.v1.tar+gzip`, retain the advisory-lock
  note, and document that editing an applied SQL file in a new artifact is a
  hard error.
- [ ] [README.md](../README.md): quickstart for
  `oras push --artifact-type application/vnd.db-operator.migrations.v1.tar+gzip`.
- [ ] Trim the legacy `databaseOwner` items below once Phase 5 verified end-to-end.

---

# Migrations Owner Role + Concurrency Safety

Driven by wasm-platform Phase 9.3 (migrations Job lifecycle). The wasm-platform operator
provisions a per-app `migrations` PostgresCredential that must run DDL (`CREATE TABLE`,
etc.) and grant resulting tables to other per-app credentials. Today's `PostgresCredential`
only grants table-level privileges and offers no way to make a role the database owner,
so DDL fails. Separately, the migrations runner has no concurrency guard — concurrent
Job pods can race on the `_migrations` tracking table.

## Remaining Tasks

- [ ] **Runner tests** ([internal/migrations/runner/runner_test.go](internal/migrations/runner/runner_test.go)):
  add a fake-store assertion that `Lock` is called before `EnsureTable` and `Unlock`
  is called on every exit path (success, plan error, apply error).
- [ ] **README + spec**: document `databaseOwner` semantics, the owner-conflict rule,
  and the auto-granted default-privileges behaviour in
  [README.md](README.md) and [cmd/db-operator/spec.md](cmd/db-operator/spec.md). Note the
  advisory lock in [cmd/db-migrations/spec.md](cmd/db-migrations/spec.md) under the
  Interfaces section.

---

# Bug: Permission Reconciliation Brittle to Ordering — cross-repo follow-up

Bug A (owner-transition default-priv replay) and Bug B (idempotent Postgres
reconciliation, `Pending/WaitingForTable` instead of terminal `Failed`) have
been fixed and verified by the integration suite in db-operator. Two
follow-up items remain, both owned outside this repo:

## Remaining Tasks

- [ ] **Fix C in wasm-platform**: in wp-operator's `application_controller`,
  gate creation of non-owner (writer/reader) `PostgresCredential`s behind the
  migrations Job reporting `Succeeded`. Tracked in `wasm-platform/docs/todo.md`
  under the Phase 9 work.
- [ ] Trigger `e2e-tests` via the Tilt MCP server in the wasm-platform
  workspace and confirm it passes. (This is the cross-repo gate — db-operator
  alone can't prove the fix.)
