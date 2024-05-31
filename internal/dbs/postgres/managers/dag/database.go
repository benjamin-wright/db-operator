package dag

// import (
// 	"context"
// 	"fmt"

// 	pgdb "github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/database"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
// )

// type database struct {
// 	name    string
// 	owner   string
// 	readers []string
// 	writers []string
// }

// func (d database) GetID() string {
// 	return d.name
// }

// func (d database) resolve(k8s *k8s.Client, admin *pgdb.Client) error {
// 	if !d.database.exists {
// 		err := admin.CreateDB(d.database.required)
// 		if err != nil {
// 			return fmt.Errorf("failed to create database: %+v", err)
// 		}
// 		k8s.Clients().Event(context.TODO(), d.Owner.Client, "Normal", "DatabaseCreated", fmt.Sprintf("Database %s created", d.Database.Required.Name))
// 	}

// 	return nil
// }
