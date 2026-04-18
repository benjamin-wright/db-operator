# Multi-Database Support — Change Plan

## Overview

Currently each `PostgresDatabase` CR represents both a PostgreSQL **cluster** (StatefulSet + Service + admin Secret) and a **single logical database** (`spec.databaseName`). The goal is to decouple these: a `PostgresDatabase` becomes a PostgreSQL **instance**, and the databases that live inside it are declared per-credential in `PostgresCredential.spec.permissions`.

## 1. CRD Type Changes

### `pkg/api/v1alpha1/postgresdatabase_types.go`

- **Remove** the `DatabaseName` field from `PostgresDatabaseSpec`.
- **Remove** the `+kubebuilder:printcolumn` for `Database` (`.spec.databaseName`).
- The spec keeps only `PostgresVersion` and `StorageSize`.

### `pkg/api/v1alpha1/postgrescredential_types.go`

- **Replace** the flat `Permissions []DatabasePermission` field with a structured list:
  ```go
  type DatabasePermissionEntry struct {
      // Databases is the list of PostgreSQL database names this entry applies to.
      // Each database will be created inside the target instance if it does not already exist.
      // +kubebuilder:validation:MinItems=1
      Databases []string `json:"databases"`

      // Permissions is the set of table-level privileges to grant in those databases.
      // +kubebuilder:validation:MinItems=1
      Permissions []DatabasePermission `json:"permissions"`
  }
  ```
- Change `PostgresCredentialSpec.Permissions` to `Permissions []DatabasePermissionEntry`.
- **Remove** the top-level `+kubebuilder:validation:MinItems=1` from the old flat permissions field.

## 2. PostgresDatabase Builder — `internal/operator/controller/postgresdatabase_builder.go`

- **`desiredAdminSecret`**: Remove the `PGDATABASE` key from the admin Secret's `StringData`. The admin secret holds only `PGUSER` and `PGPASSWORD`.
- **`desiredStatefulSet`**:
  - Remove the `POSTGRES_DB` env var from the container spec (Postgres defaults to a `postgres` database on init).
  - Update readiness and liveness probes to use `-d postgres` instead of `-d pgdb.Spec.DatabaseName`.

## 3. PostgresDatabase Controller — `internal/operator/controller/postgresdatabase_controller.go`

- No structural changes. The controller still creates a StatefulSet, Service, and admin Secret. It just no longer cares about a database name since the instance starts with the default `postgres` database.

## 4. PostgresCredential Controller — `internal/operator/controller/postgrescredential_controller.go`

This is the most significant change.

### `reconcileCredential`
- After resolving the admin credentials and host, iterate over `pgcred.Spec.Permissions` entries.
- For each entry, for each database name in `entry.Databases`:
  1. Call a new `PostgresManager.EnsureDatabase(host, adminUser, adminPass, dbName)` to create the database if it doesn't exist (connecting to the `postgres` maintenance database).
  2. Call `PostgresManager.EnsureUser(host, adminUser, adminPass, dbName, username, password, entry.Permissions)` — same as today, but once per database.
- The credential Secret currently writes a single `PGDATABASE`. Two options:
  - **Option A**: Write the first database as `PGDATABASE` (simplest migration path; works for the common single-database case).
  - **Option B**: Drop `PGDATABASE` from the secret entirely and let the application choose. This is a breaking change for existing consumers.
  - **Recommended**: Option A — use the first database from the first permissions entry. Document that `PGDATABASE` reflects the first referenced database.

### `reconcileDelete`
- On deletion, iterate over all `entry.Databases` and call `DropUser` for each database to revoke privileges. The databases themselves are not dropped (they may be shared by other credentials).

## 5. PostgresManager Interface — `internal/operator/controller/postgrescredential_client.go`

- **Add** `EnsureDatabase(host, adminUser, adminPass, dbName string) error` to the `PostgresManager` interface.
  - Implementation: connect to the `postgres` database, run `SELECT 1 FROM pg_database WHERE datname = $1`, and if not found, execute `CREATE DATABASE <quoted_identifier>`.
