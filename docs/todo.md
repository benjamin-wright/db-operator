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

- [x] **CRD types**: add `DatabaseOwner bool` to `PostgresCredentialSpec` with the CEL
  rule above. Run `make manifests` to regenerate
  [charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml](charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml).
- [x] **`PostgresManager` interface**: add `EnsureOwner` and `FindOwner` methods to
  [internal/operator/controller/postgrescredential_client.go](internal/operator/controller/postgrescredential_client.go);
  update the fake in tests.
- [x] **`reconcileCredential`**: detect owner conflicts via `FindOwner`; call
  `EnsureOwner` per database when `databaseOwner: true`; in `EnsureUser` add default-
  privileges propagation when an owner is set.
- [x] **`reconcileDelete`**: when an owner credential is deleted, the database keeps the
  PG role until `DropUser` runs. `DROP ROLE` fails if the role still owns objects;
  pre-emptively run `REASSIGN OWNED BY <user> TO <admin>; DROP OWNED BY <user>;` before
  `DROP ROLE`. Document the consequence: deleting an owner credential transfers
  ownership of all tables in the database to the bootstrap admin role.
- [x] **Status condition**: define and surface `OwnerConflict` reason on
  `PostgresCredentialStatus.Conditions`.
- [x] **Tests** ([internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)):
  - owner credential creates the database, takes ownership, can run `CREATE TABLE`;
  - non-owner credential against the same database can `SELECT` from a table the owner
    creates *after* the non-owner credential is provisioned;
  - second `databaseOwner: true` credential against the same database is rejected with
    `OwnerConflict`;
  - deleting an owner credential reassigns ownership without leaving orphaned objects.
- [x] **Migration store**: add `Lock() error` / `Unlock() error` to the
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

---

# Table-Scoped Permission Grants

`DatabasePermissionEntry` exposes an optional `tables` field that restricts which
tables a credential's privileges apply to. When `tables` is omitted the existing
behaviour is preserved (privileges are granted on all tables via `ON ALL TABLES IN
SCHEMA public`). When `tables` is non-empty, privileges are granted only on the named
tables that already exist; no default-privilege rules are set for future tables
because `ALTER DEFAULT PRIVILEGES` cannot be scoped to a specific table list.

## Design

### `EnsureUser` signature change

Update the `PostgresManager` interface and its `postgresManager` implementation:

```go
EnsureUser(host, adminUser, adminPass, dbName, username, password string,
    permissions []v1alpha1.DatabasePermission, tables []string) error
```

### Grant logic inside `EnsureUser`

- **`tables` is nil/empty (current behaviour):**
  ```sql
  GRANT <privs> ON ALL TABLES IN SCHEMA public TO <username>;
  ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT <privs> ON TABLES TO <username>;
  -- plus the owner-scoped ALTER DEFAULT PRIVILEGES if an owner exists
  ```
- **`tables` is non-empty:**
  Each table name must be validated as a non-empty string before being passed to
  `pq.QuoteIdentifier`. The grant becomes:
  ```sql
  GRANT <privs> ON TABLE <t1>, <t2>, … TO <username>;
  ```
  No `ALTER DEFAULT PRIVILEGES` is emitted (there is no PostgreSQL mechanism to
  pre-grant on a specific future table by name). If a named table does not yet
  exist, PostgreSQL will return an error; the controller should surface this as a
  `Failed` status with reason `TableNotFound`.

### Call-site update in `reconcileCredential`

Pass `entry.Tables` as the final argument to every `EnsureUser` call in
[internal/operator/controller/postgrescredential_controller.go](internal/operator/controller/postgrescredential_controller.go).

## Tasks

- [x] **CRD manifest**: run `make generate` to regenerate
  [charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml](charts/db-operator/crds/db-operator.benjamin-wright.github.com_postgrescredentials.yaml)
  (the `tables` field already exists in the Go types but the CRD YAML may be stale).
- [x] **`PostgresManager` interface**: add `tables []string` to the `EnsureUser` signature in
  [internal/operator/controller/postgrescredential_client.go](internal/operator/controller/postgrescredential_client.go).
- [x] **`EnsureUser` implementation**: update `postgresManager.EnsureUser` to branch on
  `len(tables)`:
  - empty → existing `ON ALL TABLES` + `ALTER DEFAULT PRIVILEGES` path (unchanged);
  - non-empty → `GRANT … ON TABLE <quoted-tables…> TO <username>` only; validate each
    table name is non-empty before quoting to guard against blank strings reaching SQL.
- [x] **`reconcileCredential` call site**: pass `entry.Tables` to every `EnsureUser`
  call in
  [internal/operator/controller/postgrescredential_controller.go](internal/operator/controller/postgrescredential_controller.go).
- [x] **Tests**
  ([internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)):
  - credential with `tables: [foo]` can `SELECT` from `foo` but not from a second table
    `bar` in the same database;
  - credential with `tables` omitted retains the existing `ON ALL TABLES` behaviour
    (existing tests continue to pass);
  - credential referencing a table that does not exist transitions to `Failed` with
    reason `TableNotFound`.
