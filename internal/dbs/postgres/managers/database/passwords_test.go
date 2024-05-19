package database

// import (
// 	"fmt"
// 	"testing"

// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
// 	"github.com/benjamin-wright/db-operator/internal/state"
// 	"github.com/benjamin-wright/db-operator/internal/state/bucket"
// 	"github.com/stretchr/testify/assert"
// )

// func secret(id int) secrets.Resource {
// 	return secrets.Resource{
// 		Comparable: secrets.Comparable{
// 			Name:      fmt.Sprintf("secret%d", id),
// 			Namespace: fmt.Sprintf("namespace%d", id),
// 			User:      fmt.Sprintf("user%d", id),
// 			Database:  fmt.Sprintf("database%d", id),
// 			Cluster: secrets.Cluster{
// 				Name:      fmt.Sprintf("cluster%d", id),
// 				Namespace: fmt.Sprintf("cluster-namespace%d", id),
// 			},
// 		},
// 	}
// }

// func user(id int) database.User {
// 	return database.User{
// 		Name: fmt.Sprintf("user%d", id),
// 		Cluster: database.Cluster{
// 			Name:      fmt.Sprintf("cluster%d", id),
// 			Namespace: fmt.Sprintf("cluster-namespace%d", id),
// 		},
// 	}
// }

// func client(id int) clients.Resource {
// 	return clients.Resource{
// 		Comparable: clients.Comparable{
// 			Username:  fmt.Sprintf("user%d", id),
// 			Secret:    fmt.Sprintf("secret%d", id),
// 			Namespace: fmt.Sprintf("namespace%d", id),
// 			Database:  fmt.Sprintf("database%d", id),
// 			Cluster: clients.Cluster{
// 				Name:      fmt.Sprintf("cluster%d", id),
// 				Namespace: fmt.Sprintf("cluster-namespace%d", id),
// 			},
// 		},
// 	}
// }

// func secretDemand(id int) state.DemandTarget[clients.Resource, secrets.Resource] {
// 	return state.DemandTarget[clients.Resource, secrets.Resource]{
// 		Parent: client(id),
// 		Target: secret(id),
// 	}
// }

// func secretWithPassword(id int, password string) state.DemandTarget[clients.Resource, secrets.Resource] {
// 	secret := secretDemand(id)
// 	secret.Target.Password = password
// 	return secret
// }

// func userDemand(id int) state.DemandTarget[clients.Resource, database.User] {
// 	return state.DemandTarget[clients.Resource, database.User]{
// 		Parent: client(id),
// 		Target: user(id),
// 	}
// }

// func userWithPassword(id int, password string) state.DemandTarget[clients.Resource, database.User] {
// 	user := userDemand(id)
// 	user.Target.Password = password
// 	return user
// }

// func TestSetPasswords(t *testing.T) {
// 	generatePassword = func(int, bool, bool) string {
// 		return "password"
// 	}

// 	type testDemand struct {
// 		secretsToAdd    bucket.Bucket[state.DemandTarget[clients.Resource, secrets.Resource]]
// 		secretsToRemove []secrets.Resource
// 		usersToAdd      []state.DemandTarget[clients.Resource, database.User]
// 		usersToRemove   []database.User
// 	}

// 	tests := []struct {
// 		name            string
// 		input           testDemand
// 		existingUsers   []database.User
// 		existingSecrets []secrets.Resource
// 		expected        testDemand
// 	}{
// 		{
// 			name: "set passwords",
// 			input: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretDemand(1),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userDemand(1),
// 				},
// 			},
// 			expected: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretWithPassword(1, "password"),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userWithPassword(1, "password"),
// 				},
// 			},
// 		},
// 		{
// 			name: "secret required but user missing from demand",
// 			input: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretDemand(1),
// 				},
// 			},
// 			existingUsers: []database.User{
// 				user(1),
// 			},
// 			expected: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretWithPassword(1, "password"),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userWithPassword(1, "password"),
// 				},
// 				usersToRemove: []database.User{
// 					user(1),
// 				},
// 			},
// 		},
// 		{
// 			name: "secret required but user missing from demand and doesn't already exist",
// 			input: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretDemand(1),
// 				},
// 			},
// 			expected: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretWithPassword(1, "password"),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userWithPassword(1, "password"),
// 				},
// 			},
// 		},
// 		{
// 			name: "user required but secret missing from demand",
// 			input: testDemand{
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userDemand(1),
// 				},
// 			},
// 			existingSecrets: []secrets.Resource{
// 				secret(1),
// 			},
// 			expected: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretWithPassword(1, "password"),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userWithPassword(1, "password"),
// 				},
// 				secretsToRemove: []secrets.Resource{
// 					secret(1),
// 				},
// 			},
// 		},
// 		{
// 			name: "user required but secret missing from demand and doesn't already exist",
// 			input: testDemand{
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userDemand(1),
// 				},
// 			},
// 			expected: testDemand{
// 				secretsToAdd: []state.DemandTarget[clients.Resource, secrets.Resource]{
// 					secretWithPassword(1, "password"),
// 				},
// 				usersToAdd: []state.DemandTarget[clients.Resource, database.User]{
// 					userWithPassword(1, "password"),
// 				},
// 			},
// 		},
// 	}

// 	for _, test := range tests {
// 		t.Run(test.name, func(t *testing.T) {
// 			secretsDemand := state.Demand[clients.Resource, secrets.Resource]{
// 				ToAdd:    test.input.secretsToAdd,
// 				ToRemove: test.input.secretsToRemove,
// 			}

// 			usersDemand := state.Demand[clients.Resource, database.User]{
// 				ToAdd:    test.input.usersToAdd,
// 				ToRemove: test.input.usersToRemove,
// 			}

// 			existingUsers := bucket.NewBucket[database.User]()
// 			for _, user := range test.existingUsers {
// 				existingUsers.Add(user)
// 			}

// 			existingSecrets := bucket.NewBucket[secrets.Resource]()
// 			for _, secret := range test.existingSecrets {
// 				existingSecrets.Add(secret)
// 			}

// 			setPasswords(&secretsDemand, &usersDemand, existingUsers, existingSecrets)

// 			assert.Equal(t, test.expected.secretsToAdd, secretsDemand.ToAdd, "secretsToAdd")
// 			assert.Equal(t, test.expected.secretsToRemove, secretsDemand.ToRemove, "secretsToRemove")
// 			assert.Equal(t, test.expected.usersToAdd, usersDemand.ToAdd, "usersToAdd")
// 			assert.Equal(t, test.expected.usersToRemove, usersDemand.ToRemove, "usersToRemove")
// 		})
// 	}
// }
