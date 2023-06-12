package database

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/cockroach/database"
	"github.com/benjamin-wright/db-operator/internal/cockroach/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/utils"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"go.uber.org/zap"
)

type Manager struct {
	namespace string
	ctx       context.Context
	cancel    context.CancelFunc
	client    *k8s.Client
	updates   chan any
	state     State
	debouncer utils.Debouncer
}

type WatchFunc func(context.Context, context.CancelFunc, chan<- any) error

func New(
	namespace string,
	debounce time.Duration,
) (*Manager, error) {
	client, err := k8s.New(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan any)

	for _, f := range []WatchFunc{
		client.Clients().Watch,
		client.StatefulSets().Watch,
		client.Secrets().Watch,
		client.Migrations().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %w", err)
		}
	}

	state := State{
		clients:      state.NewBucket[k8s.CockroachClient](),
		statefulSets: state.NewBucket[k8s.CockroachStatefulSet](),
		secrets:      state.NewBucket[k8s.CockroachSecret](),
		migrations:   state.NewBucket[k8s.CockroachMigration](),
		databases:    state.NewBucket[database.Database](),
		users:        state.NewBucket[database.User](),
		permissions:  state.NewBucket[database.Permission](),
		applied:      state.NewBucket[database.Migration](),
	}

	return &Manager{
		namespace: namespace,
		ctx:       ctx,
		cancel:    cancel,
		client:    client,
		updates:   updates,
		state:     state,
		debouncer: utils.NewDebouncer(debounce),
	}, nil
}

func (m *Manager) Stop() {
	m.cancel()
}

func (m *Manager) Start() {
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				zap.S().Infof("context cancelled, exiting manager loop")
				return
			default:
				m.refresh()
			}
		}
	}()
}

func (m *Manager) refresh() {
	select {
	case <-m.ctx.Done():
	case update := <-m.updates:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case <-m.debouncer.Wait():
		zap.S().Infof("Database state update")
		m.refreshDatabaseState()
		zap.S().Infof("Processing Started")
		m.processCockroachClients()
		m.processCockroachMigrations()
		zap.S().Infof("Processing Done")
	}
}

func (m *Manager) refreshDatabaseState() {
	m.state.ClearRemote()

	for _, client := range m.state.GetActiveClients() {
		cli, err := database.New(client.Deployment, m.namespace)
		if err != nil {
			zap.S().Errorf("Failed to create client for database %s: %w", client.Deployment, err)
			continue
		}
		defer cli.Stop()

		users, err := cli.ListUsers()
		if err != nil {
			zap.S().Errorf("Failed to list users in %s: %w", client.Database, err)
			continue
		}

		m.state.Apply(k8s_generic.Update[database.User]{ToAdd: users})

		names, err := cli.ListDBs()
		if err != nil {
			zap.S().Errorf("Failed to list databases in %s: %w", client.Database, err)
			continue
		}

		for _, db := range names {
			m.state.Apply(k8s_generic.Update[database.Database]{ToAdd: []database.Database{db}})

			permissions, err := cli.ListPermitted(db)
			if err != nil {
				zap.S().Errorf("Failed to list permissions in %s: %w", db.Name, err)
				continue
			}

			m.state.Apply(k8s_generic.Update[database.Permission]{ToAdd: permissions})

			mClient, err := database.NewMigrations(db.DB, m.namespace, db.Name)
			if err != nil {
				zap.S().Errorf("Failed to get migration client in %s: %w", db.Name, err)
				continue
			}
			defer mClient.Stop()

			if ok, err := mClient.HasMigrationsTable(); err != nil {
				zap.S().Errorf("Failed to check for existing migrations table in %s: %w", db.Name, err)
				continue
			} else if !ok {
				err = mClient.CreateMigrationsTable()
				if err != nil {
					zap.S().Errorf("Failed to get create migrations table %s: %w", db.Name, err)
					continue
				}
			}

			migrations, err := mClient.AppliedMigrations()
			if err != nil {
				zap.S().Errorf("Failed to get migrations for %s: %w", db.Name, err)
				continue
			}

			m.state.Apply(k8s_generic.Update[database.Migration]{ToAdd: migrations})
		}
	}
}