- [x] **spec.md**: document the `tables` field semantics, the no-default-privileges
  caveat for table-scoped entries, and the `TableNotFound` failure reason in
  [cmd/db-operator/spec.md](cmd/db-operator/spec.md).

---

# Bug: Default-Privileges Propagation Not Reaching Migrations-Owned Tables

Discovered while debugging the wasm-platform `sql-hello` e2e test (24 Apr 2026).
Symptom: writer/reader credentials authenticate successfully but every statement
returns `permission denied for table greetings`. The migrations Job creates
`greetings`, owned by the `…__migrations` role, but neither the writer nor the
reader credential receives privileges on it.

## Evidence

In a live PostgreSQL session against `wasm_default__sql_hello`:

- `\dt greetings` → owner is `wasm_default__sql_hello__migrations` (correct).
- `pg_default_acl` shows two rows for the `public` schema, **both with
  `granted_by = postgres`**:
  ```
  postgres | public | {…__writer=arwdDxt/postgres, …__reader=r/postgres,
                       …__migrations=arwdDxt/postgres}
  postgres | public | {…__writer=rwU/postgres, …__reader=r/postgres,
                       …__migrations=rwU/postgres}
  ```
- `has_table_privilege('…__writer', 'greetings', 'INSERT')` → `f`.
- `has_table_privilege('…__reader', 'greetings', 'SELECT')` → `f`.

`ALTER DEFAULT PRIVILEGES` only fires for objects created by the role named in
its `FOR ROLE` clause (default: the role running the statement). Because the
existing rows are `granted_by = postgres`, they only auto-grant on tables
`postgres` creates — not tables `…__migrations` creates. Hence the bug.

## Likely root cause (needs verification)

