package database

// import (
// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/database"
// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
// 	"github.com/benjamin-wright/db-operator/internal/state"
// 	"github.com/benjamin-wright/db-operator/internal/state/bucket"
// 	"github.com/benjamin-wright/db-operator/internal/utils"
// )

// var generatePassword = utils.GeneratePassword

// func isUserSecret(user database.User, secret secrets.Resource) bool {
// 	return user.Cluster.Name == secret.Cluster.Name && user.Cluster.Namespace == secret.Cluster.Namespace && user.Name == secret.User
// }

// func alignSecretDemand(
// 	secretsDemand *state.Demand[clients.Resource, secrets.Resource],
// 	userDemand *state.Demand[clients.Resource, database.User],
// 	usersBucket bucket.Bucket[database.User],
// ) {
// 	for _, secret := range secretsDemand.ToAdd.List() {
// 		userId := -1

// 		// check if the user already exists in the user demand
// 		for id, user := range userDemand.ToAdd.List() {
// 			if isUserSecret(user.Target, secret.Target) {
// 				userId = id
// 				break
// 			}
// 		}

// 		// if the user exists in the user demand then everything is good
// 		if userId >= 0 {
// 			continue
// 		}

// 		// delete if already exists
// 		if existing, ok := usersBucket.Get(secret.Target.User, secret.Target.Cluster.TargetName()); ok {
// 			userDemand.ToRemove.Add(existing)
// 		}

// 		// re-add the user to the user demand
// 		userDemand.ToAdd.Add(state.DemandTarget[clients.Resource, database.User]{
// 			Parent: secret.Parent,
// 			Target: database.User{
// 				Name: secret.Target.User,
// 				Cluster: database.Cluster{
// 					Name:      secret.Target.Cluster.Name,
// 					Namespace: secret.Target.Cluster.Namespace,
// 				},
// 			},
// 		})
// 	}
// }

// func alignUserDemand(
// 	secretsDemand *state.Demand[clients.Resource, secrets.Resource],
// 	userDemand *state.Demand[clients.Resource, database.User],
// 	secretsBucket bucket.Bucket[secrets.Resource],
// ) {
// 	for _, user := range userDemand.ToAdd.List() {
// 		secretId := -1

// 		// check if the secret already exists in the secret demand
// 		for id, secret := range secretsDemand.ToAdd.List() {
// 			if isUserSecret(user.Target, secret.Target) {
// 				secretId = id
// 				break
// 			}
// 		}

// 		// if the secret exists in the secret demand then everything is good
// 		if secretId >= 0 {
// 			continue
// 		}

// 		// delete if already exists
// 		if existing, ok := secretsBucket.Get(user.Parent.Secret, user.Parent.Namespace); ok {
// 			secretsDemand.ToRemove.Add(existing)
// 		}

// 		// re-add the secret to the secret demand
// 		secretsDemand.ToAdd.Add(state.DemandTarget[clients.Resource, secrets.Resource]{
// 			Parent: user.Parent,
// 			Target: secrets.Resource{
// 				Comparable: secrets.Comparable{
// 					Name:      user.Parent.Secret,
// 					Namespace: user.Parent.Namespace,
// 					Database:  user.Parent.Database,
// 					User:      user.Target.Name,
// 					Cluster: secrets.Cluster{
// 						Name:      user.Target.Cluster.Name,
// 						Namespace: user.Target.Cluster.Namespace,
// 					},
// 				},
// 			},
// 		})
// 	}
// }

// func setPasswords(
// 	secretsDemand *state.Demand[clients.Resource, secrets.Resource],
// 	userDemand *state.Demand[clients.Resource, database.User],
// 	usersBucket bucket.Bucket[database.User],
// 	secretsBucket bucket.Bucket[secrets.Resource],
// ) {
// 	alignSecretDemand(secretsDemand, userDemand, usersBucket)
// 	alignUserDemand(secretsDemand, userDemand, secretsBucket)

// 	for secretId, secret := range secretsDemand.ToAdd.List() {
// 		for userId, user := range userDemand.ToAdd.List() {
// 			if isUserSecret(user.Target, secret.Target) {
// 				password := generatePassword(32, true, true)
// 				userDemand.ToAdd.List()[userId].Target.Password = password
// 				secretsDemand.ToAdd.List()[secretId].Target.Password = password
// 				break
// 			}
// 		}

// 	}
// }
