package database

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	clients      bucket.Bucket[clients.Resource]
	statefulSets bucket.Bucket[stateful_sets.Resource]
	secrets      bucket.Bucket[secrets.Resource]
	databases    bucket.Bucket[database.Database]
	users        bucket.Bucket[database.User]
	permissions  bucket.Bucket[database.Permission]
	applied      bucket.Bucket[database.Migration]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[clients.Resource]:
		s.clients.Apply(u)
	case k8s_generic.Update[stateful_sets.Resource]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[secrets.Resource]:
		s.secrets.Apply(u)
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

func (s *State) GetActiveClients() []clients.Resource {
	clients := []clients.Resource{}

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

func (s *State) GetDBDemand() state.Demand[clients.Resource, database.Database] {
	return state.GetServiceBound(
		s.clients,
		s.databases,
		s.statefulSets,
		func(client clients.Resource) database.Database {
			return database.Database{
				Name: client.Database,
				Cluster: database.Cluster{
					Name:      client.Cluster.Name,
					Namespace: client.Cluster.Namespace,
				},
			}
		},
	)
}

func (s *State) GetUserDemand() state.Demand[clients.Resource, database.User] {
	return state.GetServiceBound(
		s.clients,
		s.users,
		s.statefulSets,
		func(client clients.Resource) database.User {
			return database.User{
				Name: client.Username,
				Cluster: database.Cluster{
					Name:      client.Cluster.Name,
					Namespace: client.Cluster.Namespace,
				},
			}
		},
	)
}

func (s *State) GetPermissionDemand() state.Demand[clients.Resource, database.Permission] {
	return state.GetServiceBound(
		s.clients,
		s.permissions,
		s.statefulSets,
		func(client clients.Resource) database.Permission {
			return database.Permission{
				User:     client.Username,
				Database: client.Database,
				Cluster: database.Cluster{
					Name:      client.Cluster.Name,
					Namespace: client.Cluster.Namespace,
				},
			}
		},
	)
}

func (s *State) GetSecretsDemand() state.Demand[clients.Resource, secrets.Resource] {
	return state.GetServiceBound(
		s.clients,
		s.secrets,
		s.statefulSets,
		func(client clients.Resource) secrets.Resource {
			return secrets.Resource{
				Comparable: secrets.Comparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					Cluster: secrets.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
					Database: client.Database,
					User:     client.Username,
				},
			}
		},
	)
}
