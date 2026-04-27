package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/benjamin-wright/db-operator/internal/pgconn"
	"github.com/benjamin-wright/db-operator/internal/pgwatcher"
)

// New constructs and returns an HTTP handler serving the MCP server with the
// pg_list_clusters and pg_exec_sql tools.
func New(index *pgwatcher.Index) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "db-mcp",
		Version: "v0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pg_list_clusters",
		Description: "List all visible PostgresDatabase clusters with namespace, name, host, and databases.",
	}, newListClusters(index))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pg_exec_sql",
		Description: "Execute a read-only SQL statement against a named cluster and database. Returns column metadata and rows capped at row_limit.",
	}, newExecSQL(index))

	return mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, nil)
}

// ── pg_list_clusters ──────────────────────────────────────────────────────────

type listClustersInput struct{}

type clusterEntry struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Databases []string `json:"databases"`
}

func newListClusters(index *pgwatcher.Index) func(context.Context, *mcp.CallToolRequest, listClustersInput) (*mcp.CallToolResult, any, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ listClustersInput) (*mcp.CallToolResult, any, error) {
		clusters := index.List()
		entries := make([]clusterEntry, 0, len(clusters))
		for _, c := range clusters {
			if !c.Ready {
				continue
			}
			entries = append(entries, clusterEntry{
				Namespace: c.Namespace,
				Name:      c.Name,
				Host:      c.Host,
				Databases: c.Databases,
			})
		}
		data, err := json.Marshal(entries)
		if err != nil {
			return nil, nil, fmt.Errorf("marshalling cluster list: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		}, nil, nil
	}
}

// ── pg_exec_sql ───────────────────────────────────────────────────────────────

type execSQLInput struct {
	ClusterNamespace string `json:"cluster_namespace" jsonschema:"Kubernetes namespace of the PostgresDatabase"`
	ClusterName      string `json:"cluster_name"      jsonschema:"Name of the PostgresDatabase"`
	Database         string `json:"database"          jsonschema:"Name of the PostgreSQL database to connect to"`
	SQL              string `json:"sql"               jsonschema:"SQL statement to execute"`
	RowLimit         int    `json:"row_limit,omitempty" jsonschema:"Maximum rows to return (default 100, max 1000)"`
}

type execSQLOutput struct {
	Columns []pgconn.Column `json:"columns"`
	Rows    [][]string      `json:"rows"`
}

func newExecSQL(index *pgwatcher.Index) func(context.Context, *mcp.CallToolRequest, execSQLInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input execSQLInput) (*mcp.CallToolResult, any, error) {
		key := pgwatcher.ClusterKey{Namespace: input.ClusterNamespace, Name: input.ClusterName}
		info, ok := index.Get(key)
		if !ok {
			return nil, nil, fmt.Errorf("cluster %s/%s not found", input.ClusterNamespace, input.ClusterName)
		}
		if !info.Ready {
			return nil, nil, fmt.Errorf("cluster %s/%s is not yet ready", input.ClusterNamespace, input.ClusterName)
		}

		rowLimit := input.RowLimit
		if rowLimit <= 0 {
			rowLimit = 100
		}
		if rowLimit > 1000 {
			rowLimit = 1000
		}

		result, err := pgconn.Query(ctx, pgconn.ConnDetails{
			Host:     info.Host,
			Port:     info.Port,
			User:     info.User,
			Password: info.Password,
		}, input.Database, input.SQL, rowLimit)
		if err != nil {
			return nil, nil, err
		}

		data, err := json.Marshal(execSQLOutput{Columns: result.Columns, Rows: result.Rows})
		if err != nil {
			return nil, nil, fmt.Errorf("marshalling result: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		}, nil, nil
	}
}
