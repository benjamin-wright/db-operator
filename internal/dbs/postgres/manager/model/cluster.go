package model

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
)

type Model struct {
	Clusters map[string]*Cluster
}

func (m Model) Owns(obj interface{}) bool {
	switch obj := obj.(type) {
	case stateful_sets.Resource:
		_, ok := m.Clusters[obj.GetID()]
		return ok
	case services.Resource:
		_, ok := m.Clusters[obj.GetID()]
		return ok
	}

	return false
}

type Cluster struct {
	Name        string
	Namespace   string
	Users       map[string]*UserData
	Databases   map[string]*DatabaseData
	StatefulSet stateful_sets.Resource
	Service     services.Resource
}

type UserData struct {
	ClientID string
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

func NewModel(clusterDemand bucket.Bucket[clusters.Resource], clientDemand bucket.Bucket[clients.Resource]) Model {
	model := Model{
		Clusters: make(map[string]*Cluster),
	}

	for _, cluster := range clusterDemand.List() {
		model.Clusters[cluster.GetID()] = &Cluster{
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
	}

	for _, client := range clientDemand.List() {
		cluster, ok := model.Clusters[client.Cluster.GetID()]
		if !ok {
			continue
		}

		cluster.Users[client.Name] = &UserData{
			ClientID: client.GetID(),
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

		if _, ok := cluster.Databases[client.Database]; !ok {
			cluster.Databases[client.Database] = &DatabaseData{}
		}

		switch client.Permission {
		case clients.PermissionAdmin:
			cluster.Databases[client.Database].Owner = client.Username
		case clients.PermissionWrite:
			cluster.Databases[client.Database].Writers = append(cluster.Databases[client.Database].Writers, client.Username)
		case clients.PermissionRead:
			cluster.Databases[client.Database].Readers = append(cluster.Databases[client.Database].Readers, client.Username)
		}
	}

	return model
}
