package deployment

import (
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	clusters     bucket.Bucket[clusters.Resource]
	clients      bucket.Bucket[clients.Resource]
	statefulSets bucket.Bucket[stateful_sets.Resource]
	pvcs         bucket.Bucket[pvcs.Resource]
	services     bucket.Bucket[services.Resource]
	secrets      bucket.Bucket[secrets.Resource]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[clusters.Resource]:
		s.clusters.Apply(u)
	case k8s_generic.Update[clients.Resource]:
		s.clients.Apply(u)
	case k8s_generic.Update[stateful_sets.Resource]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[pvcs.Resource]:
		s.pvcs.Apply(u)
	case k8s_generic.Update[services.Resource]:
		s.services.Apply(u)
	case k8s_generic.Update[secrets.Resource]:
		s.secrets.Apply(u)
	default:
		log.Logger.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}

func (s *State) GetStatefulSetDemand() state.Demand[clusters.Resource, stateful_sets.Resource] {
	return state.GetStorageBound(
		s.clusters,
		s.statefulSets,
		func(cluster clusters.Resource) stateful_sets.Resource {
			return stateful_sets.Resource{
				Comparable: stateful_sets.Comparable{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
					Storage:   cluster.Storage,
				},
			}
		},
	)
}

func (s *State) GetServiceDemand() state.Demand[clusters.Resource, services.Resource] {
	return state.GetOneForOne(
		s.clusters,
		s.services,
		func(cluster clusters.Resource) services.Resource {
			return services.Resource{
				Comparable: services.Comparable{
					Name:      cluster.Name,
					Namespace: cluster.Namespace,
				},
			}
		},
	)
}

func (s *State) GetPVCDemand() []pvcs.Resource {
	return state.GetOrphaned(
		s.statefulSets,
		s.pvcs,
		func(ss stateful_sets.Resource, pvc pvcs.Resource) bool {
			return ss.Name == pvc.Cluster
		},
	)
}
