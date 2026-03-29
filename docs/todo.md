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
