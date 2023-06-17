package deployment

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	dbs          bucket.Bucket[k8s.CockroachDB, *k8s.CockroachDB]
	clients      bucket.Bucket[k8s.CockroachClient, *k8s.CockroachClient]
	statefulSets bucket.Bucket[k8s.CockroachStatefulSet, *k8s.CockroachStatefulSet]
	pvcs         bucket.Bucket[k8s.CockroachPVC, *k8s.CockroachPVC]
	services     bucket.Bucket[k8s.CockroachService, *k8s.CockroachService]
	secrets      bucket.Bucket[k8s.CockroachSecret, *k8s.CockroachSecret]
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
		log.Logger.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) GetStatefulSetDemand() state.Demand[k8s.CockroachDB, k8s.CockroachStatefulSet] {
	return state.GetStorageBound(
		s.dbs,
		s.statefulSets,
		func(db k8s.CockroachDB) k8s.CockroachStatefulSet {
			return k8s.CockroachStatefulSet{
				CockroachStatefulSetComparable: k8s.CockroachStatefulSetComparable{
					Name:      db.Name,
					Namespace: db.Namespace,
					Storage:   db.Storage,
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
					Name:      db.Name,
					Namespace: db.Namespace,
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