- **`EnsureUser`** signature is unchanged — it already takes a `dbName` parameter. It will now be called multiple times (once per database) by the credential controller.
- **`DropUser`**: Consider whether to revoke per-database or keep the current behaviour. The current implementation revokes on `ALL TABLES IN SCHEMA public` for the given `dbName`, which is already per-database. It will need to be called once per database.

## 6. Generated CRD Manifests (Helm chart)

After changing the Go types and kubebuilder markers, regenerate the CRD YAMLs:
- `charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgresdatabases.yaml` — `databaseName` property and its `required` entry are removed.
- `charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml` — `permissions` changes from a flat string array to an array of objects with `databases` and `permissions`.

Run `make manifests` (or the project's equivalent) to regenerate.

## 7. Tests

### `internal/operator/controller/postgresdatabase_controller_test.go`
- Remove `DatabaseName` from all `PostgresDatabaseSpec` literals.
- Drop assertions that check admin Secret contains `PGDATABASE`.

### `internal/operator/controller/postgrescredential_controller_test.go`
- Update `PostgresCredentialSpec` literals to use the new `Permissions` structure:
  ```go
  Permissions: []v1alpha1.DatabasePermissionEntry{
      {
          Databases:   []string{"mydb"},
          Permissions: []v1alpha1.DatabasePermission{v1alpha1.PermissionSelect, v1alpha1.PermissionInsert},
      },
  },
  ```
- Assert that `PGDATABASE` in the credential Secret matches the first database name from permissions.
- Add a test for multi-database credentials to verify the user is created in each referenced database.

### `internal/test_utils/utils.go`
- Remove `DatabaseName` from the `NewDatabase` helper's `PostgresDatabaseSpec`.

## 8. Documentation

### `README.md`
- Update the PostgresDatabase example to remove `databaseName`.
- Update the PostgresCredential example to show the new nested permissions structure.
- Update the Secret key table to clarify that `PGDATABASE` comes from the first permissions entry.

### `cmd/db-operator/spec.md`
- Update the `PostgresDatabase` CRD description to remove mention of `databaseName`.
- Update the `PostgresCredential` description to explain per-database permissions and on-demand database creation.

### `test.yaml`
- Remove `databaseName` and update to match the new spec.

## 9. Migration / Backwards Compatibility

This is a **breaking CRD change**. Existing `PostgresDatabase` CRs with `databaseName` will fail validation after the CRD update. A migration path would be:

1. Users update their `PostgresDatabase` CRs to remove `databaseName`.
2. Users update their `PostgresCredential` CRs to include the database name(s) in the new `permissions` structure.
3. The operator is upgraded with the new CRDs.

No automatic data migration is needed — existing StatefulSets and databases on disk are unaffected. The Postgres instance still contains whatever databases were previously created; the credential controller will simply find them already present when it calls `EnsureDatabase`.

## Summary of Files Touched

| File | Change |
|------|--------|
| `pkg/api/v1alpha1/postgresdatabase_types.go` | Remove `DatabaseName` field and printer column |
| `pkg/api/v1alpha1/postgrescredential_types.go` | Add `DatabasePermissionEntry` type; restructure `Permissions` |
| `internal/operator/controller/postgresdatabase_builder.go` | Remove `PGDATABASE` from admin secret; remove `POSTGRES_DB` env; update probes |
| `internal/operator/controller/postgrescredential_controller.go` | Loop over permission entries; call `EnsureDatabase` + `EnsureUser` per DB; update secret construction |
| `internal/operator/controller/postgrescredential_client.go` | Add `EnsureDatabase` to `PostgresManager` interface and implementation |
| `internal/operator/controller/postgresdatabase_controller_test.go` | Remove `DatabaseName` from test fixtures |
| `internal/operator/controller/postgrescredential_controller_test.go` | Update to new permissions structure; add multi-DB test |
| `internal/test_utils/utils.go` | Remove `DatabaseName` from `NewDatabase` helper |
| `charts/db-operator/crds/*.yaml` | Regenerated via `make manifests` |
| `README.md` | Update examples and secret key docs |
| `cmd/db-operator/spec.md` | Update CRD descriptions |
| `test.yaml` | Remove `databaseName` |

---

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

