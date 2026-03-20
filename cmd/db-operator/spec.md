# DB Operator Specification

## Purpose
A Kubernetes operator that provisions and manages self-contained PostgreSQL, Redis, and NATS instances via CRDs.

## Scope
- `PostgresDatabase` CRD — declares a PostgreSQL instance (version 14–17) with a database name and storage size; the operator provisions a StatefulSet, headless Service, and admin Secret for each instance
- `PostgresCredential` CRD — declares a PostgreSQL user against a referenced `PostgresDatabase`; the operator generates a random password, creates the user with the specified permissions, and writes credentials to a named Kubernetes Secret in the same namespace
  - Supported permissions: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`, `ALL`
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

## Interfaces
- `games-hub.io/v1alpha1/PostgresDatabase` — namespaced CRD; consumed by application deployments to request a PostgreSQL instance
- `games-hub.io/v1alpha1/PostgresCredential` — namespaced CRD; consumed by application deployments to request a database user and credentials Secret
- `games-hub.io/v1alpha1/RedisDatabase` — namespaced CRD; consumed by application deployments to request a Redis instance
- `games-hub.io/v1alpha1/RedisCredential` — namespaced CRD; consumed by application deployments to request a Redis ACL user and credentials Secret
- `games-hub.io/v1alpha1/NatsCluster` — namespaced CRD; consumed by application deployments to request a NATS server instance
- `games-hub.io/v1alpha1/NatsAccount` — namespaced CRD; consumed by application deployments to declare a NATS account (with users, exports, and imports) on a cluster
- Kubernetes API server — the operator reads and writes StatefulSets, Deployments, Services, ConfigMaps, and Secrets as owned sub-resources of each CRD

