package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/manager/model"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/v2/internal/utils"
	"github.com/rs/zerolog/log"
)

type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *k8s.Client
	updates   <-chan any
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
		client.Clusters().Watch,
		client.Clients().Watch,
		client.StatefulSets().Watch,
		client.Secrets().Watch,
		client.Services().Watch,
		client.PVCs().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		clusters:     bucket.NewBucket[clusters.Resource](),
		clients:      bucket.NewBucket[clients.Resource](),
		statefulSets: bucket.NewBucket[stateful_sets.Resource](),
		secrets:      bucket.NewBucket[secrets.Resource](),
		services:     bucket.NewBucket[services.Resource](),
		pvcs:         bucket.NewBucket[pvcs.Resource](),
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
		log.Debug().Msg("Processing postgres deployments started")
		demand := model.NewModel(m.state.clusters, m.state.clients)

		err := m.clean(demand)
		if err != nil {
			log.Error().Err(err).Msg("Failed to clean postgres deployments")
			return
		}

		err = m.resolve(demand)
		if err != nil {
			log.Error().Err(err).Msg("Failed to resolve postgres deployments")
		} else {
			log.Debug().Msg("Processing postgres deployments finished")
		}
	}
}

func (m *Manager) clean(demand model.Model) error {
	for _, ss := range m.state.statefulSets.List() {
		if !demand.Owns(ss) {
			err := m.client.StatefulSets().Delete(context.TODO(), ss.Name, ss.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres statefulset")
			}
		}
	}

	for _, service := range m.state.services.List() {
		if !demand.Owns(service) {
			err := m.client.Services().Delete(context.TODO(), service.Name, service.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres service")
			}
		}
	}

	for _, secret := range m.state.secrets.List() {
		if !demand.Owns(secret) {
			err := m.client.Secrets().Delete(context.TODO(), secret.Name, secret.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres client secret")
			}
		}
	}

	return nil
}

func (m *Manager) resolve(demand model.Model) error {
	for _, cluster := range demand.Clusters {
		clusterObj, exists := m.state.clusters.Get(cluster.GetID())
		if !exists {
			return fmt.Errorf("cluster %s not found", cluster.GetID())
		}

		if !m.resolveK8s(cluster, clusterObj) {
			continue
		}

		err := m.resolveAdmin(cluster)
		if err != nil {
			return fmt.Errorf("failed to resolve admin: %+v", err)
		}

		for name := range cluster.Databases {
			err := m.resolveDatabase(cluster, name)
			if err != nil {
				return fmt.Errorf("failed to resolve database: %+v", err)
			}
		}
	}

	return nil
}

func (m *Manager) resolveK8s(cluster *model.Cluster, clusterObj clusters.Resource) bool {
	_, exists := m.state.services.Get(cluster.Service.GetID())
	if !exists {
		err := m.client.Services().Create(context.TODO(), cluster.Service)
		if err != nil {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Warning", "CreateFailed", err.Error())
		} else {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Normal", "Created", "Service created")
		}
	}

	statefulset, exists := m.state.statefulSets.Get(cluster.StatefulSet.GetID())
	if !exists {
		err := m.client.StatefulSets().Create(context.TODO(), cluster.StatefulSet)
		if err != nil {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Warning", "CreateFailed", err.Error())
		} else {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Normal", "Created", "StatefulSet created")
		}

		return false
	}

	if statefulset.Ready && !clusterObj.Ready {
		clusterObj.Ready = true
		err := m.client.Clusters().UpdateStatus(context.TODO(), clusterObj)
		if err != nil {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Warning", "StatusUpdateFailed", err.Error())
		} else {
			m.client.Clusters().Event(context.TODO(), clusterObj, "Normal", "DeploymentReady", "Deployment is ready")
		}
	}

	return statefulset.Ready
}

