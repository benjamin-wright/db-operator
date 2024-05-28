package manager

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/deployments"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/state"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	clusters    bucket.Bucket[clusters.Resource]
	clients     bucket.Bucket[clients.Resource]
	deployments bucket.Bucket[deployments.Resource]
	services    bucket.Bucket[services.Resource]
	secrets     bucket.Bucket[secrets.Resource]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[clusters.Resource]:
		s.clusters.Apply(u)
	case k8s_generic.Update[clients.Resource]:
		s.clients.Apply(u)
	case k8s_generic.Update[deployments.Resource]:
		s.deployments.Apply(u)
	case k8s_generic.Update[services.Resource]:
		s.services.Apply(u)
	case k8s_generic.Update[secrets.Resource]:
		s.secrets.Apply(u)
	default:
		log.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) GetDeploymentDemand() state.Demand[clusters.Resource, deployments.Resource] {
	return state.GetOneForOne(
		s.clusters,
		s.deployments,
		func(db clusters.Resource) deployments.Resource {
			return deployments.Resource{
				Comparable: deployments.Comparable{
					Name:      db.Name,
					Namespace: db.Namespace,
				},
			}
		},
	)
}

func (s *State) GetServiceDemand() state.Demand[clusters.Resource, services.Resource] {
	return state.GetOneForOne(
		s.clusters,
		s.services,
		func(db clusters.Resource) services.Resource {
			return services.Resource{
				Comparable: services.Comparable{
					Name:      db.Name,
					Namespace: db.Namespace,
				},
			}
		},
	)
}

func (s *State) GetSecretsDemand() state.Demand[clients.Resource, secrets.Resource] {
	return state.GetServiceBound(
		s.clients,
		s.secrets,
		s.deployments,
		func(client clients.Resource) secrets.Resource {
			return secrets.Resource{
				Comparable: secrets.Comparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					Cluster: secrets.Cluster{
						Name:      client.Cluster.Name,
						Namespace: client.Cluster.Namespace,
					},
				},
			}
		},
	)
}
