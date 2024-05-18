package database

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/internal/state"
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
		return nil, fmt.Errorf("failed to create postgres client: %+v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan any)

	for _, f := range []WatchFunc{
		client.Clients().Watch,
		client.StatefulSets().Watch,
		client.Secrets().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		clients:      bucket.NewBucket[clients.Resource](),
		statefulSets: bucket.NewBucket[stateful_sets.Resource](),
		secrets:      bucket.NewBucket[secrets.Resource](),
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
		log.Info().Msg("Updating postgres database state")
		m.refreshDatabaseState()
		log.Info().Msg("Processing postgres databases started")
		m.processPostgresClients()
		log.Info().Msg("Processing postgres databases finished")
	}
}

func (m *Manager) refreshDatabaseState() {
	m.state.ClearRemote()

	for _, client := range m.state.GetActiveClients() {
		cli, err := database.New(client.Cluster.Name, client.Cluster.Namespace)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create client for database %s/%s", client.Cluster.Namespace, client.Cluster.Name)
			continue
		}
		defer cli.Stop()

		users, err := cli.ListUsers()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list users in %s/%s", client.Cluster.Namespace, client.Cluster.Name)
			continue
		}

		m.state.Apply(k8s_generic.Update[database.User]{ToAdd: users})

		names, err := cli.ListDBs()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list databases in %s/%s", client.Cluster.Namespace, client.Cluster.Name)
			continue
		}

		for _, db := range names {
			m.state.Apply(k8s_generic.Update[database.Database]{ToAdd: []database.Database{db}})

			permissions, err := cli.ListPermitted(db)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to list permissions in %s/%s/%s", client.Cluster.Namespace, client.Cluster.Name, db.Name)
				continue
			}

			m.state.Apply(k8s_generic.Update[database.Permission]{ToAdd: permissions})

			// mClient, err := database.NewMigrations(db.Cluster.Name, db.Cluster.Namespace, db.Name)
			// if err != nil {
			// 	log.Error().Err(err).Msgf("Failed to get migration client in %s/%s/%s", client.Cluster.Namespace, client.Cluster.Name, db.Name)
			// 	continue
			// }
			// defer mClient.Stop()

			// if ok, err := mClient.HasMigrationsTable(); err != nil {
			// 	log.Error().Err(err).Msgf("Failed to check for migrations table in %s/%s/%s", client.Cluster.Namespace, client.Cluster.Name, db.Name)
			// 	continue
			// } else if !ok {
			// 	err = mClient.CreateMigrationsTable()
			// 	if err != nil {
			// 		log.Error().Err(err).Msgf("Failed to create migrations table in %s/%s/%s", client.Cluster.Namespace, client.Cluster.Name, db.Name)
			// 		continue
			// 	}
			// }

			// migrations, err := mClient.AppliedMigrations()
			// if err != nil {
			// 	log.Error().Err(err).Msgf("Failed to get applied migrations in %s/%s/%s", client.Cluster.Namespace, client.Cluster.Name, db.Name)
			// 	continue
			// }

			// m.state.Apply(k8s_generic.Update[database.Migration]{ToAdd: migrations})
		}
	}
}

func isUserSecret(user database.User, secret secrets.Resource) bool {
	return user.Cluster.Name == secret.Cluster.Name && user.Cluster.Namespace == secret.Cluster.Namespace && user.Name == secret.User
}

