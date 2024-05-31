package database

import (
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type State struct {
	clients      bucket.Bucket[clients.Resource]
	statefulSets bucket.Bucket[stateful_sets.Resource]
	secrets      bucket.Bucket[secrets.Resource]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[clients.Resource]:
		s.clients.Apply(u)
	case k8s_generic.Update[stateful_sets.Resource]:
		s.statefulSets.Apply(u)
	case k8s_generic.Update[secrets.Resource]:
		s.secrets.Apply(u)
	default:
		log.Logger.Error().Interface("update", u).Msg("wat dis? Unknown state update")
	}
}
