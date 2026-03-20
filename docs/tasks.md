# Controller Upgrade Tasks

Bring the remaining controllers up to the standard established by `postgresdatabase_controller.go`.

## Reference patterns

| Pattern | Standard |
|---|---|
| Struct fields | `InstanceName string`, wrapped `client`, separate `builder` — no embedded `client.Client` or `*runtime.Scheme` |
| File separation | `_client.go` for k8s interaction, `_builder.go` for resource construction |
| Error helpers | `isConflict()`, `isForbidden()`, `isNotFound()` (defined in the shared client file) |
| `get()` | Returns `(bool, error)` — `false` means not-found; no inline `apierrors` checks at call sites |
| `delete()` | Not-found treated as success internally |
| Forbidden logging | `logger.Error(reconcileErr, "reconcile blocked by Forbidden error; namespace may be terminating")` |

---

## Task 1 — RedisDatabaseReconciler

**File:** `internal/operator/controller/redisdatabase_controller.go`

- [x] Extract `desiredRedisService()`, `desiredRedisStatefulSet()`, and admin Secret construction into a new `redisdatabase_builder.go`
- [x] Extract a `redisDatabaseClient` struct into a new `redisdatabase_client.go`
- [x] Rework the reconciler struct to hold `client`/`builder` instead of embedding `client.Client`/`Scheme`
- [x] Replace all `r.Get()`, `r.Create()`, `r.Update()`, `r.Delete()`, `r.Status().Update()` calls with `client` methods
- [x] Replace `apierrors.IsConflict()` etc. with the shared `isConflict()` helpers from `postgresdatabase_client.go`

---

## Task 2 — NatsAccountReconciler

**File:** `internal/operator/controller/natsaccount_controller.go`

- [ ] Extract user Secret construction into a new `natsaccount_builder.go`
- [ ] Extract a `natsAccountClient` struct into a new `natsaccount_client.go`
- [ ] Rework the reconciler struct to hold `client`/`builder` instead of embedding `client.Client`/`Scheme`
- [ ] Replace all direct k8s calls with `client` methods
- [ ] Replace `apierrors.XXX()` checks with shared helpers
- [ ] Add missing `logger.Error()` on the Forbidden path

---

## Task 3 — NatsClusterReconciler

**File:** `internal/operator/controller/natscluster_controller.go`

- [ ] Extract `desiredNatsService()`, `desiredNatsDeployment()`, inline ConfigMap, and JetStream PVC construction into a new `natscluster_builder.go`
- [ ] Extract a `natsClusterClient` struct into a new `natscluster_client.go`
- [ ] Rework the reconciler struct to hold `client`/`builder` instead of embedding `client.Client`/`Scheme`
- [ ] Replace all direct k8s calls with `client` methods
- [ ] Replace `apierrors.XXX()` checks with shared helpers
- [ ] Add missing `logger.Error()` on the Forbidden path

---

## Task 4 — PostgresCredentialReconciler

**File:** `internal/operator/controller/postgrescredential_controller.go`

- [ ] Extract k8s interaction into a new `postgrescredential_client.go`
- [ ] Move direct `database/sql` / `lib/pq` calls (currently in `ensurePostgresUser`) behind an interface injected into the reconciler, per the External Dependency Ownership standard
- [ ] Rework the reconciler struct to hold `client` and the new Postgres interface instead of embedding `client.Client`/`Scheme`
- [ ] Replace `apierrors.XXX()` checks with shared helpers
- [ ] Add missing `logger.Error()` on the Forbidden path

---

## Task 5 — RedisCredentialReconciler

**File:** `internal/operator/controller/rediscredential_controller.go`

- [ ] Extract k8s interaction into a new `rediscredential_client.go`
- [ ] Move direct `go-redis/v9` calls (currently in `ensureRedisACLUser`) behind an interface injected into the reconciler, per the External Dependency Ownership standard
- [ ] Rework the reconciler struct to hold `client` and the new Redis interface instead of embedding `client.Client`/`Scheme`
- [ ] Replace `apierrors.XXX()` checks with shared helpers
- [ ] Add missing `logger.Error()` on the Forbidden path
