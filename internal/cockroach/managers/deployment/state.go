package deployment

import (
	"github.com/benjamin-wright/db-operator/internal/cockroach/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"go.uber.org/zap"
)

type State struct {
	dbs          state.Bucket[k8s.CockroachDB, *k8s.CockroachDB]
	clients      state.Bucket[k8s.CockroachClient, *k8s.CockroachClient]
	statefulSets state.Bucket[k8s.CockroachStatefulSet, *k8s.CockroachStatefulSet]
	pvcs         state.Bucket[k8s.CockroachPVC, *k8s.CockroachPVC]
	services     state.Bucket[k8s.CockroachService, *k8s.CockroachService]
	secrets      state.Bucket[k8s.CockroachSecret, *k8s.CockroachSecret]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[k8s.CockroachDB]:
		s.dbs.Apply(u)
	case k8s_generic.Update[k8s.CockroachClient]:
		s.clients.Apply(u)
	case k8s_generic.Update[k8s.CockroachStatefulSet]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[k8s.CockroachPVC]:
		s.pvcs.Apply(u)
	case k8s_generic.Update[k8s.CockroachService]:
		s.services.Apply(u)
	case k8s_generic.Update[k8s.CockroachSecret]:
		s.secrets.Apply(u)
	default:
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) GetStatefulSetDemand() state.Demand[k8s.CockroachDB, k8s.CockroachStatefulSet] {
	return state.GetStorageBound(
		s.dbs,
		s.statefulSets,
		func(db k8s.CockroachDB) k8s.CockroachStatefulSet {
			return k8s.CockroachStatefulSet{
				CockroachStatefulSetComparable: k8s.CockroachStatefulSetComparable{
					Name:    db.Name,
					Storage: db.Storage,
				},
			}
		},
	)
}

func (s *State) GetServiceDemand() state.Demand[k8s.CockroachDB, k8s.CockroachService] {
	return state.GetOneForOne(
		s.dbs,
		s.services,
		func(db k8s.CockroachDB) k8s.CockroachService {
			return k8s.CockroachService{
				CockroachServiceComparable: k8s.CockroachServiceComparable{
					Name: db.Name,
				},
			}
		},
	)
}

func (s *State) GetPVCDemand() []k8s.CockroachPVC {
	return state.GetOrphaned(
		s.statefulSets,
		s.pvcs,
		func(ss k8s.CockroachStatefulSet, pvc k8s.CockroachPVC) bool {
			return ss.Name == pvc.Database
		},
	)
}
