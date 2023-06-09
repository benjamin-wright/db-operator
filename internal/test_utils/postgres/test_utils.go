package postgres

import (
	"context"
	"testing"

	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/jackc/pgx/v4"
)

type TestUtils struct {
	conn *pgx.Conn
}

func New(cfg postgres.ConnectConfig) (*TestUtils, error) {
	conn, err := postgres.Connect(cfg)
	if err != nil {
		return nil, err
	}

	return &TestUtils{
		conn: conn,
	}, nil
}

func (u *TestUtils) GetTableNames(t *testing.T) []string {
	rows, err := u.conn.Query(context.Background(), "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'")
	if err != nil {
		t.Fatalf("failed to get table names: %+v", err)
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			t.Fatalf("failed to read table name: %+v", err)
		}

		tableNames = append(tableNames, tableName)
	}

	return tableNames
}
