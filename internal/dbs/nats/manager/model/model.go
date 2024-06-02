package model

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/deployments"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
)

type Model struct {
	Clusters map[string]*Cluster
}

type Cluster struct {
	Cluster    clusters.Resource
	Deployment deployments.Resource
	Service    services.Resource
	Users      map[string]*UserData
}

type UserData struct {
	ClientID string
	Secret   secrets.Resource
}

func New(clusterDemand bucket.Bucket[clusters.Resource], clientDemand bucket.Bucket[clients.Resource]) *Model {
	model := &Model{
		Clusters: make(map[string]*Cluster),
	}

	for _, cluster := range clusterDemand.List() {
		model.Clusters[cluster.GetID()] = &Cluster{
			Cluster: cluster,
			Users:   map[string]*UserData{},
		}
	}

	for _, client := range clientDemand.List() {
		cluster, ok := model.Clusters[client.GetClusterID()]
		if !ok {
			continue
		}

		cluster.Users[client.GetID()] = &UserData{
			ClientID: client.GetID(),
			Secret: secrets.Resource{
				Comparable: secrets.Comparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					Cluster: secrets.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
				},
			},
		}
	}

	return model
}

func (m *Model) Owns(obj interface{}) bool {
	switch obj := obj.(type) {
	case deployments.Resource:
		for _, cluster := range m.Clusters {
			if cluster.Deployment.GetID() == obj.GetID() {
				return true
			}
		}
		return false
	case services.Resource:
		for _, cluster := range m.Clusters {
			if cluster.Service.GetID() == obj.GetID() {
				return true
			}
		}
		return false
	case secrets.Resource:
		for _, cluster := range m.Clusters {
			for _, user := range cluster.Users {
				if user.Secret.GetID() == obj.GetID() {
					return true
				}
			}
		}
		return false
	}

	return false
}
