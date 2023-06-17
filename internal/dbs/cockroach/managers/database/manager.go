package database

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/k8s"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/internal/utils"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *k8s.Client
	updates   chan any
	state     State
	debouncer utils.Debouncer
}

type WatchFunc func(context.Context, context.CancelFunc, chan<- any) error

func New(
	debounce time.Duration,
) (*Manager, error) {
	client, err := k8s.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach client: %+v", err)
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
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		clients:      bucket.NewBucket[k8s.CockroachClient](),
		statefulSets: bucket.NewBucket[k8s.CockroachStatefulSet](),
		secrets:      bucket.NewBucket[k8s.CockroachSecret](),
		migrations:   bucket.NewBucket[k8s.CockroachMigration](),
		databases:    bucket.NewBucket[database.Database](),
		users:        bucket.NewBucket[database.User](),
		permissions:  bucket.NewBucket[database.Permission](),
		applied:      bucket.NewBucket[database.Migration](),
	}

	return &Manager{
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
				log.Info().Msg("context cancelled, exiting manager loop")
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
		log.Info().Msg("Updating cockroach database state")
		m.refreshDatabaseState()
		log.Info().Msg("Processing cockroach databases started")
		m.processCockroachClients()
		m.processCockroachMigrations()
		log.Info().Msg("Processing cockroach databases finished")
	}
}

