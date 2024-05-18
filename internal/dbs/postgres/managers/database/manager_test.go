package database

import (
	"testing"

	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/stretchr/testify/assert"
)

func secret(user string, cluster string, namespace string) state.DemandTarget[clients.Resource, secrets.Resource] {
	return state.DemandTarget[clients.Resource, secrets.Resource]{
		Parent: clients.Resource{},
		Target: secrets.Resource{
			Comparable: secrets.Comparable{
				User: user,
				Cluster: secrets.Cluster{
					Name:      cluster,
					Namespace: namespace,
				},
			},
		},
	}
}

func user(name string, cluster string, namespace string) state.DemandTarget[clients.Resource, database.User] {
	return state.DemandTarget[clients.Resource, database.User]{
		Parent: clients.Resource{},
		Target: database.User{
			Name: name,
			Cluster: database.Cluster{
				Name:      cluster,
				Namespace: namespace,
			},
		},
	}
}

func TestSetPasswords(t *testing.T) {
	t.Run("set passwords", func(t *testing.T) {
		secretsDemand := state.Demand[clients.Resource, secrets.Resource]{
			ToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
				secret("user1", "cluster1", "namespace1"),
			},
			ToRemove: []state.DemandTarget[clients.Resource, secrets.Resource]{},
		}

		usersDemand := state.Demand[clients.Resource, database.User]{
			ToAdd: []state.DemandTarget[clients.Resource, database.User]{
				user("user1", "cluster1", "namespace1"),
			},
			ToRemove: []state.DemandTarget[clients.Resource, database.User]{},
		}

		setPasswords(&secretsDemand, &usersDemand, bucket.Bucket[database.User]{})

		assert.Len(t, secretsDemand.ToAdd, 1)
		assert.Len(t, secretsDemand.ToRemove, 0)
		assert.Len(t, usersDemand.ToAdd, 1)
		assert.Len(t, usersDemand.ToRemove, 0)

		password := usersDemand.ToAdd[0].Target.Password
		assert.NotEmpty(t, password)
		assert.Len(t, password, 32*2)
		assert.Equal(t, password, secretsDemand.ToAdd[0].Target.Password)
	})

	t.Run("secret required but user already exists", func(t *testing.T) {
		secretsDemand := state.Demand[clients.Resource, secrets.Resource]{
			ToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
				secret("user1", "cluster1", "namespace1"),
			},
			ToRemove: []state.DemandTarget[clients.Resource, secrets.Resource]{},
		}

		usersDemand := state.Demand[clients.Resource, database.User]{
			ToAdd:    []state.DemandTarget[clients.Resource, database.User]{},
			ToRemove: []state.DemandTarget[clients.Resource, database.User]{},
		}

		existingUsers := bucket.NewBucket[database.User]()
		existingUsers.Add(database.User{
			Name: "user1",
			Cluster: database.Cluster{
				Name:      "cluster1",
				Namespace: "namespace1",
			},
		})

		setPasswords(&secretsDemand, &usersDemand, existingUsers)

		assert.Len(t, secretsDemand.ToAdd, 1)
		assert.Len(t, secretsDemand.ToRemove, 0)
		assert.Len(t, usersDemand.ToAdd, 1)
		assert.Len(t, usersDemand.ToRemove, 1)

		password := usersDemand.ToAdd[0].Target.Password

		assert.NotEmpty(t, password)
		assert.Len(t, password, 32*2)
		assert.Equal(t, password, secretsDemand.ToAdd[0].Target.Password)
	})
}
