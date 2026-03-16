# db-operator

A Kubernetes operator that provisions and manages PostgreSQL instances and credentials via CRDs.

## Components

| Command | Description | Spec |
|---------|-------------|------|
| `cmd/db-operator` | Kubernetes operator — watches `PostgresDatabase` and `PostgresCredential` CRDs | [spec](cmd/db-operator/spec.md) |
| `cmd/db-migrations` | Reusable migration runner — applies versioned SQL schema changes to PostgreSQL | [spec](cmd/db-migrations/spec.md) |

## Project Structure

```
cmd/                  # Compilable entry points (one per component)
internal/
  operator/           # CRD types (api/) and reconciliation logic (controller/)
  migrations/         # Migration discovery, execution, and tracking
  test_utils/         # Shared test helpers
charts/db-operator/   # Helm chart (CRDs, templates, values)
docs/                 # Project-wide standards
```

## Development

### Prerequisites

- Go 1.25+
- [k3d](https://k3d.io)
- [Tilt](https://tilt.dev)
- Helm

### Local Cluster

```sh
make cluster-up      # Create k3d cluster with registry
make cluster-down    # Tear down the cluster
```

After `cluster-up`, export the kubeconfig it prints (or use direnv).

### Tilt

```sh
tilt up
```

Builds the operator image, deploys the Helm chart into a `db-operator` namespace, and runs integration tests on change.

### Make Targets

| Target | Description |
|--------|-------------|
| `make generate` | Regenerate DeepCopy methods and CRD manifests |
| `make fmt` | Run `go fmt` |
| `make vet` | Run `go vet` |
| `make test` | Run unit tests |
| `make integration-test` | Run integration tests (requires a running cluster) |

## Standards

See [docs/standards.md](docs/standards.md) for coding conventions, testing strategy, and Kubernetes controller guidelines.