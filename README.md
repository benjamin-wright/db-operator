# db-operator

A Kubernetes operator that provisions and manages PostgreSQL, Redis, and NATS instances and credentials via CRDs.

## Supported Databases

| Kind | Short name | What it manages |
|------|-----------|-----------------|
| `PostgresDatabase` | `pgdb` | A self-contained PostgreSQL instance (versions 14, 15, 16, 17) |
| `PostgresCredential` | `pgcred` | A PostgreSQL role with configurable table-level privileges |
| `PostgresMigrationSet` | `pgms` | A versioned set of SQL migrations applied to a `PostgresDatabase` from an OCI artifact |
| `RedisDatabase` | `rdb` | A Redis 8 instance |
| `RedisCredential` | — | A Redis ACL user with configurable key patterns and command categories |
| `NatsCluster` | `nats` | A NATS server with optional JetStream persistence |
| `NatsAccount` | — | A NATS account with users, exports, and imports |

All CRDs are in API group `db-operator.benjamin-wright.github.com/v1alpha1`.

## Deploying the Operator

Install cert-manager first and wait for its controller, webhook and CA injector
to be ready. The db-operator chart creates its own issuer and serving certificate
for migration admission. Install a pinned release with Helm:

```sh
DB_OPERATOR_VERSION=v1.2.0
helm upgrade --install db-operator oci://registry-1.docker.io/benwright/db-operator-chart \
  --namespace db-operator \
  --create-namespace \
  --version "$DB_OPERATOR_VERSION" \
  --wait --timeout 5m
kubectl wait --for=condition=Ready certificate/db-operator-webhook \
  --namespace db-operator --timeout=120s
kubectl wait --for=jsonpath='{.webhooks[0].clientConfig.caBundle}' \
  validatingwebhookconfiguration/db-operator-migrations --timeout=120s
```

The chart installs seven CRDs and matching operator, migration-runner and optional
MCP image versions. Apply migration sets only after the certificate, webhook
endpoints and injected CA bundle are ready.

Helm does not upgrade existing CRDs. For an upgrade, apply the selected release's
CRDs before upgrading the deployment, then repeat the readiness checks above:

```sh
helm show crds oci://registry-1.docker.io/benwright/db-operator-chart \
  --version "$DB_OPERATOR_VERSION" | kubectl apply -f -
helm upgrade db-operator oci://registry-1.docker.io/benwright/db-operator-chart \
  --namespace db-operator --version "$DB_OPERATOR_VERSION" \
  --wait --timeout 5m
```

Keep CRDs, migration claims and retained data in place during upgrades. Migration
admission fails closed while its webhook is unavailable.

## Chart Values

| Value | Default | Description |
|-------|---------|-------------|
| `image.repository` | `docker.io/benwright/db-operator` | Operator image repository |
| `image.tag` | `""` (defaults to chart `appVersion`) | Operator image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `migrationImage.repository` | `docker.io/benwright/db-migrations` | Migration job image repository |
| `migrationImage.tag` | `""` (defaults to chart `appVersion`) | Migration job image tag |
| `migrationImage.pullPolicy` | `IfNotPresent` | Migration job image pull policy |
| `instanceName` | `""` | Operator instance name; when set, only CRs carrying a matching `db-operator.benjamin-wright.github.com/operator-instance` label are reconciled |
| `resources.requests.cpu` | `50m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `resources.limits.cpu` | `200m` | CPU limit |
| `resources.limits.memory` | `128Mi` | Memory limit |
| `mcp.enabled` | `false` | Deploy the MCP server alongside the operator |
| `mcp.image.repository` | `docker.io/benwright/db-mcp` | MCP server image repository |
| `mcp.image.tag` | `""` (defaults to chart `appVersion`) | MCP server image tag |
| `mcp.image.pullPolicy` | `IfNotPresent` | MCP server image pull policy |
| `mcp.addr` | `:8080` | Bind address for the MCP server |

Override values with `--set` or a values file:

```sh
helm install db-operator ... \
  --set image.repository=my-registry/db-operator \
  --set image.tag=v1.2.3
```

## Usage Examples

### PostgreSQL

Create a PostgreSQL instance, then create credentials for an application:

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: PostgresDatabase
metadata:
  name: my-postgres
  namespace: default
spec:
  postgresVersion: "16"
  storageSize: 2Gi
```

