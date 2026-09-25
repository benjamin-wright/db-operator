# DB Migrations Specification

## Purpose
A reusable framework that applies and tracks versioned SQL schema changes against PostgreSQL databases.

## Scope
- Base Docker image encapsulating migration execution and tracking logic
- Common Helm chart deploying a Kubernetes Job
- Helm chart accepts a target migration ID; when set, applies up to that ID or rolls back to it, enabling selective rollouts and rollbacks
- Apps provide only migration files, named `<id>-<name>-apply.sql` and `<id>-<name>-rollback.sql`
- Tracks applied migrations and stores content hashes of apply and rollback files; raises an error if a previously-applied file's content has changed — editing an SQL file that has already been applied in a prior artifact is a **hard error** that fails the Job
- Acquires a session-scoped PostgreSQL advisory lock before accessing the tracking table; concurrent Job pods serialise on this lock so the second pod waits, then applies nothing if the first has already run all migrations
- Supports an `--artifact <oci-ref>` flag that fetches migration SQL directly from an OCI artifact rather than from the local filesystem; the artifact must have exactly one layer of media type `application/vnd.db-operator.migrations.v1.tar+gzip` containing a flat tar+gzip archive of the migration file pairs

## Interfaces
- Base Docker image — extended by each app to include its migration files
- Helm chart — deployed per-app as a Job alongside the application's own resources
- `--artifact <oci-ref>` — when set, migration files are fetched from the given OCI reference (tag or digest-pinned) at runtime; the reference must resolve to an artifact with the `application/vnd.db-operator.migrations.v1.tar+gzip` layer media type
- `--target <revision>` — numeric revision to converge to; omit to apply all discovered migrations
- PostgreSQL connection — reads and writes a migrations tracking table in the target database; acquires a session-scoped advisory lock for the duration of the run
