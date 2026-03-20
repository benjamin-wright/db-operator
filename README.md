# db-operator

A Kubernetes operator that provisions and manages PostgreSQL, Redis, and NATS instances and credentials via CRDs.

## Supported Databases

| Kind | Short name | What it manages |
|------|-----------|-----------------|
| `PostgresDatabase` | `pgdb` | A self-contained PostgreSQL instance (versions 14, 15, 16, 17) |
| `PostgresCredential` | `pgcred` | A PostgreSQL role with configurable table-level privileges |
| `RedisDatabase` | `rdb` | A Redis 8 instance |
| `RedisCredential` | — | A Redis ACL user with configurable key patterns and command categories |
| `NatsCluster` | `nats` | A NATS server with optional JetStream persistence |
| `NatsAccount` | — | A NATS account with users, exports, and imports |

All CRDs are in API group `db-operator.benjamin-wright.github.com/v1alpha1`.

## Deploying the Operator

Install with Helm. The chart ships the CRDs and the operator deployment together.

```sh
helm install db-operator oci://ghcr.io/benjamin-wright/db-operator/db-operator \
  --namespace db-operator \
  --create-namespace
```

To use a specific version:

```sh
helm install db-operator oci://ghcr.io/benjamin-wright/db-operator/db-operator \
  --namespace db-operator \
  --create-namespace \
  --version 0.1.0
```

To upgrade an existing installation:

```sh
helm upgrade db-operator oci://ghcr.io/benjamin-wright/db-operator/db-operator \
  --namespace db-operator
```

## Chart Values

| Value | Default | Description |
|-------|---------|-------------|
| `image.repository` | `localhost:5001/db-operator` | Operator image repository |
| `image.tag` | `latest` | Operator image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `instanceName` | `""` | Operator instance name; when set, only CRs carrying a matching `db-operator.benjamin-wright.github.com/operator-instance` label are reconciled |
| `resources.requests.cpu` | `50m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `resources.limits.cpu` | `200m` | CPU limit |
| `resources.limits.memory` | `128Mi` | Memory limit |

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