func setPasswords(
	secretsDemand *state.Demand[clients.Resource, secrets.Resource],
	userDemand *state.Demand[clients.Resource, database.User],
) {
	secretIds := []int{}
	userIds := []int{}
	for secretId, secret := range secretsDemand.ToAdd {
		missing := true
		for userId, user := range userDemand.ToAdd {
			if isUserSecret(user.Target, secret.Target) {
				password, err := utils.GeneratePassword(32, true, true)
				if err != nil {
					// remove the secret and user from the ToAdd list
					secretIds = append(secretIds, secretId)
					userIds = append(userIds, userId)
					log.Error().Err(err).Msgf("Failed to generate password for user %s in db %s", user.Target.Name, user.Target.Cluster)
					continue
				}

				userDemand.ToAdd[userId].Target.Password = password
				secretsDemand.ToAdd[secretId].Target.Password = password
				missing = false
				break
			}
		}

		if missing {
			// remove the secret from the ToAdd list
			secretIds = append(secretIds, secretId)
			log.Error().Msgf("Failed to find user for secret %s", secret.Target.Name)
		}
	}

	for _, secretId := range secretIds {
		secretsDemand.ToAdd = append(secretsDemand.ToAdd[:secretId], secretsDemand.ToAdd[secretId+1:]...)
	}

	for _, userId := range userIds {
		userDemand.ToAdd = append(userDemand.ToAdd[:userId], userDemand.ToAdd[userId+1:]...)
	}
}

func (m *Manager) processPostgresClients() {
	dbDemand := m.state.GetDBDemand()
	userDemand := m.state.GetUserDemand()
	permsDemand := m.state.GetPermissionDemand()
	secretsDemand := m.state.GetSecretsDemand()

	setPasswords(&secretsDemand, &userDemand)

	dbs := newConsolidator()
	for _, db := range dbDemand.ToAdd {
		dbs.add(db.Target.Cluster.Name, db.Target.Cluster.Namespace)
	}
	for _, db := range dbDemand.ToRemove {
		dbs.add(db.Target.Cluster.Name, db.Target.Cluster.Namespace)
	}
	for _, user := range userDemand.ToAdd {
		dbs.add(user.Target.Cluster.Name, user.Target.Cluster.Namespace)
	}
	for _, user := range userDemand.ToRemove {
		dbs.add(user.Target.Cluster.Name, user.Target.Cluster.Namespace)
	}
	for _, perm := range permsDemand.ToAdd {
		dbs.add(perm.Target.Cluster.Name, perm.Target.Cluster.Namespace)
	}
	for _, perm := range permsDemand.ToRemove {
		dbs.add(perm.Target.Cluster.Name, perm.Target.Cluster.Namespace)
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
				if perm.Target.Cluster.Name != db || perm.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.Cluster)
				err = cli.RevokePermission(perm.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to revoke permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.Cluster)
				}
			}

			for _, toRemove := range dbDemand.ToRemove {
				if toRemove.Target.Cluster.Name != db || toRemove.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping database %s in db %s", toRemove.Target.Name, toRemove.Target.Cluster)
				err = cli.DeleteDB(toRemove.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete database %s in db %s", toRemove.Target.Name, toRemove.Target.Cluster)
				}
			}

			for _, user := range userDemand.ToRemove {
				if user.Target.Cluster.Name != db || user.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Dropping user %s in db %s", user.Target.Name, user.Target.Cluster)
				err = cli.DeleteUser(user.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete user %s in db %s", user.Target.Name, user.Target.Cluster)
				}
			}

			for _, toAdd := range dbDemand.ToAdd {
				if toAdd.Target.Cluster.Name != db || toAdd.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Creating database %s in db %s", toAdd.Target.Name, toAdd.Target.Cluster)
				err := cli.CreateDB(toAdd.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create database %s in db %s", toAdd.Target.Name, toAdd.Target.Cluster)
				}
			}

			for _, user := range userDemand.ToAdd {
				if user.Target.Cluster.Name != db || user.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Creating user %s in db %s", user.Target.Name, user.Target.Cluster)

				err := cli.CreateUser(user.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create user %s in db %s", user.Target.Name, user.Target.Cluster)
				}
			}

			for _, perm := range permsDemand.ToAdd {
				if perm.Target.Cluster.Name != db || perm.Target.Cluster.Namespace != namespace {
					continue
				}

				log.Info().Msgf("Adding permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.Cluster)
				err := cli.GrantPermission(perm.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to grant permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.Cluster)
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
