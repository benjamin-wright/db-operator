package model

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
)

type Cluster struct {
	Name        string
	Namespace   string
	Users       map[string]*UserData
	Databases   map[string]*DatabaseData
	StatefulSet stateful_sets.Resource
	Service     services.Resource
}

type UserData struct {
	Password string
	Secret   secrets.Resource
}

type DatabaseData struct {
	Owner   string
	Writers []string
	Readers []string
}

func (c *Cluster) GetID() string {
	return c.Name + "@" + c.Namespace
}

func NewDemand(cluster clusters.Resource, clientList []clients.Resource) *Cluster {
	c := &Cluster{
		Name:      cluster.Name,
		Namespace: cluster.Namespace,
		Users:     make(map[string]*UserData),
		Databases: make(map[string]*DatabaseData),
		StatefulSet: stateful_sets.Resource{
			Comparable: stateful_sets.Comparable{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
				Storage:   cluster.Storage,
				Ready:     cluster.Ready,
			},
		},
		Service: services.Resource{
			Comparable: services.Comparable{
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		},
	}

	for _, client := range clientList {
		c.Users[client.Name] = &UserData{
			Secret: secrets.Resource{
				Comparable: secrets.Comparable{
					Name:      client.Name,
					Namespace: client.Namespace,
					Cluster: secrets.Cluster{
						Name:      cluster.Name,
						Namespace: cluster.Namespace,
					},
				},
			},
		}

		if _, ok := c.Databases[client.Database]; !ok {
			c.Databases[client.Database] = &DatabaseData{}
		}

		switch client.Permission {
		case clients.PermissionAdmin:
			c.Databases[client.Database].Owner = client.Username
		case clients.PermissionWrite:
			c.Databases[client.Database].Writers = append(c.Databases[client.Database].Writers, client.Username)
		case clients.PermissionRead:
			c.Databases[client.Database].Readers = append(c.Databases[client.Database].Readers, client.Username)
		}
	}

	return c
}
