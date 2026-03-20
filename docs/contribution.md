# Contributing

## Prerequisites

- Go 1.25+
- [k3d](https://k3d.io)
- [Tilt](https://tilt.dev)
- Helm

## Local Cluster

```sh
make cluster-up      # Create k3d cluster with registry
make cluster-down    # Tear down the cluster
```

After `cluster-up`, export the kubeconfig it prints (or use direnv).

## Tilt

```sh
tilt up
```

Builds the operator image, deploys the Helm chart into a `db-operator` namespace, and runs integration tests on change.

## Make Targets

| Target | Description |
|--------|-------------|
| `make generate` | Regenerate DeepCopy methods and CRD manifests |
| `make fmt` | Run `go fmt` |
| `make vet` | Run `go vet` |
| `make test` | Run unit tests |
| `make integration-test` | Run integration tests (requires a running cluster) |

## Standards

See [docs/standards.md](standards.md) for coding conventions, testing strategy, and Kubernetes controller guidelines.

## TODOs

- Update the gitlab build to run arm builds on arm machines, cross-platform is way too slow
