package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
	conn *pgxpool.Pool
}

func New(cfg config.Config) (*Client, error) {
	conn, err := config.Connect(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{conn}, nil
}

func (c *Client) Stop() {
	c.conn.Close()
}

func (c *Client) ListUsers() ([]string, error) {
	rows, err := c.conn.Query(context.Background(), "SELECT usename FROM pg_catalog.pg_user")
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %+v", err)
	}
	defer rows.Close()

	users := []string{}

	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			return nil, fmt.Errorf("failed to decode database user: %+v", err)
		}

		users = append(users, user)
	}

	return users, nil
}

func (c *Client) CreateUser(username string, password string) error {
	if password != "" {
		if _, err := c.conn.Exec(context.Background(), "CREATE USER "+sanitize(username)+" WITH PASSWORD '"+password+"'"); err != nil {
			return fmt.Errorf("failed to create database user: %+v", err)
		}
	} else {
		if _, err := c.conn.Exec(context.Background(), "CREATE USER "+sanitize(username)); err != nil {
			return fmt.Errorf("failed to create database user: %+v", err)
		}
	}

	return nil
}

func (c *Client) DropUser(username string) error {
	if _, err := c.conn.Exec(context.Background(), "DROP OWNED BY "+sanitize(username)+" CASCADE"); err != nil {
		return fmt.Errorf("failed to revoke user permissions: %+v", err)
	}

	if _, err := c.conn.Exec(context.Background(), "DROP USER "+sanitize(username)); err != nil {
		return fmt.Errorf("failed to drop database user: %+v", err)
	}

	return nil
}

func (c *Client) ListDatabases() ([]string, error) {
	rows, err := c.conn.Query(context.Background(), "SELECT datname FROM pg_catalog.pg_database")
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %+v", err)
	}
	defer rows.Close()

	databases := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to decode database: %+v", err)
		}

		databases = append(databases, name)
	}

	return databases, nil
}

func sanitize(name string) string {
	return pgx.Identifier.Sanitize([]string{name})
}

func (c *Client) CreateDatabase(database string) error {
	if _, err := c.conn.Exec(context.Background(), "CREATE DATABASE "+sanitize(database)); err != nil {
		return fmt.Errorf("failed to create database: %+v", err)
	}

	return nil
}

func (c *Client) DropDatabase(database string) error {
	if _, err := c.conn.Exec(context.Background(), "DROP DATABASE "+sanitize(database)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("failed to drop database: %+v", err)
	}

	return nil
}

func (c *Client) SetOwner(database string, user string) error {
	if _, err := c.conn.Exec(context.Background(), "ALTER DATABASE "+sanitize(database)+" OWNER TO "+sanitize(user)); err != nil {
		return fmt.Errorf("failed to set database owner: %+v", err)
	}

	return nil
}

func (c *Client) GetOwner(database string) (string, error) {
	rows, err := c.conn.Query(context.Background(), "SELECT B.usename FROM pg_database A INNER JOIN pg_user B ON A.datdba = B.usesysid WHERE A.datname = $1", database)
	if err != nil {
		return "", fmt.Errorf("failed to get database owner: %+v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return "", fmt.Errorf("database not found")
	}

	var owner string
	if err := rows.Scan(&owner); err != nil {
		return "", fmt.Errorf("failed to decode database owner: %+v", err)
	}

	return owner, nil
}

/*
ListPermitted returns a list of users with read and write permissions on the database.

Example:

	readers, writers, err := client.ListPermitted("mydb")
*/
func (c *Client) ListPermitted(database string) ([]string, []string, error) {
	rows, err := c.conn.Query(context.Background(), "SELECT defaclacl FROM pg_default_acl WHERE defaclobjtype = 'r'")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list permissions: %+v", err)
	}
	defer rows.Close()

	readersMap := map[string]struct{}{}
	writersMap := map[string]struct{}{}

	for rows.Next() {
		var acl string
		if err := rows.Scan(&acl); err != nil {
			return nil, nil, fmt.Errorf("failed to decode user permission: %+v", err)
		}

		permission, err := parseACL(acl)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse user permission: %+v", err)
		}

		if permission.Write {
			writersMap[permission.For] = struct{}{}
		} else {
			readersMap[permission.For] = struct{}{}
		}
	}

	writers := make([]string, 0, len(writersMap))
	for writer := range writersMap {
		writers = append(writers, writer)
	}

	readers := make([]string, 0, len(readersMap))
	for reader := range readersMap {
		readers = append(readers, reader)
	}

	return readers, writers, nil
}

var readPermissions = []string{"SELECT"}
var writePermissions = []string{"INSERT", "UPDATE", "DELETE"}

func (c *Client) GrantPermissions(username string, owner string, writer bool) error {
	permissions := readPermissions[:]
	if writer {
		permissions = append(permissions, writePermissions...)
	}

	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR USER %s IN SCHEMA public GRANT %s ON TABLES TO %s", sanitize(owner), strings.Join(permissions, ", "), sanitize(username))); err != nil {
		return fmt.Errorf("failed to grant default table permissions: %+v", err)
	}

	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA public TO %s", strings.Join(permissions, ", "), sanitize(username))); err != nil {
		return fmt.Errorf("failed to grant existing table permissions: %+v", err)
	}

	permissions = readPermissions[:]
	if writer {
		permissions = append(permissions, "UPDATE")
	}

	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR USER %s IN SCHEMA public GRANT %s ON SEQUENCES TO %s", sanitize(owner), strings.Join(permissions, ", "), sanitize(username))); err != nil {
		return fmt.Errorf("failed to grant default sequence permissions: %+v", err)
	}

	return nil
}

func (c *Client) RevokePermissions(username string, owner string) error {
	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR USER %s IN SCHEMA public REVOKE ALL ON TABLES FROM %s", sanitize(owner), sanitize(username))); err != nil {
		return fmt.Errorf("failed to revoke default table permissions: %+v", err)
	}

	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR USER %s IN SCHEMA public REVOKE ALL ON SEQUENCES FROM %s", sanitize(owner), sanitize(username))); err != nil {
		return fmt.Errorf("failed to revoke default sequence permissions: %+v", err)
	}

	if _, err := c.conn.Exec(context.Background(), fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA public FROM %s", sanitize(username))); err != nil {
		return fmt.Errorf("failed to revoke existing table permissions: %+v", err)
	}

	return nil
}
