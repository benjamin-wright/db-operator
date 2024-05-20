package database

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/internal/utils"
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
	default:
		log.Logger.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) ClearRemote() {
	s.databases.Clear()
	s.users.Clear()
	s.permissions.Clear()
}

func (s *State) getRequests() (
	bucket.Bucket[state.DemandTarget[clients.Resource, database.Database]],
	bucket.Bucket[state.DemandTarget[clients.Resource, database.User]],
	bucket.Bucket[state.DemandTarget[clients.Resource, database.Permission]],
	bucket.Bucket[state.DemandTarget[clients.Resource, secrets.Resource]],
) {
	databaseRequests := bucket.NewBucket[state.DemandTarget[clients.Resource, database.Database]]()
	userRequests := bucket.NewBucket[state.DemandTarget[clients.Resource, database.User]]()
	permissionRequests := bucket.NewBucket[state.DemandTarget[clients.Resource, database.Permission]]()
	secretRequests := bucket.NewBucket[state.DemandTarget[clients.Resource, secrets.Resource]]()

	for _, client := range s.clients.List() {
		target := client.GetTarget()
		targetNamespace := client.GetTargetNamespace()

		statefulSet, hasSS := s.statefulSets.Get(target, targetNamespace)

		if !hasSS || !statefulSet.IsReady() {
			continue
		}

		if client.Owner {
			databaseRequests.Add(state.DemandTarget[clients.Resource, database.Database]{
				Parent: client,
				Target: database.Database{
					Name: client.Database,
					Cluster: database.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
					Owner: client.Username,
				},
			})
		}

		userRequests.Add(state.DemandTarget[clients.Resource, database.User]{
			Parent: client,
			Target: database.User{
				Name: client.Username,
				Cluster: database.Cluster{
					Name:      client.Cluster.Name,
					Namespace: client.Cluster.Namespace,
				},
			},
		})

		permissionRequests.Add(state.DemandTarget[clients.Resource, database.Permission]{
			Parent: client,
			Target: database.Permission{
				User:     client.Username,
				Database: client.Database,
				Owner:    client.Owner,
				Cluster: database.Cluster{
					Name:      client.Cluster.Name,
					Namespace: client.Cluster.Namespace,
				},
			},
		})

		secretRequests.Add(state.DemandTarget[clients.Resource, secrets.Resource]{
			Parent: client,
			Target: secrets.Resource{
				Comparable: secrets.Comparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					Database:  client.Database,
					User:      client.Username,
					Cluster: secrets.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
				},
			},
		})
	}

	return databaseRequests, userRequests, permissionRequests, secretRequests
}

func (s *State) diffDatabases(requests bucket.Bucket[state.DemandTarget[clients.Resource, database.Database]]) state.Demand[clients.Resource, database.Database] {
	demand := state.NewDemand[clients.Resource, database.Database]()

	for _, db := range requests.List() {
		if _, ok := s.databases.Get(db.Target.GetName(), db.Target.GetNamespace()); !ok {
			demand.ToAdd.Add(db)
		}
	}

	for _, db := range s.databases.List() {
		if _, ok := requests.Get(db.GetName(), db.GetNamespace()); !ok {
			demand.ToRemove.Add(db)
		}
	}

	return demand
}

func (s *State) diffUsers(requests bucket.Bucket[state.DemandTarget[clients.Resource, database.User]]) state.Demand[clients.Resource, database.User] {
	demand := state.NewDemand[clients.Resource, database.User]()

	for _, userRequest := range requests.List() {
		_, userExists := s.users.Get(userRequest.Target.GetName(), userRequest.Target.GetNamespace())
		_, secretExists := s.secrets.Get(userRequest.Parent.Secret, userRequest.Parent.Namespace)

		if userExists && !secretExists {
			demand.ToRemove.Add(userRequest.Target)
			userExists = false
		}

		if !userExists {
			userRequest.Target.Password = utils.GeneratePassword(32, true, true)
			demand.ToAdd.Add(userRequest)
		}
	}

	for _, user := range s.users.List() {
		if _, ok := requests.Get(user.GetName(), user.GetNamespace()); !ok {
			demand.ToRemove.Add(user)
		}
	}

	return demand
}