When a `PostgresDatabase` is created the operator automatically provisions an admin Secret in the same namespace. Given the example above:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-postgres-admin   # always {name}-admin, same namespace as the CR
data:
  PGUSER:     <base64>   # always "postgres"
  PGPASSWORD: <base64>   # auto-generated 24-character random password
```

The secret name is also stored on `PostgresDatabase.status.secretName`. The password is generated once and never rotated automatically — delete the Secret to force regeneration.

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: PostgresCredential
metadata:
  name: my-app-creds
  namespace: default
spec:
  databaseRef: my-postgres   # name of the PostgresDatabase above
  username: myapp
  secretName: myapp-postgres-secret
  permissions:
    - databases:
        - myapp
      tables: # Omit to apply to all tables
        - mytable
      permissions:
        - SELECT
        - INSERT
        - UPDATE
        - DELETE
```

The operator populates `myapp-postgres-secret` with the following keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-postgres-secret
data:
  PGUSER:     <base64>   # the username specified in the CR
  PGPASSWORD: <base64>   # auto-generated 24-character random password
  PGHOST:     <base64>   # in-cluster DNS name, e.g. my-postgres.default.svc.cluster.local
  PGPORT:     <base64>   # always 5432
  PGDATABASE: <base64>   # only present when the credential targets exactly one database
```

Example usage in a Pod:

```yaml
envFrom:
  - secretRef:
      name: myapp-postgres-secret
```

Available permissions: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`, `ALL`.

To restrict a credential to specific tables rather than the whole schema, add a `tables` list to the permissions entry:

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: PostgresCredential
metadata:
  name: readonly-creds
  namespace: default
spec:
  databaseRef: my-postgres
  username: readonly
  secretName: readonly-postgres-secret
  permissions:
    - databases:
        - myapp
      tables:
        - orders
        - products
      permissions:
        - SELECT
```

When `tables` is set, the operator runs `GRANT SELECT ON TABLE orders, products TO readonly` — no other tables are accessible. Note that `ALTER DEFAULT PRIVILEGES` is **not** applied for table-scoped entries; tables created after the credential is provisioned will not be auto-granted. If any listed table does not exist when the credential is reconciled, the credential transitions to `Pending` with reason `WaitingForTable` and retries automatically once the table appears.

Use `PostgresMigrationSet` for schema changes. Application credentials receive
table and sequence privileges; `ALL` does not grant database ownership or schema
creation. The operator provisions a separate migration account with schema and
object permissions for forward migrations and rollback.

For credentials that need blanket access spanning all current and future tables — including those created later by other roles such as a migrations user that is not the database owner — use `clusterRoles` to grant a PostgreSQL predefined role via membership:

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: PostgresCredential
metadata:
  name: inspector-creds
  namespace: default
spec:
  databaseRef: my-postgres
  username: inspector
  secretName: inspector-postgres-secret
  clusterRoles:
    - pg_read_all_data
```

Membership in `pg_read_all_data` is a cluster-wide grant resolved at access time, so it covers tables created later by any role without any per-database `GRANT` plumbing. Allowed values are `pg_read_all_data`, `pg_read_all_stats`, `pg_read_all_settings`, and `pg_monitor`. `clusterRoles` may be set on its own or combined with `permissions`; at least one of the two must be present.

#### Migrations