The "default-privileges propagation" tasks under the
[Migrations Owner Role + Concurrency Safety](#migrations-owner-role--concurrency-safety)
section are marked `[x]`, so `EnsureUser` should already issue
`ALTER DEFAULT PRIVILEGES FOR ROLE <owner> …` when an owner exists. Two
candidates for why it didn't fire here:

1. **wp-operator never sets `databaseOwner: true`** on the `…__migrations`
   credential. `FindOwner` then returns the bootstrap `postgres` role, which
   is explicitly excluded from owner-conflict logic — but the propagation code
   may also short-circuit, leaving `FOR ROLE` unset and defaulting to
   `postgres`. Fix would land in wp-operator (out of scope for this repo) but
   the db-operator behaviour when the owner *is* the bootstrap role is worth
   a deliberate decision: should it still propagate `FOR ROLE postgres`, or
   should it require an explicit owner credential?
2. **`EnsureUser` reconciles writer/reader before the migrations credential**
   becomes owner. `FindOwner` at that moment returns `postgres`, propagation
   is set against `postgres`, and is never re-run when the owner later
   changes. Re-reconciling all sibling credentials after `EnsureOwner` would
   fix this.

## Tasks

- [ ] **Reproduce in isolation**: write an integration test in
  [internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)
  that creates an owner credential and a non-owner credential, then has the
  owner role create a new table, and asserts the non-owner can `SELECT` from
  it. Confirm the test fails today.
- [ ] **Inspect `pg_default_acl.defaclrole`** in the existing owner-default-
  privileges tests to assert it equals the owner role, not `postgres`. The
  current tests likely only check `has_table_privilege`, which can pass for
  pre-existing tables granted directly.
- [ ] **Diagnose**: confirm whether the gap is (1) reconcile ordering, (2) the
  bootstrap-owner short-circuit, or (3) a missed call site. Update this
  section with findings before implementing.
- [ ] **Fix** based on the diagnosis. Likely shapes:
  - emit `ALTER DEFAULT PRIVILEGES FOR ROLE <owner>` even when the owner is
    the bootstrap role, *and* re-reconcile sibling credentials when an owner
    transition is detected; or
  - require an explicit owner credential and document that non-owner
    credentials against an unowned database are a configuration error.
- [ ] Trigger `e2e-tests` via the Tilt MCP server in the wasm-platform
  workspace and confirm it passes. (This is the cross-repo gate — db-operator
  alone can't prove the fix.)

---

# DB MCP Server

A new compilable component that exposes a Model Context Protocol server for
read-only inspection of databases managed by db-operator. Lives at
`cmd/db-mcp/` with its own `spec.md`. The server runs in-cluster, watches
db-operator CRDs to discover database clusters, provisions a cluster-wide
read-only credential per cluster on demand, and exposes MCP tools so an LLM
client can list clusters and execute scoped queries.

## Scope

- **Postgres** — first target. Enumerates `PostgresCluster` CRs across all
  watched namespaces, idempotently provisions a cluster-wide read-only role
  (one per cluster), and serves the tools below.
- **Redis** — TBD. Define the equivalent enumeration + read-only access
  story before implementing.
- **NATS** — TBD. Define the equivalent enumeration + observation story
  before implementing.

## Postgres tools

- `pg_list_clusters` — returns all `PostgresCluster` CRs the server can see,
  with their namespace, name, host, and the list of databases each cluster
  hosts (derived from `PostgresCredential` CRs that target the cluster).
- `pg_exec_sql` — executes a SQL statement against a named (cluster, database)
  pair using the read-only credential. Inputs: cluster ref, database name,
  SQL text, optional row limit. Output: column metadata + rows, capped at the
  row limit.

## Read-only credential provisioning

The MCP server is deployed in-cluster and port-forwarded to a developer's
host. Credential lifecycle goes through the operator: for each discovered
`PostgresCluster`, the MCP server creates (and reconciles) a
`PostgresCredential` CR targeting every database on that cluster with
`SELECT`-only permissions. The operator's existing reconciliation loop
handles role creation, grants, and Secret materialisation; the MCP server
just mounts/reads those Secrets to obtain connection details.

This keeps one source of truth for role lifecycle (the operator) and means
no admin credentials are needed by the MCP server itself.

## Catalog visibility

A read-only credential automatically inherits PostgreSQL's default `USAGE` on
`pg_catalog` and `information_schema`, so the MCP server can introspect the
cluster without any extra grants:

- **Roles & users**: `pg_roles`, `pg_user` (password hashes are masked; only
  superusers can read `pg_authid.rolpassword`).
- **Databases / schemas / tables / columns**: `pg_database`, `pg_namespace`,
  `pg_class`, `pg_attribute`, plus the `information_schema.*` views.
- **Grants**: `information_schema.table_privileges`,
  `information_schema.role_table_grants`, `pg_class.relacl`.
- **Constraints / indexes**: `information_schema.table_constraints`,
  `pg_indexes`, etc.

Limits to flag in the spec:

- `pg_stat_activity.query` for *other* sessions is masked unless the role has
  `pg_read_all_stats`. Not granting this by default is the right call — if
  needed later, add it as an explicit toggle.
- A connection is bound to one database; cross-DB queries don't work. To list
  all databases on a cluster the MCP server reads `pg_database` from any one
  connection.

## SQL safety posture

This is a development-only server with intentionally open SQL input, so
transaction-wrapping is not worth the complexity:

- `SET TRANSACTION READ ONLY` is trivially escapable from arbitrary input
  (`COMMIT; …` / `RESET …`), so it provides no real boundary. The PG
  privileges on the read-only role are the actual safety net.
- A server-side **`statement_timeout`** is worth setting (via the
  credential's connection string or `SET statement_timeout` at session
  open) — it is enforced by the server and survives client-side escapes,
  which guards against `pg_sleep`-style connection exhaustion.

## Tasks

- [ ] **Component skeleton**: create `cmd/db-mcp/main.go` and
  `cmd/db-mcp/spec.md` per the project layout in
  [docs/standards.md](standards.md). Wire it into the Helm chart and Tiltfile
  alongside the existing components.
- [ ] **Cluster discovery**: watch `PostgresCluster` (and the credential CRs
  that reference it) using the same controller-runtime setup as
  `cmd/db-operator`. Maintain an in-memory index of cluster → databases.
- [ ] **Read-only credential CRs**: for each discovered `PostgresCluster`,
  reconcile a managed `PostgresCredential` (owner-referenced by the MCP
  Deployment) granting `SELECT` on every database on that cluster. Wait for
  the operator-produced Secret before serving requests against that cluster.
- [ ] **Confirm `SELECT`-only is expressible**: today the smallest grant in
  [pkg/api/v1alpha1](../pkg/api/v1alpha1) is `read`/`write` — verify this
  maps to plain `SELECT` (no `INSERT`/`UPDATE`/`DELETE`). If not, add a
  `readOnly` permission level before the MCP work lands.
- [ ] **MCP server**: pick an MCP Go SDK (research current options — none is
  vendored today) and expose `pg_list_clusters` and `pg_exec_sql`. Set a
  server-side `statement_timeout` on every connection; do **not** wrap input
  in `SET TRANSACTION READ ONLY` (trivially escapable from arbitrary SQL —
  the role's privileges are the real boundary).
- [ ] **Tests**: integration test under `internal/` that spins up a Postgres
  cluster via the existing test harness, exercises `pg_list_clusters` and
  `pg_exec_sql`, and asserts that write statements (`INSERT`, `CREATE TABLE`)
  fail with a permission error.
- [ ] **Redis design**: draft the equivalent enumeration + read-only-tool
  spec for `RedisCluster` CRs. Add tasks under a new sub-section once
  agreed.
- [ ] **NATS design**: draft the equivalent enumeration + observation-tool
  spec for `NatsCluster` CRs. Add tasks under a new sub-section once
  agreed.

