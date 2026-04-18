# Migrations Owner Role + Concurrency Safety

Driven by wasm-platform Phase 9.3 (migrations Job lifecycle). The wasm-platform operator
provisions a per-app `migrations` PostgresCredential that must run DDL (`CREATE TABLE`,
etc.) and grant resulting tables to other per-app credentials. Today's `PostgresCredential`
only grants table-level privileges and offers no way to make a role the database owner,
so DDL fails. Separately, the migrations runner has no concurrency guard — concurrent
Job pods can race on the `_migrations` tracking table.

## Design

### `PostgresCredential.spec.databaseOwner`

Add an optional boolean to `PostgresCredentialSpec`:

```go
// DatabaseOwner, when true, makes this credential the OWNER of every database listed
// in spec.permissions[*].databases. The role is granted ALL privileges on the database
// and on the public schema, enabling DDL operations.
//
// At most one credential per (databaseRef, database) may set databaseOwner: true.
// A second credential setting databaseOwner: true against the same database is rejected
// (status Failed, reason OwnerConflict).
//
// When other credentials are reconciled against an owner-managed database, the operator
// also runs ALTER DEFAULT PRIVILEGES FOR ROLE <owner> so that tables created later by
// the owner are auto-granted to those credentials.
// +optional
DatabaseOwner bool `json:"databaseOwner,omitempty"`
```

CEL-validated invariant: `databaseOwner: true` requires `spec.permissions` non-empty
(an owner must target at least one database).

### `PostgresManager` interface additions

- `EnsureOwner(host, adminUser, adminPass, dbName, username string) error` — runs:
  ```sql
  ALTER DATABASE <dbName> OWNER TO <username>;
  GRANT ALL ON SCHEMA public TO <username>;
  GRANT ALL ON ALL TABLES IN SCHEMA public TO <username>;
  GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO <username>;
  ```
  Idempotent — `ALTER DATABASE … OWNER TO` is a no-op when already owned by that role.
- `FindOwner(host, adminUser, adminPass, dbName string) (string, error)` — returns the
  current PG owner role name (`SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database
  WHERE datname = $1`). Returns empty string if the database does not yet exist.

### Owner conflict detection

In `reconcileCredential`, before calling `EnsureOwner` for any database:

1. Call `FindOwner(dbName)`.
2. If the result is non-empty and not equal to this credential's derived username **and**
   that other owner is itself a `databaseOwner: true` PostgresCredential in the cluster
   (lookup by `spec.databaseRef` + `spec.username`), set status `Failed` with reason
   `OwnerConflict` and message naming the conflicting CR. Do not retry.
3. Otherwise (no current owner, or current owner is the cluster bootstrap role) proceed
   with `EnsureOwner`.

Cluster bootstrap roles (`postgres`) are never treated as conflicting owners — taking
ownership away from `postgres` is the expected first-time behaviour.

### Default-privileges propagation

In `EnsureUser` (called for every credential, owner or not):

- Look up the current owner of `dbName` via `FindOwner`.
- If an owner exists and differs from the user being granted, additionally run:
  ```sql
  ALTER DEFAULT PRIVILEGES FOR ROLE <owner> IN SCHEMA public
    GRANT <privs> ON TABLES TO <username>;
  ALTER DEFAULT PRIVILEGES FOR ROLE <owner> IN SCHEMA public
    GRANT <privs> ON SEQUENCES TO <username>;
  ```
  This ensures that tables and sequences the owner creates *later* are auto-granted to
  this user. Without it, a sequence: "create credential A → create owner B → owner B
  creates table" leaves credential A unable to read the new table.

`ALTER DEFAULT PRIVILEGES FOR ROLE` requires the operator to connect as a role with
membership in the owner role. The admin role (`postgres`) is a superuser and always
satisfies this.

### Migrations runner advisory lock

In `internal/migrations/runner/runner.Run`, before `EnsureTable`:

```go
const lockKey = int64(0x_5f6d6967726174) // hashtext('_migrations'), pinned literal
if _, err := db.Exec("SELECT pg_advisory_lock($1)", lockKey); err != nil { ... }
defer db.Exec("SELECT pg_advisory_unlock($1)", lockKey)
```

Session-scoped lock — released automatically if the pod crashes. Concurrent Job pods
serialise on this lock; the second pod waits, then sees the first's writes and applies
nothing.

This requires extending the `MigrationStore` interface (or the runner's signature) to
expose the underlying `*sql.DB`. Cleanest option: add `Lock() error` and `Unlock() error`
to the store interface so the runner stays decoupled from the driver.

### Backwards compatibility

Both changes are additive:
- `databaseOwner` defaults to `false`; existing credentials behave identically.
- The advisory lock is transparent to single-pod migration Jobs (the only deployment
  pattern in use today).

## Tasks

- [ ] **CRD types**: add `DatabaseOwner bool` to `PostgresCredentialSpec` with the CEL
  rule above. Run `make manifests` to regenerate
  [charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml](charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml).
- [ ] **`PostgresManager` interface**: add `EnsureOwner` and `FindOwner` methods to
  [internal/operator/controller/postgrescredential_client.go](internal/operator/controller/postgrescredential_client.go);
  update the fake in tests.
- [ ] **`reconcileCredential`**: detect owner conflicts via `FindOwner`; call
  `EnsureOwner` per database when `databaseOwner: true`; in `EnsureUser` add default-
  privileges propagation when an owner is set.
- [ ] **`reconcileDelete`**: when an owner credential is deleted, the database keeps the
  PG role until `DropUser` runs. `DROP ROLE` fails if the role still owns objects;
  pre-emptively run `REASSIGN OWNED BY <user> TO <admin>; DROP OWNED BY <user>;` before
  `DROP ROLE`. Document the consequence: deleting an owner credential transfers
  ownership of all tables in the database to the bootstrap admin role.
- [ ] **Status condition**: define and surface `OwnerConflict` reason on
  `PostgresCredentialStatus.Conditions`.
- [ ] **Tests** ([internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)):
  - owner credential creates the database, takes ownership, can run `CREATE TABLE`;
  - non-owner credential against the same database can `SELECT` from a table the owner
    creates *after* the non-owner credential is provisioned;
  - second `databaseOwner: true` credential against the same database is rejected with
    `OwnerConflict`;
  - deleting an owner credential reassigns ownership without leaving orphaned objects.
- [ ] **Migration store**: add `Lock() error` / `Unlock() error` to the
  `MigrationStore` interface in
  [internal/migrations/store/store.go](internal/migrations/store/store.go) backed by
  `pg_advisory_lock`/`pg_advisory_unlock`. Update the runner in
  [internal/migrations/runner/runner.go](internal/migrations/runner/runner.go) to take
  the lock before `EnsureTable` and release it on exit (including the error path).
- [ ] **Runner tests** ([internal/migrations/runner/runner_test.go](internal/migrations/runner/runner_test.go)):
  add a fake-store assertion that `Lock` is called before `EnsureTable` and `Unlock`
  is called on every exit path (success, plan error, apply error).
- [ ] **README + spec**: document `databaseOwner` semantics, the owner-conflict rule,
  and the auto-granted default-privileges behaviour in
  [README.md](README.md) and [cmd/db-operator/spec.md](cmd/db-operator/spec.md). Note the
  advisory lock in [cmd/db-migrations/spec.md](cmd/db-migrations/spec.md) under the
  Interfaces section.
- [ ] **Chart version**: bump the db-operator Helm chart minor version; record the new
  version so wasm-platform can pin to it.

