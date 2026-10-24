# DB Operator Specification

## Purpose
A Kubernetes operator that provisions and manages self-contained PostgreSQL, Redis, and NATS instances via CRDs.

## Scope
- `PostgresDatabase` CRD — declares a PostgreSQL instance (version 14–17) with a storage size; the operator provisions a StatefulSet, headless Service, and admin Secret for each instance
- `PostgresCredential` CRD — declares a PostgreSQL user against a referenced `PostgresDatabase`; the operator generates a random password, creates the user with the specified per-database permissions, and writes credentials to a named Kubernetes Secret in the same namespace
  - Each permissions entry specifies one or more logical database names and the table-level privileges to grant in those databases; each database is created on demand if it does not already exist
  - Supported permissions: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`, `ALL`
  - An optional `tables` list on a permissions entry restricts the grant to only the named tables; if `tables` is omitted or empty, privileges are granted on all tables via `GRANT … ON ALL TABLES IN SCHEMA public`
    - When `tables` is set, only tables that already exist at reconcile time are granted; `ALTER DEFAULT PRIVILEGES` is **not** set because PostgreSQL has no mechanism to pre-grant future tables by name
    - If any named table does not exist in the database at reconcile time, the credential transitions to `Pending` with reason `WaitingForTable` and is retried with workqueue rate-limited backoff; once the table appears the credential becomes `Ready` automatically
  - `PGDATABASE` is populated when the credential targets exactly one logical database; otherwise consumers select their database explicitly
  - Application grants cover tables and associated sequences, including sequence usage needed for inserts. `ALL` grants table privileges without database ownership or schema creation
  - Changed policy disables login and terminates existing sessions before replacing memberships, grants and defaults. Failed replacement remains disabled; deletion waits for verified backend revocation
  - Role ownership is bound to the credential UID. Existing roles require a matching owned Secret for import; uncached UID, generation and Secret checks fence delayed backend mutations
  - `spec.clusterRoles` grants cluster-wide PostgreSQL predefined role memberships (`GRANT <role> TO <username>`); membership is the correct mechanism when a credential needs blanket access that survives later DDL by other roles, because predefined roles are evaluated at access time and apply to all current and future objects without `ALTER DEFAULT PRIVILEGES`
    - Allowed values: `pg_read_all_data`, `pg_read_all_stats`, `pg_read_all_settings`, `pg_monitor` — chosen to avoid superuser-equivalent roles such as `pg_write_all_data`, `pg_read_server_files`, and `pg_execute_server_program`
    - May be combined with `spec.permissions`, or used alone (in which case the role is created but no per-database GRANTs are issued); CEL validation requires at least one of `permissions` or `clusterRoles` to be set
- `RedisDatabase` CRD — declares a Redis 8 instance with a storage size; the operator provisions a StatefulSet, headless Service, and admin Secret for each instance
  - Admin Secret keys: `username` (always `"default"`), `password`
- `RedisCredential` CRD — declares a Redis ACL user against a referenced `RedisDatabase`; the operator generates a random password, creates the ACL user, and writes credentials to a named Kubernetes Secret in the same namespace
  - Configurable: key patterns (`keyPatterns`), ACL categories (`aclCategories`), individual commands (`commands`)
  - Supported ACL categories: `read`, `write`, `set`, `sortedset`, `list`, `hash`, `string`, `bitmap`, `hyperloglog`, `geo`, `stream`, `pubsub`, `admin`, `fast`, `slow`, `blocking`, `dangerous`, `connection`, `transaction`, `scripting`, `keyspace`, `all`
  - Credential Secret keys: `REDIS_USERNAME`, `REDIS_PASSWORD`, `REDIS_HOST`, `REDIS_PORT`
- `NatsCluster` CRD — declares a single NATS server instance with an optional JetStream persistence configuration; the operator provisions a Deployment, Service, ConfigMap, and optional PersistentVolume for each instance
  - When `jetStream` is set, JetStream is enabled and a PersistentVolume of the specified `storageSize` is provisioned
  - When `jetStream` is omitted, JetStream is disabled and no PersistentVolume is created
- `NatsAccount` CRD — declares one NATS account within a referenced `NatsCluster`; multiple accounts on a single cluster are created by deploying multiple `NatsAccount` CRs
  - Each account is identified by the CR's `metadata.name`, which becomes the NATS account name in the server configuration
  - `users` — list of NATS users; the operator generates a password for each user and writes credentials to the named Kubernetes Secret in the same namespace
  - Updating a referenced Secret's `NATS_PASSWORD` rotates broker authentication. Deleting an operator-owned credential Secret regenerates it with a new password. After convergence, fresh connections accept the new password and reject the old one.
  - `exports` — list of subjects (streams or services) this account exposes to other accounts; a `tokenRequired: true` export is private and requires an activation token
  - `imports` — list of subjects (streams or services) this account brings in from another account (referenced by its `NatsAccount` CR name); an optional `localSubject` remaps the imported subject in the local account namespace
- Status conditions and a phase field are maintained on all seven CRDs; migration sets additionally distinguish running execution
- Multiple operator instances can coexist in the same cluster; instance-scoped filtering prevents collisions in test environments
  - When `--instance-name` is empty (the default), the operator processes CRs without the `db-operator.benjamin-wright.github.com/operator-instance` label and ignores labeled CRs
  - When `--instance-name` is set, the operator processes only CRs carrying a matching `db-operator.benjamin-wright.github.com/operator-instance` label and ignores unlabeled CRs
  - The value `"default"` is reserved and must not be used as an explicit instance name; the operator rejects this value at startup
  - All owned sub-resources carry the `db-operator.benjamin-wright.github.com/operator-instance` label matching their parent CR (or no label if the parent CR has none)
  - Leader election lock ID incorporates the instance name (or `"default"` when empty) to prevent conflicts between instances
  - Instance name is configured via `--instance-name` flag (default: empty)
  - Standalone integration development uses its own cluster and an instance named `test`; ordinary deployment uses the default unlabeled instance
- `PostgresMigrationSet` CRD declares migrations for one immutable `(namespace, databaseRef, database)` target. Fail-closed admission reserves its target with an operator-namespace Lease; ordinary objects and Jobs must pass ownership checks when imported.
  - `spec.artifact` is resolved once for each new artifact/target request or authorized failed-request retry. `spec.targetRevision` selects forward or rollback convergence.
  - `status.operation` durably snapshots the request, pinned digest, retry token, generation, Job name/UID, and outcome before Job creation. Completed Job expiry does not discard this journal or replay SQL.
  - Changed nonempty `retryAt` authorizes retry of Failed or Unknown requests; an already successful request merely acknowledges the token. Pausing stops new attempts, while in-flight work completes against its original snapshot.
  - Each target has a migration role and each migration set owns its credential Secret. Bootstrap transfers application object ownership to this role, allowing both forward migration and rollback.
  - The runner holds one dedicated SQL session for its advisory lock and every migration transaction, and verifies its claim UID and operation name against an admin-owned authority row under that lock.
  - `Ready` requires exact successful execution inputs. Legacy Ready whose Job expired may be recovered by read-only ledger/hash verification. Unverifiable evidence is Unknown, never automatic replay.
  - Orphan cleanup verifies absence of the resource and active Jobs/Pods across incarnations, disarms SQL authority under the same lock, then deletes the exact claim UID/resource version. Unavailable backend administration holds cleanup.
  - Normal deletion waits for inactive Jobs/Pods across incarnations, disarms SQL authority and revokes its migration role before releasing its claim and completing deletion. A successor can immediately claim the retained database; terminating updates do not reserve a new claim.
  - `jobTTL` defaults to one hour and controls completed Job retention only. `lastSuccessfulOperation` is historical proof and does not assert the schema after a subsequent partial failure.

## Interfaces
- `db-operator.benjamin-wright.github.com/v1alpha1/PostgresDatabase` — namespaced CRD; consumed by application deployments to request a PostgreSQL instance
- `db-operator.benjamin-wright.github.com/v1alpha1/PostgresCredential` — namespaced CRD; consumed by application deployments to request a database user and credentials Secret
- `db-operator.benjamin-wright.github.com/v1alpha1/RedisDatabase` — namespaced CRD; consumed by application deployments to request a Redis instance
- `db-operator.benjamin-wright.github.com/v1alpha1/RedisCredential` — namespaced CRD; consumed by application deployments to request a Redis ACL user and credentials Secret
- `db-operator.benjamin-wright.github.com/v1alpha1/NatsCluster` — namespaced CRD; consumed by application deployments to request a NATS server instance
- `db-operator.benjamin-wright.github.com/v1alpha1/NatsAccount` — namespaced CRD; consumed by application deployments to declare a NATS account (with users, exports, and imports) on a cluster
- `db-operator.benjamin-wright.github.com/v1alpha1/PostgresMigrationSet` — namespaced CRD; consumed by application deployments to declare a versioned SQL migration set to run against a `PostgresDatabase`
- Kubernetes API server — the operator reads and writes StatefulSets, Deployments, Services, ConfigMaps, Secrets, and `batch/v1` Jobs as owned sub-resources of each CRD; it also reads Pods (read-only) to surface Job failure reasons
