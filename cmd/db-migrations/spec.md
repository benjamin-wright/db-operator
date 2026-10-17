# DB Migrations Specification

## Purpose
A reusable framework that applies and tracks versioned SQL schema changes against PostgreSQL databases.

## Scope
- Executable and Docker image for migration execution and tracking, used by operator-managed Jobs or directly with PostgreSQL credentials
- A target migration ID selects forward migration or rollback; zero rolls back all applied migrations, and omission applies all discovered migrations
- Apps provide migration files named `<id>-<name>-apply.sql` and `<id>-<name>-rollback.sql`, with unique numeric IDs and both files present
- Tracks applied migrations and stores content hashes of apply and rollback files; raises an error if a previously-applied file's content has changed — editing an SQL file that has already been applied in a prior artifact is a **hard error** that fails the Job
- Acquires a session-scoped PostgreSQL advisory lock before accessing the tracking table and retains that same connection for every migration transaction; lock acquisition is bounded to 30 seconds
- Operator-managed Jobs require their claim UID and operation name to match the database authority record after acquiring the lock; superseded or revoked Jobs fail before executing migration SQL
- Read-only verification checks the exact target and recorded migration hashes without applying or rolling back SQL
- Supports an `--artifact <oci-ref>` flag that fetches migration SQL directly from an OCI artifact rather than from the local filesystem; the artifact must have exactly one layer of media type `application/vnd.db-operator.migrations.v1.tar+gzip` containing a flat tar+gzip archive of the migration file pairs

## Interfaces
- Docker image — `docker.io/benwright/db-migrations`, tagged with the operator release
- `--migrations-dir <path>` — filesystem migration directory, default `/migrations`
- `--artifact <oci-ref>` — when set, migration files are fetched from the given OCI reference (tag or digest-pinned) at runtime; the reference must resolve to an artifact with the `application/vnd.db-operator.migrations.v1.tar+gzip` layer media type
- `--target <revision>` — numeric revision to converge to; omit to apply all discovered migrations
- `--verify-only` — verify ledger hashes and target under the same lock without executing migration SQL
- `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` — PostgreSQL connection inputs; defaults are `localhost`, `5432`, `postgres`, `postgres`, `postgres`. Connections use a 10-second connect timeout and disabled TLS
- `MIGRATION_CLAIM_UID`, `MIGRATION_OPERATION` — authority inputs supplied by operator-managed Jobs; each nonempty value is verified against the protected database record
- PostgreSQL connection — reads and writes a migrations tracking table in the target database; acquires a session-scoped advisory lock for the duration of the run
