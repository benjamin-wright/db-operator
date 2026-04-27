package pgconn

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// ConnDetails holds the Postgres connection parameters read from an operator-produced Secret.
type ConnDetails struct {
	Host     string
	Port     string
	User     string
	Password string
}

// Column describes a result column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResult holds the output of a SQL query.
type QueryResult struct {
	Columns []Column
	Rows    [][]string
}

// Query opens a single connection using details and database, sets statement_timeout
// to 10 seconds, executes sqlText, and returns up to rowLimit rows.
// The connection is closed before returning.
func Query(ctx context.Context, details ConnDetails, database, sqlText string, rowLimit int) (*QueryResult, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		details.Host, details.Port, details.User, details.Password, database,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening connection: %w", err)
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Close()

	// 10000 ms = 10 s; enforced server-side, survives client-side escapes.
	if _, err := conn.ExecContext(ctx, "SET statement_timeout = 10000"); err != nil {
		return nil, fmt.Errorf("setting statement_timeout: %w", err)
	}

	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("reading column types: %w", err)
	}

	columns := make([]Column, len(colTypes))
	for i, ct := range colTypes {
		columns[i] = Column{Name: ct.Name(), Type: ct.DatabaseTypeName()}
	}

	var result [][]string
	for rows.Next() {
		if len(result) >= rowLimit {
			break
		}
		vals := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make([]string, len(columns))
		for i, v := range vals {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = fmt.Sprintf("%v", v)
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	return &QueryResult{Columns: columns, Rows: result}, nil
}
