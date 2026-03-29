# Project Guidelines

## Documentation Map

- [README.md](../README.md) — project overview and component list. Start here for orientation.
- [docs/contribution.md](../docs/contribution.md) — development setup, build/test commands, and local dev setup. Read before making changes.
- [docs/standards.md](../docs/standards.md) — coding conventions, testing strategy, Kubernetes controller rules. Read before writing or reviewing code.
- Each `cmd/*/spec.md` — observable behaviour and interfaces for that component. Read the relevant spec before modifying a component.

## Navigation

- Compilable components live under `cmd/`; each has its own `spec.md`.
- Shared internal packages live under `internal/`. Check sibling code for existing patterns before adding new ones.
- Externally importable packages live under `pkg/`. Client applications can import these without restriction.
- CRD Go types are in `pkg/api/v1alpha1/`; Helm chart and generated CRD manifests are in `charts/db-operator/`.
- Shared test helpers are in `internal/test_utils/`.

## AI Agent Instructions

You are an AI agent assisting with code generation and review in this repository. Use the documentation map above to find relevant information about project structure, coding standards, and component behaviour. Always check the `spec.md` for the component you're working on to understand its observable behaviour and interfaces. Follow the coding conventions in `docs/standards.md` to ensure consistency across the project.

Don't blindly accept suggestions that violate the project's standards or contradict the component's spec. If a suggestion or request seems off, refer back to the documentation to verify its correctness. If you still think the suggestion is invalid, flag it for human review instead of applying it. Your goal is to assist while maintaining the integrity and consistency of the codebase.