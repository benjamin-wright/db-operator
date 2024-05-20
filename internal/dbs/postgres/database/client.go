package database

import (
	"fmt"
	"strings"

	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/rs/zerolog/log"
)

type Client struct {
	conn      *postgres.AdminConn
	cluster   string
	namespace string
}

func New(cluster string, namespace string, password string, database string) (*Client, error) {
	cfg := postgres.ConnectConfig{
		Host:     fmt.Sprintf("%s.%s.svc.cluster.local", cluster, namespace),
		Port:     26257,
		Username: "postgres",
		Password: password,
		Database: database,
	}

	conn, err := postgres.NewAdminConn(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres db at %s: %+v", cluster, err)
	}

	return &Client{
		conn:      conn,
		cluster:   cluster,
		namespace: namespace,
	}, nil
}

func (c *Client) Stop() {
	log.Info().Msgf("Closing connection to DB %s", c.cluster)
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

		databases = append(databases, Database{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Name: name,
		})
	}

	return databases, nil
}

func (c *Client) CreateDB(db Database) error {
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
	err := c.conn.DropDatabase(db.Name)
	if err != nil {
		return fmt.Errorf("failed to create database %s: %+v", db.Name, err)
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
	err := c.conn.CreateUser(user.Name, user.Password)
	if err != nil {
		return fmt.Errorf("failed to create user %s: %+v", user, err)
	}

	return nil
}

func (c *Client) DeleteUser(user User) error {
	err := c.conn.DropUser(user.Name)
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %+v", user, err)
	}

	return nil
}

func (c *Client) ListPermitted(db Database) ([]Permission, error) {
	permissions := []Permission{}
	permitted, err := c.conn.ListPermitted(db.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions: %+v", err)
	}

	for _, user := range permitted {
		if isReservedUser(user) {
			continue
		}

		permissions = append(permissions, Permission{
			Cluster: Cluster{
				Name:      c.cluster,
				Namespace: c.namespace,
			},
			Database: db.Name,
			User:     user,
		})
	}

	return permissions, nil
}

func (c *Client) GrantPermission(permission Permission) error {
	err := c.conn.GrantPermissions(permission.User, permission.Database)
	if err != nil {
		return fmt.Errorf("failed to grant permission: %+v", err)
	}

	return nil
}

func (c *Client) RevokePermission(permission Permission) error {
	err := c.conn.RevokePermissions(permission.User, permission.Database)
	if err != nil {
		return fmt.Errorf("failed to revoke permission: %+v", err)
	}

	return nil
}
