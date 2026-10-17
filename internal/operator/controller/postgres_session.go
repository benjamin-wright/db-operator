package controller

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type postgresExecutor interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// Every statement stays on the connection that owns its advisory lock. A lost
// connection cannot silently reconnect and continue a superseded mutation.
type postgresSession struct {
	conn   *sql.Conn
	db     *sql.DB
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *postgresSession) Exec(query string, args ...any) (sql.Result, error) {
	return s.conn.ExecContext(s.ctx, query, args...)
}
func (s *postgresSession) Query(query string, args ...any) (*sql.Rows, error) {
	return s.conn.QueryContext(s.ctx, query, args...)
}
func (s *postgresSession) QueryRow(query string, args ...any) *sql.Row {
	return s.conn.QueryRowContext(s.ctx, query, args...)
}
func (s *postgresSession) Begin() (*sql.Tx, error) {
	return s.conn.BeginTx(s.ctx, nil)
}
func (s *postgresSession) Close() error {
	s.cancel()
	if s.db == nil {
		return nil
	}
	_ = s.conn.Close()
	return s.db.Close()
}

func (p postgresManager) open(host, user, password, database string) (*postgresSession, error) {
	if p.boundSessions != nil {
		conn := p.boundSessions[database]
		if conn == nil {
			return nil, fmt.Errorf("database has no held migration fence")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		return &postgresSession{conn: conn, ctx: ctx, cancel: cancel}, nil
	}
	if p.ownership != nil && database == "postgres" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		return &postgresSession{conn: p.ownership.session, ctx: ctx, cancel: cancel}, nil
	}
	db, err := openPostgres(host, user, password, database)
	if err != nil {
		return nil, err
	}
	if p.ownership != nil {
		return p.ownership.lockDatabase(db)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := db.Conn(ctx)
	if err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	return &postgresSession{conn: conn, db: db, ctx: ctx, cancel: cancel}, nil
}

// Database-local grants and revocations also serialize on their own session.
// A successor cannot pass this lock while a previous target mutation is active,
// even if that predecessor's maintenance connection has already disappeared.
func (o *userOwnership) lockDatabase(db *sql.DB) (*postgresSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := db.Conn(ctx)
	if err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	s := &postgresSession{conn: conn, db: db, ctx: ctx, cancel: cancel}
	var locked bool
	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "credential/"+o.username).Scan(&locked)
	if err == nil && !locked {
		err = fmt.Errorf("credential database policy mutation is already in progress")
	}
	if err == nil {
		err = o.session.PingContext(ctx)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
