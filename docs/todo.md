# db-operator todo

No active work items in this repo. The `PostgresMigrationSet` rollout, the
migrations owner role + concurrency safety, and the sequence-grant bugs
(Fix A and Fix C) are all complete and verified by the integration suite.

## Cross-repo follow-ups (not owned here)

- [ ] **wp-operator Fix C** (wasm-platform): in `application_controller`, gate
  creation of non-owner (writer/reader) `PostgresCredential`s behind the
  migrations Job reporting `Succeeded`. Tracked in `wasm-platform/docs/todo.md`
  under the Phase 9 work.
- [ ] **wasm-platform e2e-tests**: trigger via the Tilt MCP server in the
  wasm-platform workspace and confirm it passes. This is the cross-repo gate
  that also covers sequence-grant Fix A end-to-end.

