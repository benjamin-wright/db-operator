# DB Operator Specification

## Purpose
A Kubernetes operator that provisions and manages self-contained database instances via CRDs.

## Scope
- `PostgresDatabase` CRD — declares a PostgreSQL instance (version 14–17) with a database name and storage size; the operator provisions a StatefulSet, headless Service, and admin Secret for each instance
- `PostgresCredential` CRD — declares a PostgreSQL user against a referenced `PostgresDatabase`; the operator generates a random password, creates the user with the specified permissions, and writes credentials to a named Kubernetes Secret in the same namespace
  - Supported permissions: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`, `ALL`
- Status conditions and a phase field (`Pending`, `Ready`, `Failed`) are maintained on both CRDs
- Multiple operator instances can coexist in the same cluster; instance-scoped filtering prevents collisions in test environments
  - When `--instance-name` is empty (the default), the operator processes CRs without the `games-hub.io/operator-instance` label and ignores labeled CRs
  - When `--instance-name` is set, the operator processes only CRs carrying a matching `games-hub.io/operator-instance` label and ignores unlabeled CRs
  - The value `"default"` is reserved and must not be used as an explicit instance name; the operator rejects this value at startup
  - All owned sub-resources carry the `games-hub.io/operator-instance` label matching their parent CR (or no label if the parent CR has none)
  - Leader election lock ID incorporates the instance name (or `"default"` when empty) to prevent conflicts between instances
  - Instance name is configured via `--instance-name` flag (default: empty)
  - The standalone local deployment (for integration testing) runs as a separate instance from the platform-wide deployment, without replacing or disabling it

## Interfaces
- `games-hub.io/v1alpha1/PostgresDatabase` — namespaced CRD; consumed by application deployments to request a PostgreSQL instance
- `games-hub.io/v1alpha1/PostgresCredential` — namespaced CRD; consumed by application deployments to request a database user and credentials Secret
- Kubernetes API server — the operator reads and writes StatefulSets, Services, and Secrets as owned sub-resources of each CRD

