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

# Bug: Permission Reconciliation Brittle to Ordering

Discovered while debugging the wasm-platform `sql-hello` e2e test (24 Apr 2026).
Symptom: writer/reader credentials authenticate successfully but every statement
returns `permission denied for table greetings`. The migrations Job creates
`greetings`, owned by the `…__migrations` role, but neither the writer nor the
reader credential receives privileges on it.

## Evidence

In a live PostgreSQL session against `wasm_default__sql_hello`:

- `greetings` is owned by `wasm_default__sql_hello__migrations` (correct).
- `pg_default_acl` only contains rows with `defaclrole = postgres`. There is
  no `defaclrole = …__migrations` entry, so nothing fires when the migrations
  role creates a table.
- `has_table_privilege('…__writer', 'greetings', 'INSERT')` → `f`.
- `has_table_privilege('…__reader', 'greetings', 'SELECT')` → `f`.
- The Application's writer/reader credentials use `tables: [greetings]`, so
  the table-scoped GRANT branch in `EnsureUser` is the one that should grant
  access (no default-privs apply to a table-scoped credential).

## Diagnosis (confirmed)

Two independent bugs combine:

**Bug A — owner transition does not retroactively rewrite default-priv entries.**
When writer/reader were first reconciled, `FindOwner(dbName)` returned `postgres`
(the migrations credential had not yet taken ownership), so the operator emitted
`ALTER DEFAULT PRIVILEGES FOR ROLE postgres …`. When the migrations credential
later became owner via `EnsureOwner`, nothing went back and replayed those
entries against the new owner. Tables created by the migrations role
subsequently inherit nothing.