func (s *State) diffPermissions(
	requests bucket.Bucket[state.DemandTarget[clients.Resource, database.Permission]],
	deadUsers bucket.Bucket[database.User],
) state.Demand[clients.Resource, database.Permission] {
	demand := state.NewDemand[clients.Resource, database.Permission]()

	for _, permissionRequest := range requests.List() {
		existing, permissionExists := s.permissions.Get(permissionRequest.Target.GetName(), permissionRequest.Target.GetNamespace())
		_, isRefreshing := deadUsers.Get(permissionRequest.Parent.Username, permissionRequest.Target.GetNamespace())

		if !permissionExists {
			demand.ToAdd.Add(permissionRequest)
			continue
		}

		if isRefreshing {
			demand.ToRemove.Add(permissionRequest.Target)
			demand.ToAdd.Add(permissionRequest)
		} else if existing.Owner != permissionRequest.Target.Owner {
			demand.ToRemove.Add(existing)
			demand.ToAdd.Add(permissionRequest)
		}
	}

	for _, permission := range s.permissions.List() {
		if _, ok := requests.Get(permission.GetName(), permission.GetNamespace()); !ok {
			demand.ToRemove.Add(permission)
		}
	}

	return demand
}

func (s *State) diffSecrets(
	requests bucket.Bucket[state.DemandTarget[clients.Resource, secrets.Resource]],
	users state.Demand[clients.Resource, database.User],
) state.Demand[clients.Resource, secrets.Resource] {
	demand := state.NewDemand[clients.Resource, secrets.Resource]()

	for _, secretRequest := range requests.List() {
		_, secretExists := s.secrets.Get(secretRequest.Target.GetName(), secretRequest.Target.GetNamespace())
		_, isRefreshing := users.ToRemove.Get(secretRequest.Parent.Username, secretRequest.Target.Cluster.GetNamespace())

		if secretExists && isRefreshing {
			demand.ToRemove.Add(secretRequest.Target)
			secretExists = false
		}

		if !secretExists {
			user, ok := users.ToAdd.Get(secretRequest.Parent.Username, secretRequest.Target.Cluster.GetNamespace())
			if !ok {
				log.Logger.Error().Str("secret", secretRequest.Target.GetName()).Msg("wat dis? User not found for secret")
				continue
			}

			secretRequest.Target.Password = user.Target.Password
			demand.ToAdd.Add(secretRequest)
		}
	}

	for _, secret := range s.secrets.List() {
		if _, ok := requests.Get(secret.GetName(), secret.GetNamespace()); !ok {
			demand.ToRemove.Add(secret)
		}
	}

	return demand
}

func (s *State) GetDemand() (
	state.Demand[clients.Resource, database.Database],
	state.Demand[clients.Resource, database.User],
	state.Demand[clients.Resource, database.Permission],
	state.Demand[clients.Resource, secrets.Resource],
) {
	dbRequests, userRequests, permissionRequests, secretRequests := s.getRequests()

	dbDemand := s.diffDatabases(dbRequests)
	userDemand := s.diffUsers(userRequests)
	permissionDemand := s.diffPermissions(permissionRequests, userDemand.ToRemove)
	secretsDemand := s.diffSecrets(secretRequests, userDemand)

	return dbDemand, userDemand, permissionDemand, secretsDemand
}

func (s *State) GetActiveClusters() []database.Cluster {
	clusters := []database.Cluster{}

	for _, ss := range s.statefulSets.List() {
		if !ss.IsReady() {
			continue
		}

		clusters = append(clusters, database.Cluster{
			Name:      ss.Name,
			Namespace: ss.Namespace,
		})
	}

	return clusters
}