func (m *Manager) processCockroachClients() {
	dbDemand := m.state.GetDBDemand()
	userDemand := m.state.GetUserDemand()
	permsDemand := m.state.GetPermissionDemand()
	secretsDemand := m.state.GetSecretsDemand()

	dbs := map[string]struct{}{}
	for _, db := range dbDemand.ToAdd {
		dbs[db.Target.DB] = struct{}{}
	}
	for _, db := range dbDemand.ToRemove {
		dbs[db.Target.DB] = struct{}{}
	}
	for _, user := range userDemand.ToAdd {
		dbs[user.Target.DB] = struct{}{}
	}
	for _, user := range userDemand.ToRemove {
		dbs[user.Target.DB] = struct{}{}
	}
	for _, perm := range permsDemand.ToAdd {
		dbs[perm.Target.DB] = struct{}{}
	}
	for _, perm := range permsDemand.ToRemove {
		dbs[perm.Target.DB] = struct{}{}
	}

	for _, secret := range secretsDemand.ToRemove {
		zap.S().Infof("Removing secret %s", secret.Target.Name)
		err := m.client.Secrets().Delete(m.ctx, secret.Target.Name)
		if err != nil {
			zap.S().Errorf("Failed to delete secret %s: %w", secret.Target.Name, err)
		}
	}

	for db := range dbs {
		cli, err := database.New(db, m.namespace)
		if err != nil {
			zap.S().Errorf("Failed to create database client for %s: %w", db, err)
			continue
		}
		defer cli.Stop()

		for _, perm := range permsDemand.ToRemove {
			if perm.Target.DB != db {
				continue
			}

			zap.S().Infof("Dropping permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
			err = cli.RevokePermission(perm.Target)
			if err != nil {
				zap.S().Errorf("Failed to revoke permission: %w", err)
			}
		}

		for _, toRemove := range dbDemand.ToRemove {
			if toRemove.Target.DB != db {
				continue
			}

			zap.S().Infof("Dropping database %s in db %s", toRemove.Target.Name, toRemove.Target.DB)
			err = cli.DeleteDB(toRemove.Target)
			if err != nil {
				zap.S().Errorf("Failed to delete database: %w", err)
			}
		}

		for _, user := range userDemand.ToRemove {
			if user.Target.DB != db {
				continue
			}

			zap.S().Infof("Dropping user %s in db %s", user.Target.Name, user.Target.DB)
			err = cli.DeleteUser(user.Target)
			if err != nil {
				zap.S().Errorf("Failed to delete user: %w", err)
			}
		}

		for _, toAdd := range dbDemand.ToAdd {
			if toAdd.Target.DB != db {
				continue
			}

			zap.S().Infof("Creating database %s in db %s", toAdd.Target.Name, toAdd.Target.DB)

			err := cli.CreateDB(toAdd.Target)
			if err != nil {
				zap.S().Errorf("Failed to create database: %w", err)
			}
		}

		for _, user := range userDemand.ToAdd {
			if user.Target.DB != db {
				continue
			}

			zap.S().Infof("Creating user %s in db %s", user.Target.Name, user.Target.DB)

			err := cli.CreateUser(user.Target)
			if err != nil {
				zap.S().Errorf("Failed to create user: %w", err)
			}
		}

		for _, perm := range permsDemand.ToAdd {
			if perm.Target.DB != db {
				continue
			}

			zap.S().Infof("Adding permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
			err := cli.GrantPermission(perm.Target)
			if err != nil {
				zap.S().Errorf("Failed to grant permission: %w", err)
			}
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		zap.S().Infof("Adding secret %s", secret.Target.Name)
		err := m.client.Secrets().Create(m.ctx, secret.Target)
		if err != nil {
			zap.S().Errorf("Failed to create secret %s: %w", secret.Target.Name, err)
		}
	}
}

func (m *Manager) processCockroachMigrations() {
	demand := m.state.GetMigrationsDemand()

	for _, deployment := range demand.GetDBs() {
		for _, db := range demand.GetDatabases(deployment) {
			if !demand.Next(deployment, db) {
				continue
			}

			client, err := database.NewMigrations(deployment, m.namespace, db)
			if err != nil {
				zap.S().Errorf("Failed to create migrations client: %w", err)
				continue
			}
			defer client.Stop()

			if ok, err := client.HasMigrationsTable(); err != nil {
				zap.S().Errorf("Failed to check for existing migrations table in %s: %w", db, err)
				continue
			} else if !ok {
				err = client.CreateMigrationsTable()
				if err != nil {
					zap.S().Errorf("Failed to get create migrations table %s: %w", db, err)
					continue
				}
			}

			for demand.Next(deployment, db) {
				migration, index := demand.GetNextMigration(deployment, db)

				zap.S().Infof("Running migration %s [%s] %d", deployment, db, index)

				err := client.RunMigration(index, migration)
				if err != nil {
					zap.S().Errorf("Failed to run migration %d: %w", index, err)
					break
				}
			}
		}
	}
}