func (m *Manager) resolveAdmin(cluster *model.Cluster) error {
	db, err := database.New(cluster.Name, cluster.Namespace, "postgres", "")
	if err != nil {
		return fmt.Errorf("failed to create database: %+v", err)
	}
	defer db.Stop()

	existingDBs, existingUsers, err := m.getClusterState(db)
	if err != nil {
		return fmt.Errorf("failed to get db state: %+v", err)
	}

	for _, database := range existingDBs {
		if _, ok := cluster.Databases[database.Name]; !ok {
			err := db.DeleteDB(database)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete database")
			}
		}
	}

	for _, user := range existingUsers {
		if _, ok := cluster.Users[user.Name]; !ok {
			err := db.DeleteUser(user)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete user")
			}
		}
	}

	for name, data := range cluster.Users {
		client, ok := m.state.clients.Get(data.ClientID)
		if !ok {
			return fmt.Errorf("client %s not found", data.ClientID)
		}
		_, userExists := existingUsers[name]
		_, secretExists := m.state.secrets.Get(data.Secret.GetID())

		if userExists && !secretExists {
			err := db.DeleteUser(database.User{
				Name: name,
				Cluster: database.Cluster{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
				},
			})
			if err != nil {
				m.client.Clients().Event(context.TODO(), client, "Warning", "UserDeleteFailed", err.Error())
			} else {
				userExists = false
				m.client.Clients().Event(context.TODO(), client, "Normal", "UserDeleted", "Missing secret so regenerating user")
			}
		}

		if secretExists && !userExists {
			err := m.client.Secrets().Delete(context.TODO(), data.Secret.Name, data.Secret.Namespace)
			if err != nil {
				m.client.Clients().Event(context.TODO(), client, "Warning", "SecretDeleteFailed", err.Error())
			} else {
				secretExists = false
				m.client.Clients().Event(context.TODO(), client, "Normal", "SecretDeleted", "Missing user so regenerating secret")
			}
		}

		if !userExists && !secretExists {
			data.Secret.Password = utils.GeneratePassword(32, true, false)

			err := db.CreateUser(database.User{
				Name:     name,
				Password: data.Secret.Password,
				Cluster: database.Cluster{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
				},
			})
			if err != nil {
				m.client.Clients().Event(context.TODO(), client, "Warning", "UserCreateFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), client, "Normal", "UserCreated", "User created")
			}

			err = m.client.Secrets().Create(context.TODO(), data.Secret)
			if err != nil {
				m.client.Clients().Event(context.TODO(), client, "Warning", "SecretCreateFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), client, "Normal", "SecretCreated", "Secret created")
			}
		}

		if client.Permission == clients.PermissionAdmin && !client.Ready {
			client.Ready = true
			err = m.client.Clients().UpdateStatus(context.TODO(), client)
			if err != nil {
				m.client.Clients().Event(context.TODO(), client, "Warning", "StatusUpdateFailed", err.Error())
			}
		}
	}

	for name, data := range cluster.Databases {
		if existing, ok := existingDBs[name]; !ok {
			err := db.CreateDB(database.Database{
				Name:  name,
				Owner: data.Owner,
				Cluster: database.Cluster{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
				},
			})
			if err != nil {
				log.Error().Err(err).Msg("Failed to create database")
			}
		} else if existing.Owner != data.Owner {
			existing.Owner = data.Owner
			err := db.SetOwner(existing)
			if err != nil {
				log.Error().Err(err).Msg("Failed to update database owner")
			}
		}
	}

	return nil
}

func (m *Manager) getClusterState(db *database.Client) (map[string]database.Database, map[string]database.User, error) {
	existingDBs, err := db.ListDBs()
	if err != nil {
		log.Error().Err(err).Msg("Failed to list databases")
		return nil, nil, fmt.Errorf("failed to list databases: %+v", err)
	}

	dbs := map[string]database.Database{}
	for _, db := range existingDBs {
		dbs[db.Name] = db
	}

	existingUsers, err := db.ListUsers()
	if err != nil {
		log.Error().Err(err).Msg("Failed to list users")
		return nil, nil, fmt.Errorf("failed to list users: %+v", err)
	}

	users := map[string]database.User{}
	for _, user := range existingUsers {
		users[user.Name] = user
	}

	return dbs, users, nil
}

func (m *Manager) resolveDatabase(cluster *model.Cluster, name string) error {
	client, err := database.New(cluster.Name, cluster.Namespace, "postgres", name)
	if err != nil {
		return fmt.Errorf("failed to create database: %+v", err)
	}
	defer client.Stop()

	db, ok := cluster.Databases[name]
	if !ok {
		return fmt.Errorf("database %s not found", name)
	}

	existingPermissions, err := client.ListPermitted(name)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list permissions")
		return fmt.Errorf("failed to list permissions: %+v", err)
	}

	for _, permission := range existingPermissions {
		owned := false
		switch permission.Write {
		case true:
			if _, ok := db.Writers[permission.User]; ok {
				owned = true
			}
		case false:
			if _, ok := db.Readers[permission.User]; ok {
				owned = true
			}
		}

		if !owned {
			err := client.RevokePermission(permission)
			if err != nil {
				log.Error().Err(err).Msg("Failed to drop permission")
			}
		}
	}

	lookup := map[string]database.Permission{}
	for _, permission := range existingPermissions {
		lookup[permission.User] = permission
	}

	for user, cli := range db.Writers {
		permission := database.Permission{
			User:     user,
			Database: name,
			Cluster: database.Cluster{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
			Write: true,
		}

		existing, exists := lookup[user]

		if exists && permission.Write != existing.Write {
			exists = false
			err := client.RevokePermission(existing)
			if err != nil {
				log.Error().Err(err).Msg("Failed to revoke permission")
				m.client.Clients().Event(context.TODO(), cli, "Warning", "PermissionFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), cli, "Normal", "PermissionRevoked", "Revoking to upgrade or downgrade permissions")
			}
		}

		if !exists {
			err := client.GrantPermission(permission)
			if err != nil {
				log.Error().Err(err).Msg("Failed to grant permission")
				m.client.Clients().Event(context.TODO(), cli, "Warning", "PermissionFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), cli, "Normal", "PermissionGranted", "Permission granted")
			}
		}

		if !cli.Ready {
			cli.Ready = true
			err = m.client.Clients().UpdateStatus(context.TODO(), cli)
			if err != nil {
				m.client.Clients().Event(context.TODO(), cli, "Warning", "StatusUpdateFailed", err.Error())
			}
		}
	}

	for user, cli := range db.Readers {
		permission := database.Permission{
			User:     user,
			Database: name,
			Cluster: database.Cluster{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		}

		existing, exists := lookup[user]

		if exists && permission.Write != existing.Write {
			exists = false
			err := client.RevokePermission(existing)
			if err != nil {
				log.Error().Err(err).Msg("Failed to revoke permission")
				m.client.Clients().Event(context.TODO(), cli, "Warning", "PermissionFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), cli, "Normal", "PermissionRevoked", "Revoking to upgrade or downgrade permissions")
			}
		}

		if !exists {
			err := client.GrantPermission(permission)
			if err != nil {
				log.Error().Err(err).Msg("Failed to grant permission")
				m.client.Clients().Event(context.TODO(), cli, "Warning", "PermissionFailed", err.Error())
			} else {
				m.client.Clients().Event(context.TODO(), cli, "Normal", "PermissionGranted", "Permission granted")
			}
		}

		if !cli.Ready {
			cli.Ready = true
			err = m.client.Clients().UpdateStatus(context.TODO(), cli)
			if err != nil {
				m.client.Clients().Event(context.TODO(), cli, "Warning", "StatusUpdateFailed", err.Error())
			}
		}
	}

	return nil
}
