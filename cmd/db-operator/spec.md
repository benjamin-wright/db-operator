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
  - `PGDATABASE` in the credential Secret reflects the first database from the first permissions entry
  - `spec.databaseOwner: true` makes the credential's role the OWNER of every database listed in `spec.permissions[*].databases`; the role is granted ALL privileges on the database and its public schema, enabling DDL operations
  - At most one credential per `(databaseRef, database)` may set `databaseOwner: true`; a second credential with `databaseOwner: true` targeting the same database transitions to `Failed` with reason `OwnerConflict`
  - When a non-owner credential is reconciled against a database that has an owner, the operator additionally sets `ALTER DEFAULT PRIVILEGES FOR ROLE <owner>` so tables and sequences created later by the owner are auto-granted to that credential
  - When a credential takes ownership of a database (an owner transition), every public-schema default-privilege entry attached to the previous owner is replayed under the new owner so sibling credentials provisioned before the transition continue to receive grants on objects created by the new owner; sibling credentials' status does not change as a result
  - `spec.databaseOwner: true` requires `spec.permissions` to be non-empty (CEL-validated)
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
  - `exports` — list of subjects (streams or services) this account exposes to other accounts; a `tokenRequired: true` export is private and requires an activation token
  - `imports` — list of subjects (streams or services) this account brings in from another account (referenced by its `NatsAccount` CR name); an optional `localSubject` remaps the imported subject in the local account namespace
- Status conditions and a phase field (`Pending`, `Ready`, `Failed`) are maintained on all six CRDs
- Multiple operator instances can coexist in the same cluster; instance-scoped filtering prevents collisions in test environments
  - When `--instance-name` is empty (the default), the operator processes CRs without the `db-operator.benjamin-wright.github.com/operator-instance` label and ignores labeled CRs
  - When `--instance-name` is set, the operator processes only CRs carrying a matching `db-operator.benjamin-wright.github.com/operator-instance` label and ignores unlabeled CRs
  - The value `"default"` is reserved and must not be used as an explicit instance name; the operator rejects this value at startup
  - All owned sub-resources carry the `db-operator.benjamin-wright.github.com/operator-instance` label matching their parent CR (or no label if the parent CR has none)
  - Leader election lock ID incorporates the instance name (or `"default"` when empty) to prevent conflicts between instances
  - Instance name is configured via `--instance-name` flag (default: empty)
  - The standalone local deployment (for integration testing) runs as a separate instance from the platform-wide deployment, without replacing or disabling it
- `PostgresMigrationSet` CRD — declares a versioned set of SQL migrations to apply to a named logical database within a referenced `PostgresDatabase`
  - `spec.artifact` — OCI reference (tag or digest) to the migration artifact; the operator resolves it to a digest on every reconcile and stores the pinned reference in `status.observedArtifact`
  - `spec.database` — logical database name; created on demand by the internal migrations role if it does not already exist
  - `spec.targetRevision` — numeric revision to converge to; lowering the value triggers a rollback Job
  - `spec.jobTTL` — how long a completed Job (Succeeded or Failed) is retained before the operator deletes it; defaults to `1h`
  - `spec.paused` — when `true`, the operator sets `phase = Pending` with reason `Paused` and will not create new Jobs; any in-flight Job runs to completion
  - The operator uses a dedicated internal migrations role (`__dbop_migrations`) provisioned per `PostgresDatabase`; this role is made the owner of the target database so it can run DDL. The role and its credentials are stored in a Secret named `<pgdb>-migrations-internal` in the same namespace
  - Each reconcile computes a deterministic Job key (`sha256(observedArtifact|targetRevision)`); only one Job per key is ever created
  - If a Job with a different key is still in-flight (not yet Succeeded or Failed), the controller sets `phase = Pending` with reason `WaitingForInFlightJob` and requeues; it does not delete the running Job
  - When the matching Job succeeds, the controller sets `status.currentRevision = spec.targetRevision`, transitions to `phase = Ready`, and enqueues every `PostgresCredential` in the same namespace whose `databaseRef` and `permissions[*].databases` overlap with the migration set — ensuring credentials pick up grants for newly created tables
  - When the matching Job fails, the controller sets `phase = Failed` and surfaces the failure reason from the Job's Pod; the Job is left until its TTL elapses
  - Completed Jobs are deleted by the operator after `jobTTL` from completion time; this is controller-managed and does not rely on Kubernetes `ttlSecondsAfterFinished`
  - Status fields: `phase` (`Pending`/`Running`/`Ready`/`Failed`), `currentRevision`, `observedArtifact`, conditions

## Interfaces
- `games-hub.io/v1alpha1/PostgresDatabase` — namespaced CRD; consumed by application deployments to request a PostgreSQL instance
- `games-hub.io/v1alpha1/PostgresCredential` — namespaced CRD; consumed by application deployments to request a database user and credentials Secret
- `games-hub.io/v1alpha1/RedisDatabase` — namespaced CRD; consumed by application deployments to request a Redis instance
- `games-hub.io/v1alpha1/RedisCredential` — namespaced CRD; consumed by application deployments to request a Redis ACL user and credentials Secret
- `games-hub.io/v1alpha1/NatsCluster` — namespaced CRD; consumed by application deployments to request a NATS server instance
- `games-hub.io/v1alpha1/NatsAccount` — namespaced CRD; consumed by application deployments to declare a NATS account (with users, exports, and imports) on a cluster
- `games-hub.io/v1alpha1/PostgresMigrationSet` — namespaced CRD; consumed by application deployments to declare a versioned SQL migration set to run against a `PostgresDatabase`
- Kubernetes API server — the operator reads and writes StatefulSets, Deployments, Services, ConfigMaps, Secrets, and `batch/v1` Jobs as owned sub-resources of each CRD; it also reads Pods (read-only) to surface Job failure reasons

