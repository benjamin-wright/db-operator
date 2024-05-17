package database

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	clients      bucket.Bucket[k8s.CockroachClient, *k8s.CockroachClient]
	statefulSets bucket.Bucket[k8s.CockroachStatefulSet, *k8s.CockroachStatefulSet]
	secrets      bucket.Bucket[k8s.CockroachSecret, *k8s.CockroachSecret]
	migrations   bucket.Bucket[k8s.CockroachMigration, *k8s.CockroachMigration]
	databases    bucket.Bucket[database.Database, *database.Database]
	users        bucket.Bucket[database.User, *database.User]
	permissions  bucket.Bucket[database.Permission, *database.Permission]
	applied      bucket.Bucket[database.Migration, *database.Migration]
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
		log.Logger.Error().Interface("update", u).Msg("wat dis? Unknown state update")
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
		targetNamespace := client.GetTargetNamespace()
		statefulSet, hasSS := s.statefulSets.Get(target, targetNamespace)

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
				DB: database.DBRef{
					Name:      client.DBRef.Name,
					Namespace: client.DBRef.Namespace,
				},
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
				DB: database.DBRef{
					Name:      client.DBRef.Name,
					Namespace: client.DBRef.Namespace,
				},
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
				DB: database.DBRef{
					Name:      client.DBRef.Name,
					Namespace: client.DBRef.Namespace,
				},
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
					Name:      client.Secret,
					Namespace: client.Namespace,
					DB:        client.DBRef,
					Database:  client.Database,
					User:      client.Username,
				},
			}
		},
	)
}

func (s *State) GetMigrationsDemand() state.DBMigrations {
	migrations := state.NewMigrations()

	isReady := func(db string, namespace string) bool {
		ss, hasSS := s.statefulSets.Get(db, namespace)

		return hasSS && ss.Ready
	}

	for _, m := range s.migrations.List() {
		if isReady(m.DBRef.Name, m.DBRef.Namespace) {
			migrations.AddRequest(m.DBRef.Namespace, m.DBRef.Name, m.Database, m.Index, m.Migration)
		}
	}

	for _, m := range s.applied.List() {
		migrations.AddApplied(m.DB.Namespace, m.DB.Name, m.Database, m.Index)
	}

	return migrations
}
