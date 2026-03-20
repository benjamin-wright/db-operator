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
  databaseName: myapp
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
  username: <base64>   # the username specified in the CR
  password: <base64>   # auto-generated 24-character random password
  host:     <base64>   # in-cluster DNS name, e.g. my-postgres.default.svc.cluster.local
  port:     <base64>   # always 5432
  database: <base64>   # databaseName from the PostgresDatabase spec
```

Example usage in a Pod:

```yaml
env:
  - name: PGUSER
    valueFrom:
      secretKeyRef:
        name: myapp-postgres-secret
        key: username
  - name: PGPASSWORD
    valueFrom:
      secretKeyRef:
        name: myapp-postgres-secret
        key: password
  - name: PGHOST
    valueFrom:
      secretKeyRef:
        name: myapp-postgres-secret
        key: host
  - name: PGPORT
    valueFrom:
      secretKeyRef:
        name: myapp-postgres-secret
        key: port
  - name: PGDATABASE
    valueFrom:
      secretKeyRef:
        name: myapp-postgres-secret
        key: database
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
  username: <base64>   # the username specified in the CR
  password: <base64>   # auto-generated 24-character random password
  host:     <base64>   # in-cluster DNS name, e.g. my-redis.default.svc.cluster.local
  port:     <base64>   # always 6379
```

Example usage in a Pod:

```yaml
env:
  - name: REDIS_USERNAME
    valueFrom:
      secretKeyRef:
        name: myapp-redis-secret
        key: username
  - name: REDIS_PASSWORD
    valueFrom:
      secretKeyRef:
        name: myapp-redis-secret
        key: password
  - name: REDIS_HOST
    valueFrom:
      secretKeyRef:
        name: myapp-redis-secret
        key: host
  - name: REDIS_PORT
    valueFrom:
      secretKeyRef:
        name: myapp-redis-secret
        key: port
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
  username: <base64>   # the username specified in the user entry
  password: <base64>   # auto-generated 24-character random password
  account:  <base64>   # name of the parent NatsAccount CR
  host:     <base64>   # in-cluster DNS name, e.g. my-nats.default.svc.cluster.local
  port:     <base64>   # always 4222
```

Example usage in a Pod:

```yaml
env:
  - name: NATS_URL
    value: nats://$(NATS_USERNAME):$(NATS_PASSWORD)@$(NATS_HOST):$(NATS_PORT)
  - name: NATS_USERNAME
    valueFrom:
      secretKeyRef:
        name: publisher-nats-secret
        key: username
  - name: NATS_PASSWORD
    valueFrom:
      secretKeyRef:
        name: publisher-nats-secret
        key: password
  - name: NATS_HOST
    valueFrom:
      secretKeyRef:
        name: publisher-nats-secret
        key: host
  - name: NATS_PORT
    valueFrom:
      secretKeyRef:
        name: publisher-nats-secret
        key: port
```

## Components

| Command | Description | Spec |
|---------|-------------|------|
| `cmd/db-operator` | Kubernetes operator — watches all CRDs listed above | [spec](cmd/db-operator/spec.md) |
| `cmd/db-migrations` | Reusable migration runner — applies versioned SQL schema changes to PostgreSQL | [spec](cmd/db-migrations/spec.md) |

## Contributing

See [docs/contribution.md](docs/contribution.md) for development setup, make targets, and coding standards.
