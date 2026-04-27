# DB MCP Specification

## Purpose
An in-cluster Model Context Protocol server that exposes read-only inspection of databases managed by db-operator to LLM clients.

## Scope
- Discovers `PostgresCluster` CRs across all watched namespaces and maintains an in-memory index of cluster → databases
- For each discovered `PostgresCluster`, reconciles a managed `PostgresCredential` CR granting `SELECT`-only access to every database on that cluster; credential lifecycle (role creation, grants, Secret materialisation) is handled by the operator
- Waits for the operator-produced Secret before serving requests against a cluster
- Exposes two MCP tools for Postgres:
  - `pg_list_clusters` — returns all visible `PostgresCluster` CRs with their namespace, name, host, and the list of databases derived from `PostgresCredential` CRs that target the cluster
  - `pg_exec_sql` — executes a SQL statement against a named (cluster, database) pair using the read-only credential; inputs are cluster ref, database name, SQL text, and an optional row limit; output is column metadata and rows capped at the row limit
- Sets a `statement_timeout` on every connection; input is not wrapped in `SET TRANSACTION READ ONLY` because the read-only role's privileges are the actual safety boundary
- Development-only server, intended to be port-forwarded to a developer's host; not hardened for untrusted network access

## Interfaces
- MCP protocol — served over HTTP; consumed by LLM clients (e.g. IDEs with MCP support)
- Kubernetes API — reads `PostgresCluster` and `PostgresCredential` CRs and their operator-produced Secrets; creates and reconciles managed `PostgresCredential` CRs owned by the MCP server Deployment
- PostgreSQL — connects using read-only credentials from operator-produced Secrets; one connection per (cluster, database) pair on demand
