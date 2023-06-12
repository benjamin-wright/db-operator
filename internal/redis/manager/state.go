package manager

import (
	"github.com/benjamin-wright/db-operator/internal/redis/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"go.uber.org/zap"
)

type State struct {
	dbs          state.Bucket[k8s.RedisDB, *k8s.RedisDB]
	clients      state.Bucket[k8s.RedisClient, *k8s.RedisClient]
	statefulSets state.Bucket[k8s.RedisStatefulSet, *k8s.RedisStatefulSet]
	pvcs         state.Bucket[k8s.RedisPVC, *k8s.RedisPVC]
	services     state.Bucket[k8s.RedisService, *k8s.RedisService]
	secrets      state.Bucket[k8s.RedisSecret, *k8s.RedisSecret]
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
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) GetStatefulSetDemand() state.Demand[k8s.RedisDB, k8s.RedisStatefulSet] {
	return state.GetOneForOne(
		s.dbs,
		s.statefulSets,
		func(db k8s.RedisDB) k8s.RedisStatefulSet {
			return k8s.RedisStatefulSet{
				RedisStatefulSetComparable: k8s.RedisStatefulSetComparable{
					Name:    db.Name,
					Storage: db.Storage,
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
					Name: db.Name,
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
