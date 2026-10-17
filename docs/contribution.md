# Contributing

## Prerequisites

- Go 1.25+
- [k3d](https://k3d.io)
- [Tilt](https://tilt.dev)
- Helm
- Python 3 (for generated-source and formatting checks)

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

Builds the operator, migration-runner and MCP images, deploys the Helm chart into a `db-operator` namespace, and runs validation on change. `generated-check` compares CRDs and DeepCopy code against the pinned generator in a temporary source tree; it never rewrites the checkout. Use the manual `generate` resource when API sources change.

The `unit-tests` and `lint` resources check all Go packages and the Helm chart. Once they pass and the operator is ready, `integration-tests` runs every migration and controller suite against real services, including credential revocation, migration journaling, Redis and NATS. Suites run once with two Ginkgo workers, a fixed seed and a twenty-minute deadline, without focus or skip filters. The `validation` resource becomes successful only after all four gates pass. Standalone focused Make targets remain available for diagnosis.

## Make Targets

| Target | Description |
|--------|-------------|
| `make generate` | Regenerate DeepCopy methods and CRD manifests |
| `make check-generated` | Verify generated contracts without editing source |
| `make fmt` | Run `go fmt` |
| `make vet` | Run `go vet` |
| `make lint` | Check Go formatting, vet normal/integration code and lint Helm |
| `make test` | Run unit tests |
| `make integration-test` | Run integration tests (requires a running cluster) |

## Standards

See [docs/standards.md](standards.md) for coding conventions, testing strategy, and Kubernetes controller guidelines.

## TODOs

- Update the gitlab build to run arm builds on arm machines, cross-platform is way too slow
