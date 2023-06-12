package database

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/cockroach/database"
	"ponglehub.co.uk/db-operator/internal/cockroach/k8s"
	"ponglehub.co.uk/db-operator/internal/state"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type State struct {
	clients      state.Bucket[k8s.CockroachClient, *k8s.CockroachClient]
	statefulSets state.Bucket[k8s.CockroachStatefulSet, *k8s.CockroachStatefulSet]
	secrets      state.Bucket[k8s.CockroachSecret, *k8s.CockroachSecret]
	migrations   state.Bucket[k8s.CockroachMigration, *k8s.CockroachMigration]
	databases    state.Bucket[database.Database, *database.Database]
	users        state.Bucket[database.User, *database.User]
	permissions  state.Bucket[database.Permission, *database.Permission]
	applied      state.Bucket[database.Migration, *database.Migration]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[k8s.CockroachClient]:
		s.clients.Apply(u)
	case k8s_generic.Update[k8s.CockroachStatefulSet]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[k8s.CockroachSecret]:
		s.secrets.Apply(u)
	case k8s_generic.Update[k8s.CockroachMigration]:
		s.migrations.Apply(u)
	case k8s_generic.Update[database.Database]:
		s.databases.Apply(u)
	case k8s_generic.Update[database.User]:
		s.users.Apply(u)
	case k8s_generic.Update[database.Permission]:
		s.permissions.Apply(u)
	case k8s_generic.Update[database.Migration]:
		s.applied.Apply(u)
	default:
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) ClearRemote() {
	s.databases.Clear()
	s.users.Clear()
	s.permissions.Clear()
	s.applied.Clear()
}

func (s *State) GetActiveClients() []k8s.CockroachClient {
	clients := []k8s.CockroachClient{}

	for _, client := range s.clients.List() {
		target := client.GetTarget()
		statefulSet, hasSS := s.statefulSets.Get(target)

		if !hasSS || !statefulSet.IsReady() {
			continue
		}

		clients = append(clients, client)
	}

	return clients
}

func (s *State) GetDBDemand() state.Demand[k8s.CockroachClient, database.Database] {
	return state.GetServiceBound(
		s.clients,
		s.databases,
		s.statefulSets,
		func(client k8s.CockroachClient) database.Database {
			return database.Database{
				Name: client.Database,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetUserDemand() state.Demand[k8s.CockroachClient, database.User] {
	return state.GetServiceBound(
		s.clients,
		s.users,
		s.statefulSets,
		func(client k8s.CockroachClient) database.User {
			return database.User{
				Name: client.Username,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetPermissionDemand() state.Demand[k8s.CockroachClient, database.Permission] {
	return state.GetServiceBound(
		s.clients,
		s.permissions,
		s.statefulSets,
		func(client k8s.CockroachClient) database.Permission {
			return database.Permission{
				User:     client.Username,
				Database: client.Database,
				DB:       client.Deployment,
			}
		},
	)
}

func (s *State) GetSecretsDemand() state.Demand[k8s.CockroachClient, k8s.CockroachSecret] {
	return state.GetServiceBound(
		s.clients,
		s.secrets,
		s.statefulSets,
		func(client k8s.CockroachClient) k8s.CockroachSecret {
			return k8s.CockroachSecret{
				CockroachSecretComparable: k8s.CockroachSecretComparable{
					Name:     client.Secret,
					DB:       client.Deployment,
					Database: client.Database,
					User:     client.Username,
				},
			}
		},
	)
}

func (s *State) GetMigrationsDemand() state.DBMigrations {
	migrations := state.NewMigrations()

	isReady := func(db string) bool {
		ss, hasSS := s.statefulSets.Get(db)

		return hasSS && ss.Ready
	}

	for _, m := range s.migrations.List() {
		if isReady(m.Deployment) {
			migrations.AddRequest(m.Deployment, m.Database, m.Index, m.Migration)
		}
	}

	for _, m := range s.applied.List() {
		migrations.AddApplied(m.DB, m.Database, m.Index)
	}

	return migrations
}
