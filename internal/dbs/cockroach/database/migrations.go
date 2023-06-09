package database

import (
	"context"
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/postgres"
	"github.com/jackc/pgx/v4"
	"github.com/rs/zerolog/log"
)

type MigrationsClient struct {
	conn       *pgx.Conn
	deployment string
	namespace  string
	database   string
}

func NewMigrations(deployment string, namespace string, database string) (*MigrationsClient, error) {
	cfg := postgres.ConnectConfig{
		Host:     fmt.Sprintf("%s.%s.svc.cluster.local", deployment, namespace),
		Port:     26257,
		Username: "root",
		Database: database,
	}

	conn, err := postgres.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cockroach db at %s: %+v", database, err)
	}

	return &MigrationsClient{
		conn:       conn,
		deployment: deployment,
		namespace:  namespace,
		database:   database,
	}, nil
}

func (c *MigrationsClient) Stop() {
	log.Info().Msgf("Closing connection to DB %s[%s]", c.deployment, c.database)
	c.conn.Close(context.TODO())
}

func (c *MigrationsClient) HasMigrationsTable() (bool, error) {
	rows, err := c.conn.Query(context.TODO(), "SELECT DISTINCT(tablename) FROM pg_catalog.pg_tables WHERE tablename = $1", "migrations")
	if err != nil {
		return false, fmt.Errorf("failed to check for migrations: %+v", err)
	}
	defer rows.Close()

	return rows.Next(), nil
}

func (d *MigrationsClient) CreateMigrationsTable() error {
	_, err := d.conn.Exec(
		context.TODO(),
		`
			CREATE TABLE migrations (
				id INT PRIMARY KEY NOT NULL UNIQUE
			);
		`,
	)

	return err
}

func (c *MigrationsClient) AppliedMigrations() ([]Migration, error) {
	rows, err := c.conn.Query(context.TODO(), "SELECT id FROM migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to get migration ids: %+v", err)
	}
	defer rows.Close()

	migrations := []Migration{}

	for rows.Next() {
		var id int64
		err = rows.Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("failed to parse database response: %+v", err)
		}

		migrations = append(migrations, Migration{
			DB: DBRef{
				Name:      c.deployment,
				Namespace: c.namespace,
			},
			Database: c.database,
			Index:    id,
		})
	}

	return migrations, nil
}

func (c *MigrationsClient) RunMigration(index int64, query string) error {
	_, err := c.conn.Exec(context.TODO(), query)
	if err != nil {
		return fmt.Errorf("failed to run migration: %+v", err)
	}

	_, err = c.conn.Exec(context.TODO(), "INSERT INTO migrations (id) VALUES ($1)", index)
	if err != nil {
		return fmt.Errorf("failed to update migrations table: %+v", err)
	}

	return nil
}