Migration admission atomically reserves each database target and rejects
conflicting owners. The [migration design](docs/migrations-design.md) describes
the ownership protocol; [capability enforcement and migration ownership](#capability-enforcement-and-migration-ownership)
below documents its operational behavior and recovery requirements.

Schema migrations are applied via `PostgresMigrationSet`. The CR points at a `PostgresDatabase`, names a logical database, and references an OCI artifact containing versioned SQL files. The operator resolves the reference to a digest, journals the operation, runs a fenced `db-migrations` Job using a per-database migration account, and re-reconciles every matching `PostgresCredential` on success so they pick up grants for any newly created tables.

Build and push an artifact with the [`oras` CLI](https://oras.land/):

```sh
oras push registry.example.com/myapp/migrations:v1 \
  --artifact-type application/vnd.db-operator.migrations.v1.tar+gzip \
  ./migrations.tar.gz:application/vnd.db-operator.migrations.v1.tar+gzip
```

The artifact must contain exactly one layer of media type `application/vnd.db-operator.migrations.v1.tar+gzip`. Inside the tarball, each migration is a pair of files named `<id>-<name>-apply.sql` and `<id>-<name>-rollback.sql` where `<id>` is a numeric revision (leading zeros allowed but not significant: `0001` and `1` collide).

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: PostgresMigrationSet
metadata:
  name: myapp-migrations
  namespace: default
spec:
  databaseRef: my-postgres        # name of the PostgresDatabase
  database: myapp                 # logical DB name; created on demand
  artifact: registry.example.com/myapp/migrations:v1
  targetRevision: 5               # numeric revision to converge to
  jobTTL: 1h                      # optional; how long completed Jobs are retained (default 1h)
  paused: false                   # optional; when true, the operator will not launch new Jobs
```

The operator launches a `Job` whose Pod runs `db-migrations` with the recorded
digest and target. Lowering `targetRevision` requests rollback, including to zero.
An unchanged request keeps its pinned artifact and outcome even if its registry
tag changes. Change the artifact reference or target to request new work; use a
fresh retry annotation to retry a failed or unknown request. An in-flight attempt
finishes before the latest request is considered. Missing or modified SQL for an
already-applied migration fails integrity verification.

Inspect `status.operation` for the captured request, pinned digest, Job identity
and outcome, and the `Ready` condition for convergence to the current request.
`status.lastSuccessfulOperation` records historical success; it does not assert
the database state after a later partial failure. The flat `currentRevision`,
`observedArtifact` and `activeJob` fields are compatibility summaries.

### Redis

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: RedisDatabase
metadata:
  name: my-redis
  namespace: default
spec:
  storageSize: 1Gi
```

When a `RedisDatabase` is created the operator automatically provisions an admin Secret in the same namespace. Given the example above:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-redis-admin   # always {name}-admin, same namespace as the CR
data:
  REDIS_USERNAME: <base64>   # always "default"
  REDIS_PASSWORD: <base64>   # auto-generated 24-character random password
```

The secret name is also stored on `RedisDatabase.status.secretName`. The password is generated once and never rotated automatically — delete the Secret to force regeneration.

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: RedisCredential
metadata:
  name: my-app-redis-creds
  namespace: default
spec:
  databaseRef: my-redis
  username: myapp
  secretName: myapp-redis-secret
  keyPatterns:
    - "session:*"
    - "cache:*"
  aclCategories:
    - read
    - write
    - string
    - hash
```

The operator populates `myapp-redis-secret` with the following keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-redis-secret
data:
  REDIS_USERNAME: <base64>   # the username specified in the CR
  REDIS_PASSWORD: <base64>   # auto-generated 24-character random password
  REDIS_HOST:     <base64>   # in-cluster DNS name, e.g. my-redis.default.svc.cluster.local
  REDIS_PORT:     <base64>   # always 6379
```

Example usage in a Pod:

```yaml
envFrom:
  - secretRef:
      name: myapp-redis-secret
```

Available ACL categories: `read`, `write`, `set`, `sortedset`, `list`, `hash`, `string`, `bitmap`, `hyperloglog`, `geo`, `stream`, `pubsub`, `admin`, `fast`, `slow`, `blocking`, `dangerous`, `connection`, `transaction`, `scripting`, `keyspace`, `all`.

### NATS

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: NatsCluster
metadata:
  name: my-nats
  namespace: default
spec:
  natsVersion: "2.10"
  jetStream:            # omit this block to run without JetStream
    storageSize: 1Gi
```

```yaml
apiVersion: db-operator.benjamin-wright.github.com/v1alpha1
kind: NatsAccount
metadata:
  name: my-account
  namespace: default
spec:
  clusterRef: my-nats
  users:
    - username: publisher
      secretName: publisher-nats-secret
      permissions:
        publish:
          allow:
            - "events.>"
        subscribe:
          deny:
            - ">"
    - username: subscriber
      secretName: subscriber-nats-secret
      permissions:
        subscribe:
          allow:
            - "events.>"
```

The operator creates one Secret per user. Each Secret contains the following keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: publisher-nats-secret
data:
  NATS_USERNAME: <base64>   # the username specified in the user entry
  NATS_PASSWORD: <base64>   # auto-generated 24-character random password
  NATS_ACCOUNT:  <base64>   # name of the parent NatsAccount CR
  NATS_HOST:     <base64>   # in-cluster DNS name, e.g. my-nats.default.svc.cluster.local
  NATS_PORT:     <base64>   # always 4222
```

Example usage in a Pod:

```yaml
env:
  - name: NATS_URL
    value: nats://$(NATS_USERNAME):$(NATS_PASSWORD)@$(NATS_HOST):$(NATS_PORT)
envFrom:
  - secretRef:
      name: publisher-nats-secret
```

## Components

| Command | Description | Spec |
|---------|-------------|------|
| `cmd/db-operator` | Kubernetes operator — watches all CRDs listed above | [spec](cmd/db-operator/spec.md) |
| `cmd/db-migrations` | Reusable migration runner — applies versioned SQL schema changes to PostgreSQL | [spec](cmd/db-migrations/spec.md) |

## Contributing

See [docs/contribution.md](docs/contribution.md) for development setup, make targets, and coding standards.

Releases use version tags such as `v1.2.0` from a committed, validated source tree.
The [tag workflow](.github/workflows/tags.yaml) builds `db-operator`,
`db-migrations` and `db-mcp` for Linux amd64 and arm64, publishes their combined
image manifests, then publishes the chart with that tag as its version and
appVersion. Consumers should pin the completed release; the source chart's
placeholder version is overridden during packaging.

## Capability enforcement and migration ownership

PostgreSQL credential policy replacement disables login, terminates existing
sessions, removes role memberships, direct grants and default privileges, then
applies the complete new policy. Login is enabled only after replacement
succeeds. Deletion retains its finalizer until sessions and backend privileges
are revoked. A backend ownership record binds each role to its credential UID;
existing roles require a verified matching credential Secret for import.
Uncached credential UID, generation and Secret-version checks run inside the
backend mutation lock or transaction, preventing delayed reconciles from
restoring deleted access or an old password.
`PolicyEnforced=True` identifies this behavior to clients. PUBLIC table, column,
sequence and routine grants and their creator defaults are revoked in managed
application schemas, so they cannot bypass declared capabilities. This includes
arbitrary execution of SECURITY DEFINER functions.

Redis policy replacement resets passwords, selectors, commands, channels and
key patterns, including wildcard patterns. Ownership records are authenticated
with the trusted admin credential; ordinary key writers cannot forge authority.
Bounded uncached checks establish a unique live credential UID and exact Secret
version, repairing missing, invalid or replayed records. Replacement revokes
access and terminates old sessions before conditionally enabling the new policy;
a failed `WATCH` leaves access disabled. Backend or ownership failures hold the
deletion finalizer. Redis administrative and ACL capabilities remain privileged.

Migration admission requires cert-manager. The chart provisions its own
namespaced issuer, webhook certificate and Service, and a fail-closed validating
webhook. Admission atomically reserves one Lease in the operator namespace for
`(namespace, databaseRef, database)`. Dry runs only validate. Claims have no TTL
or owner reference; all instances managing one target must share a claim
namespace. The operator uses uncached ownership reads for claims and cleanup.

`status.operation` records a request's artifact, target, retry token, pinned
digest, generation, Job name/UID and terminal outcome. `lastSuccessfulOperation`
is historical evidence, not the current schema after a later partial failure.
The old flat status fields remain a derived compatibility projection. Completed
Job retention and unrelated spec changes never replay a request. A changed
nonempty `db-operator.benjamin-wright.github.com/retryAt` token authorizes retry
of a failed or unknown request; retrying a successful request only acknowledges
the token. Tags are resolved once per new request. In-flight work finishes using
its original immutable request before the latest desired request is considered.

A per-database migration account replaces shared migration credentials. The
runner retains one database session for its advisory lock and every migration
transaction. The Job's claim UID and operation name must match an admin-owned
SQL authority row under that lock. A delayed attempt cannot execute after a newer
operation is armed, even when both use the same claim. Claim arming and release
also take a target guard in the maintenance database; a delayed Job cannot regain
authority after release.

Normal migration-set deletion waits for inactive Jobs and Pods, disarms SQL
authority, revokes its migration role and releases its claim before completing.
Retained database contents can then be bound by another migration set immediately.
Terminating updates do not reserve a new claim.

The orphan cleaner rechecks abandoned claims every ten seconds after a one-minute grace
period, verifies resource, Job and Pod absence across resource incarnations,
then disarms SQL authority and deletes the exact Lease UID/resource version.
Unavailable database administration blocks cleanup rather than asserting
revocation. A reservation whose backend was never created also waits for that
backend or administrator-verified recovery; age alone cannot prove SQL safety.
Existing objects may import matching claims after duplicate-target and Job
ownership checks. Legacy Ready with an exact pinned artifact and target
is verified using a read-only ledger Job when the original Job has expired.
Unverifiable or conflicting evidence becomes Unknown and requires explicit
ledger inspection and retry authorization.