func (m *Manager) refreshDatabaseState() {
	m.state.ClearRemote()

	for _, client := range m.state.GetActiveClients() {
		cli, err := database.New(client.DBRef.Name, client.DBRef.Namespace)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create client for database %s/%s", client.DBRef.Namespace, client.DBRef.Name)
			continue
		}
		defer cli.Stop()

		users, err := cli.ListUsers()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list users in %s/%s", client.DBRef.Namespace, client.DBRef.Name)
			continue
		}

		m.state.Apply(k8s_generic.Update[database.User]{ToAdd: users})

		names, err := cli.ListDBs()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list databases in %s/%s", client.DBRef.Namespace, client.DBRef.Name)
			continue
		}

		for _, db := range names {
			m.state.Apply(k8s_generic.Update[database.Database]{ToAdd: []database.Database{db}})

			permissions, err := cli.ListPermitted(db)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to list permissions in %s/%s/%s", client.DBRef.Namespace, client.DBRef.Name, db.Name)
				continue
			}

			m.state.Apply(k8s_generic.Update[database.Permission]{ToAdd: permissions})

			mClient, err := database.NewMigrations(db.DB.Name, db.DB.Namespace, db.Name)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to get migration client in %s/%s/%s", client.DBRef.Namespace, client.DBRef.Name, db.Name)
				continue
			}
			defer mClient.Stop()

			if ok, err := mClient.HasMigrationsTable(); err != nil {
				log.Error().Err(err).Msgf("Failed to check for migrations table in %s/%s/%s", client.DBRef.Namespace, client.DBRef.Name, db.Name)
				continue
			} else if !ok {
				err = mClient.CreateMigrationsTable()
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create migrations table in %s/%s/%s", client.DBRef.Namespace, client.DBRef.Name, db.Name)
					continue
				}
			}

			migrations, err := mClient.AppliedMigrations()
			if err != nil {
				log.Error().Err(err).Msgf("Failed to get applied migrations in %s/%s/%s", client.DBRef.Namespace, client.DBRef.Name, db.Name)
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

	dbs := newConsolidator()
	for _, db := range dbDemand.ToAdd {
		dbs.add(db.Target.DB.Name, db.Target.DB.Namespace)
	}
	for _, db := range dbDemand.ToRemove {
		dbs.add(db.Target.DB.Name, db.Target.DB.Namespace)
	}
	for _, user := range userDemand.ToAdd {
		dbs.add(user.Target.DB.Name, user.Target.DB.Namespace)
	}
	for _, user := range userDemand.ToRemove {
		dbs.add(user.Target.DB.Name, user.Target.DB.Namespace)
	}
	for _, perm := range permsDemand.ToAdd {
		dbs.add(perm.Target.DB.Name, perm.Target.DB.Namespace)
	}
	for _, perm := range permsDemand.ToRemove {
		dbs.add(perm.Target.DB.Name, perm.Target.DB.Namespace)
	}

	for _, secret := range secretsDemand.ToRemove {
		log.Info().Msgf("Removing secret %s", secret.Target.Name)
		err := m.client.Secrets().Delete(m.ctx, secret.Target.Name, secret.Target.Namespace)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to delete secret %s", secret.Target.Name)
		}
	}

	for _, namespace := range dbs.getNamespaces() {
		for _, db := range dbs.getNames(namespace) {
			cli, err := database.New(db, namespace)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to create database client for %s", db)
				continue
			}
			defer cli.Stop()

			for _, perm := range permsDemand.ToRemove {
				if perm.Target.DB.Name != db || perm.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
				err = cli.RevokePermission(perm.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to revoke permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
				}
			}

			for _, toRemove := range dbDemand.ToRemove {
				if toRemove.Target.DB.Name != db || toRemove.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping database %s in db %s", toRemove.Target.Name, toRemove.Target.DB)
				err = cli.DeleteDB(toRemove.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete database %s in db %s", toRemove.Target.Name, toRemove.Target.DB)
				}
			}

			for _, user := range userDemand.ToRemove {
				if user.Target.DB.Name != db || user.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping user %s in db %s", user.Target.Name, user.Target.DB)
				err = cli.DeleteUser(user.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete user %s in db %s", user.Target.Name, user.Target.DB)
				}
			}

			for _, toAdd := range dbDemand.ToAdd {
				if toAdd.Target.DB.Name != db || toAdd.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Creating database %s in db %s", toAdd.Target.Name, toAdd.Target.DB)
				err := cli.CreateDB(toAdd.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create database %s in db %s", toAdd.Target.Name, toAdd.Target.DB)
				}
			}

			for _, user := range userDemand.ToAdd {
				if user.Target.DB.Name != db || user.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Creating user %s in db %s", user.Target.Name, user.Target.DB)

				err := cli.CreateUser(user.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create user %s in db %s", user.Target.Name, user.Target.DB)
				}
			}

			for _, perm := range permsDemand.ToAdd {
				if perm.Target.DB.Name != db || perm.Target.DB.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Adding permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
				err := cli.GrantPermission(perm.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to grant permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
				}
			}
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		log.Info().Msgf("Adding secret %s", secret.Target.Name)
		err := m.client.Secrets().Create(m.ctx, secret.Target)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create secret %s", secret.Target.Name)
		}
	}
}

func (m *Manager) processCockroachMigrations() {
	demand := m.state.GetMigrationsDemand()

	for _, namespace := range demand.GetNamespaces() {
		for _, deployment := range demand.GetDBs(namespace) {
			for _, db := range demand.GetDatabases(namespace, deployment) {
				if !demand.Next(namespace, deployment, db) {
					continue
				}

				client, err := database.NewMigrations(deployment, namespace, db)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create migrations client for %s", db)
					continue
				}
				defer client.Stop()

				if ok, err := client.HasMigrationsTable(); err != nil {
					log.Error().Err(err).Msgf("Failed to check for existing migrations table in %s", db)
					continue
				} else if !ok {
					err = client.CreateMigrationsTable()
					if err != nil {
						log.Error().Err(err).Msgf("Failed to create migrations table in %s", db)
						continue
					}
				}

				for demand.Next(namespace, deployment, db) {
					migration, index := demand.GetNextMigration(namespace, deployment, db)

					log.Info().Msgf("Running migration %s [%s] %d", deployment, db, index)

					err := client.RunMigration(index, migration)
					if err != nil {
						log.Error().Err(err).Msgf("Failed to run migration %s [%s] %d", deployment, db, index)
						break
					}
				}
			}
		}
	}
}
