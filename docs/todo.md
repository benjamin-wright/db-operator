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
