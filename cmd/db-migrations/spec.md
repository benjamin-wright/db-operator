# DB Migrations Specification

## Purpose
A reusable framework that applies and tracks versioned SQL schema changes against PostgreSQL databases.

## Scope
- Base Docker image encapsulating migration execution and tracking logic
- Common Helm chart deploying a Kubernetes Job
- Helm chart accepts a target migration ID; when set, applies up to that ID or rolls back to it, enabling selective rollouts and rollbacks
- Apps provide only migration files, named `<id>-<name>-apply.sql` and `<id>-<name>-rollback.sql`
- Tracks applied migrations and stores content hashes of apply and rollback files; raises an error if a previously-applied file's content has changed
- Acquires a session-scoped PostgreSQL advisory lock before accessing the tracking table; concurrent Job pods serialise on this lock so the second pod waits, then applies nothing if the first has already run all migrations

## Interfaces
- Base Docker image — extended by each app to include its migration files
- Helm chart — deployed per-app as a Job alongside the application's own resources
- Tilt functions — loaded from `tools/tilt/utils.tiltfile` by app Tiltfiles; one builds the base migration image, another builds an app-specific image and deploys it
- PostgreSQL connection — reads and writes a migrations tracking table in the target database
