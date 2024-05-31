package dag

// import (
// 	"context"

// 	pgdb "github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/database"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
// 	"github.com/benjamin-wright/db-operator/v2/internal/utils"
// )

// type client struct {
// 	client clients.Resource
// 	user   demand[pgdb.User]
// 	secret demand[secrets.Resource]
// }

// func (c client) GetID() string {
// 	return c.client.GetID()
// }

// func (c client) GetName() string {
// 	return c.client.GetName()
// }

// func (c client) GetNamespace() string {
// 	return c.client.GetNamespace()
// }

// func (c client) resolve(k8s *k8s.Client, db *pgdb.Client) error {
// 	password := c.secret.actual.Password
// 	if !c.secret.exists {
// 		password = utils.GeneratePassword(32, true, false)
// 	}

// 	if !c.secret.exists {
// 		c.secret.required.Password = password

// 		if err := k8s.Secrets().Create(context.TODO(), c.secret.required); err != nil {
// 			k8s.Clients().Event(context.TODO(), c.client, "Warning", "CreateSecretFailed", err.Error())
// 			return err
// 		}
// 		k8s.Clients().Event(context.TODO(), c.client, "Normal", "SecretCreated", "Secret created")

// 		if c.user.exists {
// 			c.user.exists = false

// 			if err := db.DeleteUser(c.User.Actual); err != nil {
// 				k8s.Clients().Event(context.TODO(), c.Client, "Warning", "DeleteUserFailed", err.Error())
// 				return err
// 			}
// 			k8s.Clients().Event(context.TODO(), c.Client, "Normal", "UserDeleted", "User existed but secret was missing, so need to regenerate")
// 		}
// 	}

// 	if !c.User.Exists {
// 		c.User.Required.Password = password

// 		if err := db.CreateUser(c.User.Required); err != nil {
// 			k8s.Clients().Event(context.TODO(), c.Client, "Warning", "CreateUserFailed", err.Error())
// 			return err
// 		}
// 		k8s.Clients().Event(context.TODO(), c.Client, "Normal", "UserCreated", "User created")
// 	}

// 	return nil
// }
