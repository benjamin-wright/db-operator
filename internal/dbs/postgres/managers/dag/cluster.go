package dag

// import (
// 	"context"
// 	"fmt"

// 	pgdb "github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/database"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/services"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
// 	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
// )

// type cluster struct {
// 	cluster   clusters.Resource
// 	users     bucket.Bucket[*client]
// 	databases bucket.Bucket[*database]

// 	deployment demand[stateful_sets.Resource]
// 	service    demand[services.Resource]
// }

// func (c cluster) GetID() string {
// 	return c.cluster.GetID()
// }

// func (c cluster) addClient(cli clients.Resource) {
// 	clientObj := client{
// 		client: cli,
// 		user: demand[pgdb.User]{
// 			required: pgdb.User{
// 				Name: cli.Username,
// 				Cluster: pgdb.Cluster{
// 					Name:      cli.Cluster.Name,
// 					Namespace: cli.Cluster.Namespace,
// 				},
// 			},
// 		},
// 	}

// 	c.users.Add(&clientObj)

// 	for _, db := range cli.Admin {
// 		if existing, ok := c.databases.Get(db); ok {
// 			existing.owner = cli.Username
// 			continue
// 		}

// 		c.databases.Add(&database{
// 			name:  db,
// 			owner: cli.Username,
// 		})
// 	}

// 	for _, db := range cli.Writer {
// 		if existing, ok := c.databases.Get(db); ok {
// 			existing.writers = append(existing.writers, cli.Username)
// 			continue
// 		}

// 		c.databases.Add(&database{
// 			name:    db,
// 			writers: []string{cli.Username},
// 		})
// 	}

// 	for _, db := range cli.Reader {
// 		if existing, ok := c.databases.Get(db); ok {
// 			existing.readers = append(existing.readers, cli.Username)
// 			continue
// 		}

// 		c.databases.Add(&database{
// 			name:    db,
// 			readers: []string{cli.Username},
// 		})
// 	}
// }

// func (c cluster) event(k8s *k8s.Client, eventType, reason, message string) {
// 	k8s.Clusters().Event(context.TODO(), c.cluster, eventType, reason, message)
// }

// func (c cluster) resolve(k8s *k8s.Client) error {
// 	if c.deployment.actual.Ready && !c.cluster.Ready {
// 		c.cluster.Ready = true
// 		c.event(k8s, "Normal", "ClusterReady", "Cluster is ready")
// 		err := k8s.Clusters().UpdateStatus(context.TODO(), c.cluster)
// 		if err != nil {
// 			return fmt.Errorf("failed to update cluster status: %+v", err)
// 		}
// 	}

// 	if !c.cluster.Ready {
// 		return nil
// 	}

// 	if err := c.resolveAdmin(k8s); err != nil {
// 		c.event(k8s, "Warning", "ResolveAdminFailed", fmt.Sprintf("Failed to resolve cluster admin: %+v", err))
// 		return fmt.Errorf("failed to resolve cluster admin: %+v", err)
// 	}

// 	return nil
// }

// func (c cluster) wantsUser(username string) bool {
// 	for _, u := range c.users.List() {
// 		if u.user.Name == username {
// 			return true
// 		}
// 	}

// 	return false
// }

// func (c cluster) resolveAdmin(k8s *k8s.Client) error {
// 	clusterAdmin, err := pgdb.New(c.cluster.Name, c.cluster.Namespace, "postgres", "")
// 	if err != nil {
// 		return fmt.Errorf("failed to create cluster admin: %+v", err)
// 	}
// 	defer clusterAdmin.Stop()

// 	existingDBs, err := clusterAdmin.ListDBs()
// 	if err != nil {
// 		return fmt.Errorf("failed to list databases: %+v", err)
// 	}

// 	for _, db := range existingDBs {
// 		if _, ok := c.databases.Get(db.Name); !ok {
// 			if err := clusterAdmin.DeleteDB(db); err != nil {
// 				return fmt.Errorf("failed to drop database: %+v", err)
// 			}
// 			c.event(k8s, "Normal", "DatabaseDeleted", fmt.Sprintf("Database %s deleted", db.Name))
// 		}
// 	}

// 	existingUsers, err := clusterAdmin.ListUsers()
// 	if err != nil {
// 		return fmt.Errorf("failed to list users: %+v", err)
// 	}

// 	for _, u := range existingUsers {
// 		if !c.wantsUser(u.Name) {
// 			if err := clusterAdmin.DeleteUser(u); err != nil {
// 				return fmt.Errorf("failed to drop user: %+v", err)
// 			}
// 			c.event(k8s, "Normal", "UserDeleted", fmt.Sprintf("User %s deleted", u.Name))
// 		}
// 	}

// 	for _, u := range c.users.List() {
// 		if err := u.resolve(k8s, clusterAdmin); err != nil {
// 			return fmt.Errorf("failed to resolve user: %+v", err)
// 		}
// 	}

// 	for _, db := range c.databases.List() {
// 		if err := db.resolve(k8s, clusterAdmin); err != nil {
// 			return fmt.Errorf("failed to resolve database: %+v", err)
// 		}
// 	}

// 	return nil
// }
