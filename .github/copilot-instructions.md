# Project Guidelines

## Documentation Map

- [README.md](../README.md) — project structure, build/test commands, local dev setup. Start here for orientation.
- [docs/standards.md](../docs/standards.md) — coding conventions, testing strategy, Kubernetes controller rules. Read before writing or reviewing code.
- Each `cmd/*/spec.md` — observable behaviour and interfaces for that component. Read the relevant spec before modifying a component.

## Navigation

- Compilable components live under `cmd/`; each has its own `spec.md`.
- Shared internal packages live under `internal/`. Check sibling code for existing patterns before adding new ones.
- CRD Go types are in `internal/operator/api/v1alpha1/`; Helm chart and generated CRD manifests are in `charts/db-operator/`.
- Shared test helpers are in `internal/test_utils/`.
