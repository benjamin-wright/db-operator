package model

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
)

type Model struct {
	Clusters []*Cluster
}

type Cluster struct {
	Cluster clusters.Resource

	StatefulSet stateful_sets.Resource
	Service     services.Resource
	Users       []*UserData
}

type UserData struct {
	Client clients.Resource
	Secret secrets.Resource
}

func New(clusterDemand bucket.Bucket[clusters.Resource], clientsDemand bucket.Bucket[clients.Resource]) *Model {
	model := &Model{
		Clusters: []*Cluster{},
	}

	for _, cluster := range clusterDemand.List() {
		model.Clusters = append(model.Clusters, &Cluster{
			Cluster: cluster,
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
			Users: []*UserData{},
		})
	}

	for _, client := range clientsDemand.List() {
		for _, cluster := range model.Clusters {
			if client.GetClusterID() == cluster.Cluster.GetID() {
				cluster.Users = append(cluster.Users, &UserData{
					Client: client,
					Secret: secrets.Resource{
						Comparable: secrets.Comparable{
							Name:      client.Secret,
							Namespace: client.Namespace,
							Cluster: secrets.Cluster{
								Name:      cluster.Cluster.Name,
								Namespace: cluster.Cluster.Namespace,
							},
							Unit: client.Unit,
						},
					},
				})
			}
		}
	}

	return model
}

func (m *Model) Owns(obj interface{}) bool {
	switch obj := obj.(type) {
	case stateful_sets.Resource:
		for _, cluster := range m.Clusters {
			if cluster.StatefulSet.GetID() == obj.GetID() {
				return true
			}
		}
	case services.Resource:
		for _, cluster := range m.Clusters {
			if cluster.Service.GetID() == obj.GetID() {
				return true
			}
		}
	case pvcs.Resource:
		for _, cluster := range m.Clusters {
			if cluster.Cluster.Name == obj.Database && cluster.Cluster.Namespace == obj.Namespace {
				return true
			}
		}
	case secrets.Resource:
		for _, cluster := range m.Clusters {
			for _, user := range cluster.Users {
				if user.Secret.GetID() == obj.GetID() {
					return true
				}
			}
		}
	}

	return false
}
