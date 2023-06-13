package manager

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"go.uber.org/zap"
)

type State struct {
	dbs          state.Bucket[k8s.NatsDB, *k8s.NatsDB]
	clients      state.Bucket[k8s.NatsClient, *k8s.NatsClient]
	statefulSets state.Bucket[k8s.NatsDeployment, *k8s.NatsDeployment]
	services     state.Bucket[k8s.NatsService, *k8s.NatsService]
	secrets      state.Bucket[k8s.NatsSecret, *k8s.NatsSecret]
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
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) GetDeploymentDemand() state.Demand[k8s.NatsDB, k8s.NatsDeployment] {
	return state.GetOneForOne(
		s.dbs,
		s.statefulSets,
		func(db k8s.NatsDB) k8s.NatsDeployment {
			return k8s.NatsDeployment{
				NatsDeploymentComparable: k8s.NatsDeploymentComparable{
					Name: db.Name,
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
					Name: db.Name,
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
					Name: client.Secret,
					DB:   client.Deployment,
				},
			}
		},
	)
}
