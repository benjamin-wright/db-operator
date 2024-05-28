package migrations

import (
	"context"
	"crypto/md5"
	"fmt"
	"sort"

	"github.com/benjamin-wright/db-operator/v2/pkg/postgres/config"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

type Client struct {
	conn *pgx.Conn
	cfg  config.Config
}

func New(cfg config.Config) (*Client, error) {
	conn, err := config.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres db at %s: %+v", cfg.String(), err)
	}

	return &Client{
		conn: conn,
		cfg:  cfg,
	}, nil
}

func (c *Client) Stop() {
	log.Info().Msgf("Closing connection to DB %s[%s]", c.cfg.Host, c.cfg.Database)
	c.conn.Close(context.TODO())
}

func (c *Client) Init() error {
	exists, err := c.hasMigrationsTable()
	if err != nil {
		return err
	}

	if exists {
		log.Debug().Msg("Migrations table already exists")
		return nil
	}

	log.Debug().Msg("Creating migrations table")
	return c.createMigrationsTable()
}

func (c *Client) hasMigrationsTable() (bool, error) {
	rows, err := c.conn.Query(context.TODO(), "SELECT DISTINCT(tablename) FROM pg_catalog.pg_tables WHERE tablename = $1", "migrations")
	if err != nil {
		return false, fmt.Errorf("failed to check for migrations: %+v", err)
	}
	defer rows.Close()

	return rows.Next(), nil
}

func (d *Client) createMigrationsTable() error {
	_, err := d.conn.Exec(
		context.TODO(),
		`
			CREATE TABLE migrations (
				id INT PRIMARY KEY NOT NULL UNIQUE,
				hash CHAR(32) NOT NULL
			);
		`,
	)

	return err
}

func getHash(value string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(value)))
}

func (c *Client) Run(migrations []Migration) error {
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Index < migrations[j].Index
	})

	for _, migration := range migrations {
		hash := getHash(migration.Query)

		applied, err := c.isApplied(migration.Index, hash)
		if err != nil {
			return err
		}

		if applied {
			log.Debug().Msgf("Migration %d already applied", migration.Index)
			continue
		}

		log.Debug().Msgf("Running migration %d", migration.Index)
		err = c.runMigration(migration.Query)
		if err != nil {
			return err
		}

		err = c.setApplied(migration.Index, hash)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) isApplied(index int, hash string) (bool, error) {
	rows, err := c.conn.Query(context.TODO(), "SELECT hash FROM migrations WHERE id = $1", index)
	if err != nil {
		return false, fmt.Errorf("failed to check for migration: %+v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return false, nil
	}

	var appliedHash string
	err = rows.Scan(&appliedHash)
	if err != nil {
		return false, fmt.Errorf("failed to parse database response: %+v", err)
	}

	if appliedHash != hash {
		return false, fmt.Errorf("hash mismatch for migration %d", index)
	}

	return true, nil
}

func (c *Client) runMigration(query string) error {
	_, err := c.conn.Exec(context.TODO(), query)
	if err != nil {
		return fmt.Errorf("failed to run migration: %+v", err)
	}

	return nil
}

func (c *Client) setApplied(index int, hash string) error {
	_, err := c.conn.Exec(context.TODO(), "INSERT INTO migrations (id, hash) VALUES ($1, $2)", index, hash)
	if err != nil {
		return fmt.Errorf("failed to update migrations table: %+v", err)
	}

	return nil
}
