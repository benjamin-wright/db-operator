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
	passwords    map[string]string
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
		} else {
			permissionRequests.Add(state.DemandTarget[clients.Resource, database.Permission]{
				Parent: client,
				Target: database.Permission{
					User:     client.Username,
					Database: client.Database,
					Cluster: database.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
				},
			})
		}

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

var generatePassword = func(user string) string {
	return utils.GeneratePassword(32, true, true)
}

func (s *State) diffUsers(requests bucket.Bucket[state.DemandTarget[clients.Resource, database.User]]) state.Demand[clients.Resource, database.User] {
	demand := state.NewDemand[clients.Resource, database.User]()

	for _, request := range requests.List() {
		_, userExists := s.users.Get(request.Target.GetName(), request.Target.GetNamespace())
		_, secretExists := s.secrets.Get(request.Parent.Secret, request.Parent.Namespace)

		if userExists && !secretExists {
			demand.ToRemove.Add(request.Target)
			userExists = false
		}

		if !userExists {
			s.passwords[request.Parent.Name+":"+request.Parent.Namespace] = generatePassword(request.Target.Name)
			request.Target.Password = s.passwords[request.Parent.Name+":"+request.Parent.Namespace]
			demand.ToAdd.Add(request)
		}
	}

	for _, user := range s.users.List() {
		if _, ok := requests.Get(user.GetName(), user.GetNamespace()); !ok {
			demand.ToRemove.Add(user)
		}
	}

	return demand
}

func (s *State) diffSecrets(
	requests bucket.Bucket[state.DemandTarget[clients.Resource, secrets.Resource]],
) state.Demand[clients.Resource, secrets.Resource] {
	demand := state.NewDemand[clients.Resource, secrets.Resource]()

	for _, request := range requests.List() {
		oldSecret, secretExists := s.secrets.Get(request.Target.GetName(), request.Target.GetNamespace())
		_, userExists := s.users.Get(request.Parent.Username, request.Target.Cluster.GetNamespace())

		if secretExists && !userExists {
			demand.ToRemove.Add(oldSecret)
			secretExists = false
		}

		if !secretExists {
			request.Target.Password = s.passwords[request.Parent.Name+":"+request.Parent.Namespace]
			demand.ToAdd.Add(request)
		}
	}

	for _, secret := range s.secrets.List() {
		if _, ok := requests.Get(secret.GetName(), secret.GetNamespace()); !ok {
			demand.ToRemove.Add(secret)
		}
	}

	return demand
}

func (s *State) diffPermissions(
	requests bucket.Bucket[state.DemandTarget[clients.Resource, database.Permission]],
) state.Demand[clients.Resource, database.Permission] {
	demand := state.NewDemand[clients.Resource, database.Permission]()

	for _, request := range requests.List() {
		_, permissionExists := s.permissions.Get(request.Target.GetName(), request.Target.GetNamespace())
		_, userExists := s.users.Get(request.Parent.Username, request.Target.GetNamespace())
		_, secretExists := s.secrets.Get(request.Parent.Secret, request.Parent.Namespace)

		if permissionExists && (!userExists || !secretExists) {
			demand.ToRemove.Add(request.Target)
			permissionExists = false
		}

		if !permissionExists {
			demand.ToAdd.Add(request)
			continue
		}
	}

	for _, permission := range s.permissions.List() {
		if _, ok := requests.Get(permission.GetName(), permission.GetNamespace()); !ok {
			demand.ToRemove.Add(permission)
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

	s.passwords = map[string]string{}
	dbDemand := s.diffDatabases(dbRequests)
	userDemand := s.diffUsers(userRequests)
	permissionDemand := s.diffPermissions(permissionRequests)
	secretsDemand := s.diffSecrets(secretRequests)

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
