package manager

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	dbs          bucket.Bucket[k8s.NatsDB, *k8s.NatsDB]
	clients      bucket.Bucket[k8s.NatsClient, *k8s.NatsClient]
	statefulSets bucket.Bucket[k8s.NatsDeployment, *k8s.NatsDeployment]
	services     bucket.Bucket[k8s.NatsService, *k8s.NatsService]
	secrets      bucket.Bucket[k8s.NatsSecret, *k8s.NatsSecret]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[k8s.NatsDB]:
		s.dbs.Apply(u)
	case k8s_generic.Update[k8s.NatsClient]:
		s.clients.Apply(u)
	case k8s_generic.Update[k8s.NatsDeployment]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[k8s.NatsService]:
		s.services.Apply(u)
	case k8s_generic.Update[k8s.NatsSecret]:
		s.secrets.Apply(u)
	default:
		log.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) GetDeploymentDemand() state.Demand[k8s.NatsDB, k8s.NatsDeployment] {
	return state.GetOneForOne(
		s.dbs,
		s.statefulSets,
		func(db k8s.NatsDB) k8s.NatsDeployment {
			return k8s.NatsDeployment{
				NatsDeploymentComparable: k8s.NatsDeploymentComparable{
					Name:      db.Name,
					Namespace: db.Namespace,
				},
			}
		},
	)
}

func (s *State) GetServiceDemand() state.Demand[k8s.NatsDB, k8s.NatsService] {
	return state.GetOneForOne(
		s.dbs,
		s.services,
		func(db k8s.NatsDB) k8s.NatsService {
			return k8s.NatsService{
				NatsServiceComparable: k8s.NatsServiceComparable{
					Name:      db.Name,
					Namespace: db.Namespace,
				},
			}
		},
	)
}

func (s *State) GetSecretsDemand() state.Demand[k8s.NatsClient, k8s.NatsSecret] {
	return state.GetServiceBound(
		s.clients,
		s.secrets,
		s.statefulSets,
		func(client k8s.NatsClient) k8s.NatsSecret {
			return k8s.NatsSecret{
				NatsSecretComparable: k8s.NatsSecretComparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					DB:        client.DBRef,
				},
			}
		},
	)
}