**Bug B — the GRANT block is gated on `!secretFound`, so it runs at most once.**
In [postgrescredential_controller.go](internal/operator/controller/postgrescredential_controller.go#L134)
the entire `EnsureDatabase` / `EnsureUser` / `EnsureOwner` /
`EnsureRoleMemberships` flow lives inside `if !secretFound`. Once the credential
Secret exists, no Postgres-side reconciliation ever re-runs. For the sql-hello
case writer/reader were created before the migrations Job ran, so the
table-scoped `GRANT … ON TABLE greetings` failed with `42P01 undefined_table`,
the credential went `Failed/TableNotFound`, and on subsequent reconciles the
GRANT was never re-attempted because the secret was now present.

For sql-hello specifically, Bug B is the primary cause — even with Bug A
resolved, table-scoped grants would still need to be retryable.

## Design

### Fix A — replay default-priv entries on owner transition (db-operator)

In `EnsureOwner`, after `ALTER DATABASE … OWNER TO <new>`, detect whether the
owner actually changed. If it did, copy every default-priv entry currently
attached to the previous owner so that future objects created by the new owner
inherit the same grants:

```sql
SELECT grantee::regrole::text, privilege_type, object_type
FROM pg_catalog.pg_default_acl dacl,
     LATERAL pg_catalog.aclexplode(dacl.defaclacl)
WHERE dacl.defaclrole = <previous_owner>::regrole
  AND dacl.defaclnamespace = 'public'::regnamespace;
```

For each `(grantee, privilege_type, object_type)` row, emit:

```sql
ALTER DEFAULT PRIVILEGES FOR ROLE <new_owner> IN SCHEMA public
  GRANT <privilege_type> ON <object_type> TO <grantee>;
```

`object_type` is one of `r` (TABLES), `S` (SEQUENCES), `f` (FUNCTIONS), `T`
(TYPES), `n` (SCHEMAS) — restrict to TABLES + SEQUENCES, the only kinds
`EnsureUser` itself emits.

The old `defaclrole = <previous_owner>` rows can stay; the previous owner will
not create new objects, so the stale entries are inert. Leaving them avoids
having to compute the diff.

**Surfaced subtlety:** the owner credential's reconcile is what corrects
sibling credentials' grants. Their own status will not reflect the corrective
action — they will silently start working on their next reconcile (they need
no further action).

### Fix B — make Postgres reconciliation idempotent and rely on rate-limited retries (db-operator)

Split the `if !secretFound` block in `reconcileCredential`:

- **Secret materialisation** stays one-shot. If the Secret does not exist:
  generate a fresh password, then continue. If it does exist, read
  `PGPASSWORD` from it and continue. This preserves the invariant that the
  password is stable for the credential's lifetime.
- **Postgres reconciliation** runs every reconcile: `EnsureDatabase`,
  `EnsureUser` (or `EnsureUserExists`), `EnsureOwner`, `EnsureRoleMemberships`.
  All of these are already idempotent.
- After the Postgres calls succeed, create the Secret if it did not previously
  exist.

On any Postgres-side failure, return `(ctrl.Result{}, err)`.
controller-runtime's default workqueue rate limiter applies item-scoped
exponential backoff (5ms → 1000s, capped) automatically, so transient errors
like `42P01 undefined_table` resolve themselves once the missing object
appears. **Do not** set `Status.Phase = Failed` for `TableNotFound` — surface
it as `Pending/WaitingForTable` so the UI reflects that the controller is
still working on it.

### Fix C — wp-operator orders migrations Job before non-owner credentials (wasm-platform)

Belt-and-braces: in wp-operator's `application_controller`, do not create the
writer/reader (non-owner) `PostgresCredential`s until the migrations Job has
reported `Succeeded`. This eliminates the race that triggered Bug B in
production and reduces the load on the rate-limited retry path.

This task is owned by the wasm-platform repo; tracked here for visibility
only.

## Tasks

- [ ] **Reproduce Bug A in isolation**
  ([internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)):
  create a non-owner credential first, then an owner credential, have the
  owner role `CREATE TABLE` afterwards, and assert the non-owner can
  `SELECT` from it. Inspect `pg_default_acl.defaclrole` and assert it equals
  the new owner. Confirm the test fails today.
- [ ] **Reproduce Bug B in isolation**
  ([internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)):
  create a credential with `tables: [foo]` against a database where `foo`
  does not exist, observe the `Pending/WaitingForTable` (or current `Failed`)
  status, then `CREATE TABLE foo` and assert the credential transitions to
  `Ready` with the GRANT applied. Confirm the test fails today.
- [ ] **Fix A — replay default privs on owner transition** in
  [internal/operator/controller/postgrescredential_client.go](internal/operator/controller/postgrescredential_client.go).
  Detect previous owner inside `EnsureOwner`; if it differs from the new
  owner, query `pg_default_acl` + `aclexplode` for entries on the previous
  owner and replay each TABLES/SEQUENCES grant under the new owner.
- [ ] **Fix B — idempotent Postgres reconciliation** in
  [internal/operator/controller/postgrescredential_controller.go](internal/operator/controller/postgrescredential_controller.go).
  Restructure `reconcileCredential`: read existing Secret to recover password
  if present; always run the Postgres `Ensure*` block; create the Secret
  after success when absent. Replace the `Failed/TableNotFound` terminal
  state with `Pending/WaitingForTable` and let controller-runtime's default
  rate limiter handle backoff.
- [ ] **Update existing tests**: the `Failed/TableNotFound` test in
  [internal/operator/controller/postgrescredential_controller_test.go](internal/operator/controller/postgrescredential_controller_test.go)
  must be updated to expect `Pending/WaitingForTable` followed by
  `Ready` once the table is created.
- [ ] **spec.md**: in [cmd/db-operator/spec.md](cmd/db-operator/spec.md),
  document (a) the `WaitingForTable` reason and that it is non-terminal,
  (b) that an owner transition replays default-priv entries from the
  previous owner, and (c) that sibling credentials become functional on
  their next reconcile after such a transition without their own status
  changing.
- [ ] **Cross-repo: Fix C in wasm-platform** — track in `wasm-platform/docs/todo.md`
  under the Phase 9 work. Have wp-operator gate non-owner
  `PostgresCredential` creation behind migrations Job success.
- [ ] Trigger `e2e-tests` via the Tilt MCP server in the wasm-platform
  workspace and confirm it passes. (This is the cross-repo gate — db-operator
  alone can't prove the fix.)
