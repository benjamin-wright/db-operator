# Redis CRD Support — TODO

## Overview

Add two new CRDs — **RedisDatabase** and **RedisCredential** — following the established Postgres CRD patterns. `RedisDatabase` represents a single-instance Redis 8 deployment with admin credentials. `RedisCredential` defines ACL-controlled users scoped to a specific `RedisDatabase`, with key pattern restrictions and permission control via both Redis ACL categories and individual commands.

## Decisions

- **Redis 8 only** — no version selector field (unlike Postgres which supports 14–17). Hardcoded image tag.
- **Separate instances for isolation** — each `RedisDatabase` CR deploys its own StatefulSet, mirroring the Postgres pattern.
- **ACL granularity** — both category-based (`@read`, `@write`, etc.) and individual command-based (`get`, `set`, etc.) permissions.
- **Key patterns** for data scoping — credentials specify which Redis key patterns the user can access (e.g., `user:*`, `cache:*`).

## Phase 1: RedisDatabase CRD type

Create `internal/operator/api/v1alpha1/redisdatabase_types.go`:

- `RedisDatabasePhase` type — `Pending` / `Ready` / `Failed` (kubebuilder Enum)
- `RedisDatabaseSpec`:
  - `StorageSize` (`resource.Quantity`, required) — PVC size
- `RedisDatabaseStatus`:
  - `Phase` (`RedisDatabasePhase`, default `Pending`)
  - `SecretName` (string, optional) — admin credentials secret
  - `Conditions` (`[]metav1.Condition`, listType=map)
- `RedisDatabase` root type — shortName `rdb`, category `games-hub`, printcolumns: Storage, Phase, Age
- `RedisDatabaseList` + `init()` self-registration
- Pattern reference: `PostgresDatabaseSpec` / `PostgresDatabaseStatus` in `postgresdatabase_types.go`

## Phase 2: RedisCredential CRD type

Create `internal/operator/api/v1alpha1/rediscredential_types.go`:

- `RedisCredentialPhase` type — `Pending` / `Ready` / `Failed`
- `RedisACLCategory` enum — `read`, `write`, `set`, `sortedset`, `list`, `hash`, `string`, `bitmap`, `hyperloglog`, `geo`, `stream`, `pubsub`, `admin`, `fast`, `slow`, `blocking`, `dangerous`, `connection`, `transaction`, `scripting`, `keyspace`, `all`
- `RedisCredentialSpec`:
  - `DatabaseRef` (string, required, minLength=1) — name of `RedisDatabase` in same namespace
  - `Username` (string, required, 1–63 chars) — Redis ACL username
  - `SecretName` (string, required, minLength=1) — Secret to create with credentials
  - `KeyPatterns` (`[]string`, optional) — Redis key patterns (e.g., `user:*`, `cache:*`)
  - `ACLCategories` (`[]RedisACLCategory`, optional) — ACL categories to grant
  - `Commands` (`[]string`, optional) — individual Redis commands to allow
- `RedisCredentialStatus`:
  - `Phase`, `Conditions`, `SecretName` (same shape as PostgresCredential)
- `RedisCredential` root type — shortName `rcred`, category `games-hub`, printcolumns: Database, Username, Secret, Phase, Age
- `RedisCredentialList` + `init()` self-registration
- Pattern reference: `PostgresCredentialSpec` / `DatabasePermission` in `postgrescredential_types.go`

## Phase 3: Code generation and spec update

1. Run `make generate` to regenerate `zz_generated.deepcopy.go` with DeepCopy methods for all new types
2. Run `make manifests` to produce CRD YAMLs in `charts/db-operator/crds/`
3. Update `cmd/db-operator/spec.md` to document Redis CRDs alongside the existing Postgres ones

## Verification

1. `go build ./...` compiles without errors
2. `make generate` updates `zz_generated.deepcopy.go` with all new Redis types
3. `make manifests` produces `db-operator.benjamin-wright.github.com_redisdatabases.yaml` and `db-operator.benjamin-wright.github.com_rediscredentials.yaml`
4. Inspect generated CRD YAMLs: confirm enum validation on phases and ACL categories, minLength/maxLength on strings, `anyOf` int-or-string for `storageSize`
5. `kubectl apply --dry-run=server -f charts/db-operator/crds/` passes (if cluster available)
