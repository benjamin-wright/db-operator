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
		log.Debug().Msg("Updating postgres database state")
		m.refreshDatabaseState()
		log.Debug().Msg("Processing postgres databases started")
		m.processPostgresClients()
		log.Debug().Msg("Processing postgres databases finished")
	}
}

func (m *Manager) refreshDatabaseState() {
	m.state.ClearRemote()

	for _, cluster := range m.state.GetActiveClusters() {
		globalCli, err := database.New(cluster.Name, cluster.Namespace, "postgres", "")
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create client for database %s/%s", cluster.Namespace, cluster.Name)
			continue
		}
		defer globalCli.Stop()

		users, err := globalCli.ListUsers()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list users in %s/%s", cluster.Namespace, cluster.Name)
			continue
		}
		m.state.Apply(k8s_generic.Update[database.User]{ToAdd: users})

		dbs, err := globalCli.ListDBs()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to list databases in %s/%s", cluster.Namespace, cluster.Name)
			continue
		}
		m.state.Apply(k8s_generic.Update[database.Database]{ToAdd: dbs})

		for _, db := range dbs {
			cli, err := database.New(db.Cluster.Name, db.Cluster.Namespace, "postgres", db.Name)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to create client for database %s/%s", db.Cluster.Namespace, db.Cluster.Name)
				continue
			}
			defer cli.Stop()

			permissions, err := cli.ListPermitted(db)
			if err != nil {
				log.Error().Err(err).Msgf("Failed to list permissions in %s/%s/%s", db.Cluster.Namespace, db.Cluster.Name, db.Name)
				continue
			}
			m.state.Apply(k8s_generic.Update[database.Permission]{ToAdd: permissions})
		}
	}
}

func (m *Manager) processPostgresClients() {
	dbDemand, userDemand, permsDemand, secretsDemand := m.state.GetDemand()

	clusters := newConsolidator()
	for _, db := range dbDemand.ToAdd.List() {
		if db.Target.Cluster.Name == "" {
			log.Error().Msgf("Database to add %s has no cluster", db.Target.Name)
			continue
		}
		clusters.add(db.Target.Cluster.Name, db.Target.Cluster.Namespace)
	}
	for _, db := range dbDemand.ToRemove.List() {
		if db.Cluster.Name == "" {
			log.Error().Msgf("Database to remove %s has no cluster", db.Name)
			continue
		}
		clusters.add(db.Cluster.Name, db.Cluster.Namespace)
	}
	for _, user := range userDemand.ToAdd.List() {
		if user.Target.Cluster.Name == "" {
			log.Error().Msgf("User to add %s has no cluster", user.Target.Name)
			continue
		}
		clusters.add(user.Target.Cluster.Name, user.Target.Cluster.Namespace)
	}
	for _, user := range userDemand.ToRemove.List() {
		if user.Cluster.Name == "" {
			log.Error().Msgf("User to remove %s has no cluster", user.Name)
			continue
		}
		clusters.add(user.Cluster.Name, user.Cluster.Namespace)
	}
	for _, perm := range permsDemand.ToAdd.List() {
		if perm.Target.Cluster.Name == "" {
			log.Error().Msgf("Permission to add %s has no cluster", perm.Target.User)
			continue
		}
		clusters.add(perm.Target.Cluster.Name, perm.Target.Cluster.Namespace)
	}
	for _, perm := range permsDemand.ToRemove.List() {
		if perm.Cluster.Name == "" {
			log.Error().Msgf("Permission to remove %s has no cluster", perm.User)
			continue
		}
		clusters.add(perm.Cluster.Name, perm.Cluster.Namespace)
	}

	for _, secret := range secretsDemand.ToRemove.List() {
		err := m.client.Secrets().Delete(m.ctx, secret.Name, secret.Namespace)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to delete secret %s", secret.Name)
		}
	}

	for _, namespace := range clusters.getNamespaces() {
		for _, cluster := range clusters.getNames(namespace) {
			log.Debug().Msgf("Processing cluster %s/%s", namespace, cluster)

			cli, err := database.New(cluster, namespace, "postgres", "")
			if err != nil {
				log.Error().Err(err).Msgf("Failed to create database client for %s", cluster)
				continue
			}
			defer cli.Stop()

			for _, perm := range permsDemand.ToRemove.List() {
				if perm.Cluster.Name != cluster || perm.Cluster.Namespace != namespace {
					continue
				}

				cli, err := database.New(perm.Cluster.Name, perm.Cluster.Namespace, "postgres", perm.Database)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create database client for %s/%s", perm.Cluster.Namespace, perm.Cluster.Name)
					continue
				}
				defer cli.Stop()

				err = cli.RevokePermission(perm)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to revoke permission for user %s in database %s of db %s", perm.User, perm.Database, perm.Cluster)
				}
			}

			for _, toRemove := range dbDemand.ToRemove.List() {
				if toRemove.Cluster.Name != cluster || toRemove.Cluster.Namespace != namespace {
					continue
				}

				err = cli.DeleteDB(toRemove)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete database %s in db %s", toRemove.Name, toRemove.Cluster)
				}
			}

			for _, user := range userDemand.ToRemove.List() {
				if user.Cluster.Name != cluster || user.Cluster.Namespace != namespace {
					continue
				}

				err = cli.DeleteUser(user)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to delete user %s in db %s", user.Name, user.Cluster)
				}
			}

			for _, user := range userDemand.ToAdd.List() {
				if user.Target.Cluster.Name != cluster || user.Target.Cluster.Namespace != namespace {
					continue
				}

				err := cli.CreateUser(user.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create user %s in db %s", user.Target.Name, user.Target.Cluster)
				}
			}

			for _, toAdd := range dbDemand.ToAdd.List() {
				if toAdd.Target.Cluster.Name != cluster || toAdd.Target.Cluster.Namespace != namespace {
					continue
				}

				err := cli.CreateDB(toAdd.Target)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create database %s in db %s", toAdd.Target.Name, toAdd.Target.Cluster)
				}
			}

			for _, perm := range permsDemand.ToAdd.List() {
				perm := perm.Target

				if perm.Cluster.Name != cluster || perm.Cluster.Namespace != namespace {
					continue
				}

				cli, err := database.New(perm.Cluster.Name, perm.Cluster.Namespace, "postgres", perm.Database)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to create database client for %s/%s", perm.Cluster.Namespace, perm.Cluster.Name)
					continue
				}
				defer cli.Stop()

				err = cli.GrantPermission(perm)
				if err != nil {
					log.Error().Err(err).Msgf("Failed to grant permission for user %s in database %s of db %s", perm.User, perm.Database, perm.Cluster)
				}
			}
		}
	}

	for _, secret := range secretsDemand.ToAdd.List() {
		err := m.client.Secrets().Create(m.ctx, secret.Target)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create secret %s", secret.Target.Name)
		}
	}
}
