package database

import (
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/admin"
	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Client struct {
	conn      *admin.Client
	cluster   string
	namespace string
	database  string
	logger    zerolog.Logger
}

func New(cluster string, namespace string, password string, database string) (*Client, error) {
	cfg := config.Config{
		Host:     fmt.Sprintf("%s.%s.svc.cluster.local", cluster, namespace),
		Port:     5432,
		Username: "postgres",
		Password: password,
		Database: database,
		Retry:    false,
	}

	logger := log.With().
		Str("kind", "postgres").
		Str("cluster", cluster).
		Str("namespace", namespace).
		Str("database", database).
		Logger()

	if database != "" {
		logger.Debug().Msgf("Opening connection to Postgres Database %s:%s", cluster, database)
	} else {
		logger.Debug().Msgf("Opening connection to Postgres Cluster %s", cluster)
	}

	conn, err := admin.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres db at %s: %+v", cluster, err)
	}

	return &Client{
		conn:      conn,
		cluster:   cluster,
		namespace: namespace,
		database:  database,
		logger:    logger,
	}, nil
}

func (c *Client) Stop() {
	if c.database != "" {
		c.logger.Debug().Msgf("Closing connection to Postgres Database %s:%s", c.cluster, c.database)
	} else {
		c.logger.Debug().Msgf("Closing connection to Postgres Cluster %s", c.cluster)
	}

	c.conn.Stop()
}

func isReservedDB(name string) bool {
	return name == "system" || name == "postgres" || strings.HasPrefix(name, "template")
}

func (c *Client) ListDBs() ([]Database, error) {
	names, err := c.conn.ListDatabases()
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %+v", err)
	}

	databases := []Database{}
	for _, name := range names {
		if isReservedDB(name) {
			continue
		}

		owner, err := c.conn.GetOwner(name)
		if err != nil {
			return nil, fmt.Errorf("failed to get owner of database %s: %+v", name, err)
		}

		databases = append(databases, Database{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Name:  name,
			Owner: owner,
		})
	}

	return databases, nil
}

func (c *Client) SetOwner(db Database) error {
	c.logger.Info().Msgf("Setting owner of database %s to %s", db.Name, db.Owner)

	err := c.conn.SetOwner(db.Name, db.Owner)
	if err != nil {
		return fmt.Errorf("failed to set owner of database %s to %s: %+v", db.Name, db.Owner, err)
	}

	return nil
}

func (c *Client) CreateDB(db Database) error {
	c.logger.Info().Msgf("Creating database %s", db.Name)

	err := c.conn.CreateDatabase(db.Name)
	if err != nil {
		return fmt.Errorf("failed to create database %s: %+v", db.Name, err)
	}

	err = c.conn.SetOwner(db.Name, db.Owner)
	if err != nil {
		return fmt.Errorf("failed to set owner of database %s to %s: %+v", db.Name, db.Owner, err)
	}

	return nil
}

func (c *Client) DeleteDB(db Database) error {
	c.logger.Info().Msgf("Deleting database %s", db.Name)
	err := c.conn.DropDatabase(db.Name)
	if err != nil {
		return fmt.Errorf("failed to delete database %s: %+v", db.Name, err)
	}

	return nil
}

func isReservedUser(name string) bool {
	return name == "" || name == "postgres" || name == "root"
}

func (c *Client) ListUsers() ([]User, error) {
	names, err := c.conn.ListUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %+v", err)
	}

	users := []User{}
	for _, name := range names {
		if isReservedUser(name) {
			continue
		}

		users = append(users, User{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Name: name,
		})
	}

	return users, nil
}

func (c *Client) CreateUser(user User) error {
	c.logger.Info().Msgf("Creating user %s", user.Name)
	err := c.conn.CreateUser(user.Name, user.Password)
	if err != nil {
		return fmt.Errorf("failed to create user %s: %+v", user, err)
	}

	return nil
}

func (c *Client) DeleteUser(user User) error {
	c.logger.Info().Msgf("Deleting user %s", user.Name)
	err := c.conn.DropUser(user.Name)
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %+v", user, err)
	}

	return nil
}

func (c *Client) ListPermitted(db string) ([]Permission, error) {
	permissions := []Permission{}
	readers, writers, err := c.conn.ListPermitted(db)
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions: %+v", err)
	}

	for _, user := range readers {
		if isReservedUser(user) {
			continue
		}

		permissions = append(permissions, Permission{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Database: db,
			User:     user,
		})
	}

	for _, user := range writers {
		if isReservedUser(user) {
			continue
		}

		permissions = append(permissions, Permission{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Database: db,
			User:     user,
			Write:    true,
		})
	}

	return permissions, nil
}

func (c *Client) GrantPermission(permission Permission) error {
	owner, err := c.conn.GetOwner(permission.Database)
	if err != nil {
		return fmt.Errorf("failed to get owner of database %s: %+v", permission.Database, err)
	}

	c.logger.Info().Msgf("Granting '%s' permission to read/write to '%s'", permission.User, permission.Database)
	err = c.conn.GrantPermissions(permission.User, owner, permission.Write)
	if err != nil {
		return fmt.Errorf("failed to grant permission: %+v", err)
	}

	return nil
}

func (c *Client) RevokePermission(permission Permission) error {
	owner, err := c.conn.GetOwner(permission.Database)
	if err != nil {
		return fmt.Errorf("failed to get owner of database %s: %+v", permission.Database, err)
	}

	c.logger.Info().Msgf("Revoking '%s' permission to read/write to '%s'", permission.User, permission.Database)
	err = c.conn.RevokePermissions(permission.User, owner)
	if err != nil {
		return fmt.Errorf("failed to revoke permission: %+v", err)
	}

	return nil
}
