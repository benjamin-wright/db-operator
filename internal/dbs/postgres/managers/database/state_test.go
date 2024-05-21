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

func secretWithUser(id int, clusterid int, password string, user string) secrets.Resource {
	s := secret(id, clusterid, password)
	s.User = user
	return s
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

func withName(u database.User, name string) database.User {
	u.Name = name
	return u
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
	generatePassword = func(name string) string {
		return name + "-pwd"
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

	userid1 := 1
	userid2 := 2
	// userid3 := 3
	clusterid1 := 1

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
				clients:      []clients.Resource{client(userid1, clusterid1, true)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, false)},
			},
		},
		{
			name: "one client",
			existing: existing{
				clients:      []clients.Resource{client(userid1, clusterid1, true)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
			},
			expected: expected{
				dbToAdd: []state.DemandTarget[clients.Resource, database.Database]{
					state.NewDemandTarget(client(userid1, clusterid1, true), db(userid1, clusterid1)),
				},
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(userid1, clusterid1, true), user(userid1, clusterid1, "user1-pwd")),
				},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(userid1, clusterid1, true), secret(userid1, clusterid1, "user1-pwd")),
				},
			},
		},
		{
			name: "two clients",
			existing: existing{
				clients:      []clients.Resource{client(userid1, clusterid1, true), client(userid2, clusterid1, false)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
			},
			expected: expected{
				dbToAdd: []state.DemandTarget[clients.Resource, database.Database]{
					state.NewDemandTarget(client(userid1, clusterid1, true), db(userid1, clusterid1)),
				},
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(userid1, clusterid1, true), user(userid1, clusterid1, "user1-pwd")),
					state.NewDemandTarget(client(userid2, clusterid1, false), user(userid2, clusterid1, "user2-pwd")),
				},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(userid1, clusterid1, true), secret(userid1, clusterid1, "user1-pwd")),
					state.NewDemandTarget(client(userid2, clusterid1, false), secret(userid2, clusterid1, "user2-pwd")),
				},
				permissionToAdd: []state.DemandTarget[clients.Resource, database.Permission]{
					state.NewDemandTarget(client(userid2, clusterid1, false), permission(userid2, clusterid1)),
				},
			},
		},
		{
			name: "everything exists",
			existing: existing{
				clients:      []clients.Resource{client(userid1, clusterid1, true), client(userid2, clusterid1, false)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
				databases:    []database.Database{db(userid1, clusterid1)},
				users:        []database.User{user(userid1, clusterid1, ""), user(userid2, clusterid1, "")},
				permissions:  []database.Permission{permission(userid2, clusterid1)},
				secrets:      []secrets.Resource{secret(userid1, clusterid1, "oldpwd1"), secret(userid2, clusterid1, "oldpwd2")},
			},
		},
		{
			name: "missing secret",
			existing: existing{
				clients:      []clients.Resource{client(userid1, clusterid1, true), client(userid2, clusterid1, false)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
				databases:    []database.Database{db(userid1, clusterid1)},
				users:        []database.User{user(userid1, clusterid1, ""), user(userid2, clusterid1, "")},
				permissions:  []database.Permission{permission(userid2, clusterid1)},
				secrets:      []secrets.Resource{secret(userid1, clusterid1, "oldpwd1")},
			},
			expected: expected{
				userToRemove: []database.User{user(userid2, clusterid1, "")},
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(userid2, clusterid1, false), user(userid2, clusterid1, "user2-pwd")),
				},
				permissionToRemove: []database.Permission{permission(userid2, clusterid1)},
				permissionToAdd: []state.DemandTarget[clients.Resource, database.Permission]{
					state.NewDemandTarget(client(userid2, clusterid1, false), permission(userid2, clusterid1)),
				},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(userid2, clusterid1, false), secret(userid2, clusterid1, "user2-pwd")),
				},
			},
		},
		{
			name: "missing user",
			existing: existing{
				clients:      []clients.Resource{client(userid1, clusterid1, true), client(userid2, clusterid1, false)},
				statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
				databases:    []database.Database{db(userid1, clusterid1)},
				users:        []database.User{user(userid1, clusterid1, "")},
				permissions:  []database.Permission{permission(userid2, clusterid1)},
				secrets:      []secrets.Resource{secret(userid1, clusterid1, "oldpwd1"), secret(userid2, clusterid1, "oldpwd2")},
			},
			expected: expected{
				userToAdd: []state.DemandTarget[clients.Resource, database.User]{
					state.NewDemandTarget(client(userid2, clusterid1, false), user(userid2, clusterid1, "user2-pwd")),
				},
				secretToRemove: []secrets.Resource{secret(userid2, clusterid1, "oldpwd2")},
				secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
					state.NewDemandTarget(client(userid2, clusterid1, false), secret(userid2, clusterid1, "user2-pwd")),
				},
				permissionToRemove: []database.Permission{permission(userid2, clusterid1)},
				permissionToAdd: []state.DemandTarget[clients.Resource, database.Permission]{
					state.NewDemandTarget(client(userid2, clusterid1, false), permission(userid2, clusterid1)),
				},
			},
		},
		// {
		// 	name: "wrong user name",
		// 	existing: existing{
		// 		clients:      []clients.Resource{client(userid1, clusterid1, true), client(userid2, clusterid1, false)},
		// 		statefulSets: []stateful_sets.Resource{statefulset(clusterid1, true)},
		// 		databases:    []database.Database{db(userid1, clusterid1)},
		// 		users:        []database.User{user(userid1, clusterid1, ""), withName(user(userid2, clusterid1, ""), "user3")},
		// 		permissions:  []database.Permission{permission(userid3, clusterid1)},
		// 		secrets:      []secrets.Resource{secret(userid1, clusterid1, "oldpwd1"), secretWithUser(userid2, clusterid1, "oldpwd2", "user3")},
		// 	},
		// 	expected: expected{
		// 		userToRemove: []database.User{withName(user(userid2, clusterid1, ""), "user3")},
		// 		userToAdd: []state.DemandTarget[clients.Resource, database.User]{
		// 			state.NewDemandTarget(client(userid2, clusterid1, false), user(userid2, clusterid1, "user2-pwd")),
		// 		},
		// 		permissionToRemove: []database.Permission{permission(userid3, clusterid1)},
		// 		permissionToAdd: []state.DemandTarget[clients.Resource, database.Permission]{
		// 			state.NewDemandTarget(client(userid2, clusterid1, false), permission(userid2, clusterid1)),
		// 		},
		// 		secretToRemove: []secrets.Resource{secretWithUser(userid2, clusterid1, "oldpwd2", "user3")},
		// 		secretToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
		// 			state.NewDemandTarget(client(userid2, clusterid1, false), secret(userid2, clusterid1, "user2-pwd")),
		// 		},
		// 	},
		// },
	}

	for _, tt := range tests {
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
