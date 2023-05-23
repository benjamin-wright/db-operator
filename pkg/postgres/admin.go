package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"go.uber.org/zap"
)

func parsePGXError(err error) error {
	if err == nil {
		return nil
	}

	if pgerr, ok := err.(*pgconn.PgError); ok {
		if pgerr.Message == "no object matched" {
			return nil
		}

		return fmt.Errorf("%s [%s]: %s", pgerr.Message, pgerr.Code, pgerr.Detail)
	}

	return err
}

type AdminConn struct {
	conn *pgx.Conn
}

func NewAdminConn(cfg ConnectConfig) (*AdminConn, error) {
	conn, err := Connect(cfg)
	if err != nil {
		return nil, err
	}

	return &AdminConn{conn}, nil
}

func (d *AdminConn) Stop() {
	d.conn.Close(context.Background())
}

func (d *AdminConn) ListUsers() ([]string, error) {
	rows, err := d.conn.Query(context.Background(), "SHOW USERS")
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %+v", err)
	}
	defer rows.Close()

	users := []string{}

	for rows.Next() {
		var user string
		if err := rows.Scan(&user, nil, nil); err != nil {
			return nil, fmt.Errorf("failed to decode database user: %+v", err)
		}

		users = append(users, user)
	}

	return users, nil
}

func (d *AdminConn) CreateUser(username string) error {
	zap.S().Infof("Creating user %s", username)
	if _, err := d.conn.Exec(context.Background(), "CREATE USER "+sanitize(username)); err != nil {
		return fmt.Errorf("failed to create database user: %+v", err)
	}

	return nil
}

func (d *AdminConn) DropUser(username string) error {
	zap.S().Infof("Deleting user %s", username)
	if _, err := d.conn.Exec(context.Background(), "DROP USER "+sanitize(username)); err != nil {
		return fmt.Errorf("failed to drop database user: %+v", err)
	}

	return nil
}

func (d *AdminConn) ListDatabases() ([]string, error) {
	rows, err := d.conn.Query(context.Background(), "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %+v", err)
	}
	defer rows.Close()

	databases := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name, nil, nil, nil, nil, nil); err != nil {
			return nil, fmt.Errorf("failed to decode database: %+v", err)
		}

		databases = append(databases, name)
	}

	return databases, nil
}

func sanitize(name string) string {
	return pgx.Identifier.Sanitize([]string{name})
}

func (d *AdminConn) CreateDatabase(database string) error {
	zap.S().Infof("Creating database %s", database)
	if _, err := d.conn.Exec(context.Background(), "CREATE DATABASE "+sanitize(database)); err != nil {
		return fmt.Errorf("failed to create database: %+v", err)
	}

	return nil
}

func (d *AdminConn) DropDatabase(database string) error {
	zap.S().Infof("Dropping database %s", database)
	if _, err := d.conn.Exec(context.Background(), "DROP DATABASE "+sanitize(database)); err != nil {
		return fmt.Errorf("failed to drop database: %+v", err)
	}

	return nil
}

func (d *AdminConn) ListPermitted(database string) ([]string, error) {
	rows, err := d.conn.Query(context.Background(), "SHOW GRANTS ON DATABASE "+sanitize(database))
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions: %+v", err)
	}
	defer rows.Close()

	permittedMap := map[string]struct{}{}

	for rows.Next() {
		var user string
		var privilege_type string
		if err := rows.Scan(nil, &user, &privilege_type, nil); err != nil {
			return nil, fmt.Errorf("failed to decode user permission: %+v", err)
		}

		if privilege_type == "ALL" {
			permittedMap[user] = struct{}{}
		}
	}

	permitted := make([]string, len(permittedMap))
	for user := range permittedMap {
		permitted = append(permitted, user)
	}

	return permitted, nil
}

func (d *AdminConn) GrantPermissions(username string, database string) error {
	query := fmt.Sprintf("GRANT ALL ON DATABASE %s TO %s", sanitize(database), sanitize(username))
	if _, err := d.conn.Exec(context.Background(), query); err != nil {
		return fmt.Errorf("failed to grant permissions: %+v", err)
	}

	query = fmt.Sprintf("USE %s; ALTER DEFAULT PRIVILEGES FOR ALL ROLES GRANT ALL ON TABLES TO %s;", sanitize(database), sanitize(username))
	_, err := d.conn.Exec(context.Background(), query)
	if pgerr := parsePGXError(err); pgerr != nil {
		return fmt.Errorf("failed to grant default table permissions: %+v", pgerr)
	}

	query = fmt.Sprintf("GRANT ALL ON %s.* TO %s", sanitize(database), sanitize(username))
	_, err = d.conn.Exec(context.Background(), query)
	if pgerr := parsePGXError(err); pgerr != nil {
		return fmt.Errorf("failed to grant existing table permissions: %+v", err)
	}

	zap.S().Infof("Granted '%s' permission to read/write to '%s'", username, database)

	return nil
}

func (d *AdminConn) RevokePermissions(username string, database string) error {
	query := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ALL ROLES REVOKE ALL ON DATABASE %s FROM %s;", sanitize(database), sanitize(username))
	if _, err := d.conn.Exec(context.Background(), query); err != nil {
		return fmt.Errorf("failed to revoke default permissions: %+v", err)
	}

	query = fmt.Sprintf("REVOKE ALL ON * FROM %s", sanitize(username))
	if _, err := d.conn.Exec(context.Background(), query); err != nil {
		return fmt.Errorf("failed to revoke existing table permissions: %+v", err)
	}

	query = fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM %s", sanitize(database), sanitize(username))
	if _, err := d.conn.Exec(context.Background(), query); err != nil {
		return fmt.Errorf("failed to revoke permissions: %+v", err)
	}

	zap.S().Infof("Revoked '%s' permission to read/write from '%s'", username, database)

	return nil
}
