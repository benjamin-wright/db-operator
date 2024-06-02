# DB Operator

An operator for creating and managing development databases and other stateful infrastructure

Currently supports:
- Postgres
- Redis
- NATs

## TODO

- PG tests to wait for client readiness
- Clean up postgres PVCs
- Clean up a bit:
  - Move existing-state-related functions to state.go
  - Find things like event logic to DRY up