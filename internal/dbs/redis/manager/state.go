package manager

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	dbs          bucket.Bucket[k8s.RedisDB, *k8s.RedisDB]
	clients      bucket.Bucket[k8s.RedisClient, *k8s.RedisClient]
	statefulSets bucket.Bucket[k8s.RedisStatefulSet, *k8s.RedisStatefulSet]
	pvcs         bucket.Bucket[k8s.RedisPVC, *k8s.RedisPVC]
	services     bucket.Bucket[k8s.RedisService, *k8s.RedisService]
	secrets      bucket.Bucket[k8s.RedisSecret, *k8s.RedisSecret]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[k8s.RedisDB]:
		s.dbs.Apply(u)
	case k8s_generic.Update[k8s.RedisClient]:
		s.clients.Apply(u)
	case k8s_generic.Update[k8s.RedisStatefulSet]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[k8s.RedisPVC]:
		s.pvcs.Apply(u)
	case k8s_generic.Update[k8s.RedisService]:
		s.services.Apply(u)
	case k8s_generic.Update[k8s.RedisSecret]:
		s.secrets.Apply(u)
	default:
		log.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) GetStatefulSetDemand() state.Demand[k8s.RedisDB, k8s.RedisStatefulSet] {
	return state.GetOneForOne(
		s.dbs,
		s.statefulSets,
		func(db k8s.RedisDB) k8s.RedisStatefulSet {
			return k8s.RedisStatefulSet{
				RedisStatefulSetComparable: k8s.RedisStatefulSetComparable{
					Name:      db.Name,
					Namespace: db.Namespace,
					Storage:   db.Storage,
				},
			}
		},
	)
}

func (s *State) GetServiceDemand() state.Demand[k8s.RedisDB, k8s.RedisService] {
	return state.GetOneForOne(
		s.dbs,
		s.services,
		func(db k8s.RedisDB) k8s.RedisService {
			return k8s.RedisService{
				RedisServiceComparable: k8s.RedisServiceComparable{
					Name:      db.Name,
					Namespace: db.Namespace,
				},
			}
		},
	)
}

func (s *State) GetPVCDemand() []k8s.RedisPVC {
	return state.GetOrphaned(
		s.statefulSets,
		s.pvcs,
		func(ss k8s.RedisStatefulSet, pvc k8s.RedisPVC) bool {
			return ss.Name == pvc.Database
		},
	)
}

func (s *State) GetSecretsDemand() state.Demand[k8s.RedisClient, k8s.RedisSecret] {
	return state.GetServiceBound(
		s.clients,
		s.secrets,
		s.statefulSets,
		func(client k8s.RedisClient) k8s.RedisSecret {
			return k8s.RedisSecret{
				RedisSecretComparable: k8s.RedisSecretComparable{
					Name:      client.Secret,
					Namespace: client.Namespace,
					DB:        client.DBRef,
				},
			}
		},
	)
}
