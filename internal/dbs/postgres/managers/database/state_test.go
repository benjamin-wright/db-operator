package database

import (
	"strconv"
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/stretchr/testify/assert"
)

func client(id int, clusterid int, owner bool) clients.Resource {
	return clients.Resource{
		Comparable: clients.Comparable{
			Name:      "client" + strconv.Itoa(id),
			Namespace: "namespace" + strconv.Itoa(id),
			Cluster: clients.Cluster{
				Name:      "cluster" + strconv.Itoa(clusterid),
				Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
			},
			Database: "database" + strconv.Itoa(clusterid),
			Username: "user" + strconv.Itoa(id),
			Secret:   "secret" + strconv.Itoa(id),
			Owner:    owner,
		},
	}
}

func statefulset(clusterid int, ready bool) stateful_sets.Resource {
	return stateful_sets.Resource{
		Comparable: stateful_sets.Comparable{
			Name:      "cluster" + strconv.Itoa(clusterid),
			Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
			Storage:   "storage" + strconv.Itoa(clusterid),
			Ready:     ready,
		},
	}
}

func secret(id int, clusterid int, password string) secrets.Resource {
	return secrets.Resource{
		Comparable: secrets.Comparable{
			Name:      "secret" + strconv.Itoa(id),
			Namespace: "namespace" + strconv.Itoa(id),
			Cluster: secrets.Cluster{
				Name:      "cluster" + strconv.Itoa(clusterid),
				Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
			},
			Database: "database" + strconv.Itoa(clusterid),
			User:     "user" + strconv.Itoa(id),
			Password: password,
		},
	}
}

func db(ownerid int, clusterid int) database.Database {
	return database.Database{
		Name:  "database" + strconv.Itoa(clusterid),
		Owner: "user" + strconv.Itoa(ownerid),
		Cluster: database.Cluster{
			Name:      "cluster" + strconv.Itoa(clusterid),
			Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
		},
	}
}

func user(id int, clusterid int, password string) database.User {
	return database.User{
		Name:     "user" + strconv.Itoa(id),
		Password: password,
		Cluster: database.Cluster{
			Name:      "cluster" + strconv.Itoa(clusterid),
			Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
		},
	}
}

func permission(id int, clusterid int) database.Permission {
	return database.Permission{
		Database: "database" + strconv.Itoa(clusterid),
		User:     "user" + strconv.Itoa(id),
		Cluster: database.Cluster{
			Name:      "cluster" + strconv.Itoa(clusterid),
			Namespace: "cluster-namespace" + strconv.Itoa(clusterid),
		},
	}
}

func TestGetDemand(t *testing.T) {
	count := 0
	generatePassword = func(int, bool, bool) string {
		count++
		return "password" + strconv.Itoa(count)
	}

	type existing struct {
		clients      []clients.Resource
		statefulSets []stateful_sets.Resource
		secrets      []secrets.Resource
		databases    []database.Database
		users        []database.User
		permissions  []database.Permission
	}

	type expected struct {
		dbToAdd            []state.DemandTarget[clients.Resource, database.Database]
		userToAdd          []state.DemandTarget[clients.Resource, database.User]
		permissionToAdd    []state.DemandTarget[clients.Resource, database.Permission]
		secretToAdd        []state.DemandTarget[clients.Resource, secrets.Resource]
		dbToRemove         []database.Database
		userToRemove       []database.User
		permissionToRemove []database.Permission
		secretToRemove     []secrets.Resource
	}

	user1 := 1
	user2 := 2
	cluster1 := 1

	tests := []struct {
		name     string
		existing existing
		expected expected
	}{
		{
			name: "no clients",
		},
		{
			name: "one client with offline statefulset",
			existing: existing{
				clients:      []clients.Resource{client(user1, cluster1, true)},
				statefulSets: []stateful_sets.Resource{statefulset(cluster1, false)},
			},
		},
		{
			name: "one client",
			existing: existing{
				clients:      []clients.Resource{client(user1, cluster1, true)},
				statefulSets: []stateful_sets.Resource{statefulset(cluster1, true)},
			},
			expected: expected{
				dbToAdd: []state.DemandTarget[clients.Resource, database.Database]{
					state.NewDemandTarget(client(user1, cluster1, true), db(user1, cluster1)),
				},
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(user1, cluster1, true), user(user1, cluster1, "password1")),
				},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(user1, cluster1, true), secret(user1, cluster1, "password1")),
				},
			},
		},
		{
			name: "two clients",
			existing: existing{
				clients:      []clients.Resource{client(user1, cluster1, true), client(user2, cluster1, false)},
				statefulSets: []stateful_sets.Resource{statefulset(cluster1, true)},
			},
			expected: expected{
				dbToAdd: []state.DemandTarget[clients.Resource, database.Database]{
					state.NewDemandTarget(client(user1, cluster1, true), db(user1, cluster1)),
				},
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(user1, cluster1, true), user(user1, cluster1, "password1")),
					state.NewDemandTarget(client(user2, cluster1, false), user(user2, cluster1, "password2")),
				},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(user1, cluster1, true), secret(user1, cluster1, "password1")),
					state.NewDemandTarget(client(user2, cluster1, false), secret(user2, cluster1, "password2")),
				},
				permissionToAdd: []state.DemandTarget[clients.Resource, database.Permission]{
					state.NewDemandTarget(client(user2, cluster1, false), permission(user2, cluster1)),
				},
			},
		},
	}

	for _, tt := range tests {
		count = 0
		t.Run(tt.name, func(t *testing.T) {
			s := State{
				clients:      bucket.NewBucket[clients.Resource](),
				statefulSets: bucket.NewBucket[stateful_sets.Resource](),
				secrets:      bucket.NewBucket[secrets.Resource](),
				databases:    bucket.NewBucket[database.Database](),
				users:        bucket.NewBucket[database.User](),
				permissions:  bucket.NewBucket[database.Permission](),
			}

			for _, c := range tt.existing.clients {
				s.clients.Add(c)
			}

			for _, ss := range tt.existing.statefulSets {
				s.statefulSets.Add(ss)
			}

			for _, sec := range tt.existing.secrets {
				s.secrets.Add(sec)
			}

			for _, db := range tt.existing.databases {
				s.databases.Add(db)
			}

			for _, u := range tt.existing.users {
				s.users.Add(u)
			}

			for _, p := range tt.existing.permissions {
				s.permissions.Add(p)
			}

			dbDemand, userDemand, permissionDemand, secretDemand := s.GetDemand()

			assert.EqualValues(t, state.NewInitializedDemand(tt.expected.dbToAdd, tt.expected.dbToRemove), dbDemand, "dbDemand")
			assert.EqualValues(t, state.NewInitializedDemand(tt.expected.userToAdd, tt.expected.userToRemove), userDemand, "userDemand")
			assert.EqualValues(t, state.NewInitializedDemand(tt.expected.permissionToAdd, tt.expected.permissionToRemove), permissionDemand, "permissionDemand")
			assert.EqualValues(t, state.NewInitializedDemand(tt.expected.secretToAdd, tt.expected.secretToRemove), secretDemand, "secretDemand")
		})
	}
}
