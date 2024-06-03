package manager

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
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
		log.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}
