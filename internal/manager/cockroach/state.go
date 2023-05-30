package cockroach

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/services/cockroach"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
	"ponglehub.co.uk/db-operator/internal/state"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type State struct {
	cdbs         state.Bucket[crds.CockroachDB, *crds.CockroachDB]
	cclients     state.Bucket[crds.CockroachClient, *crds.CockroachClient]
	csss         state.Bucket[resources.CockroachStatefulSet, *resources.CockroachStatefulSet]
	cpvcs        state.Bucket[resources.CockroachPVC, *resources.CockroachPVC]
	csvcs        state.Bucket[resources.CockroachService, *resources.CockroachService]
	csecrets     state.Bucket[resources.CockroachSecret, *resources.CockroachSecret]
	cdatabases   state.Bucket[cockroach.Database, *cockroach.Database]
	cusers       state.Bucket[cockroach.User, *cockroach.User]
	cpermissions state.Bucket[cockroach.Permission, *cockroach.Permission]
	capplied     state.Bucket[cockroach.Migration, *cockroach.Migration]
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[crds.CockroachDB]:
		s.cdbs.Apply(u)
	case k8s_generic.Update[crds.CockroachClient]:
		s.cclients.Apply(u)
	case k8s_generic.Update[resources.CockroachStatefulSet]:
		s.csss.Apply(u)
	case k8s_generic.Update[resources.CockroachPVC]:
		s.cpvcs.Apply(u)
	case k8s_generic.Update[resources.CockroachService]:
		s.csvcs.Apply(u)
	case k8s_generic.Update[resources.CockroachSecret]:
		s.csecrets.Apply(u)
	case k8s_generic.Update[cockroach.Database]:
		s.cdatabases.Apply(u)
	case k8s_generic.Update[cockroach.User]:
		s.cusers.Apply(u)
	case k8s_generic.Update[cockroach.Permission]:
		s.cpermissions.Apply(u)
	case k8s_generic.Update[cockroach.Migration]:
		s.capplied.Apply(u)
	default:
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) GetCSSSDemand() state.Demand[crds.CockroachDB, resources.CockroachStatefulSet] {
	return state.GetStorageBound(
		s.cdbs,
		s.csss,
		func(db crds.CockroachDB) resources.CockroachStatefulSet {
			return resources.CockroachStatefulSet{
				CockroachStatefulSetComparable: resources.CockroachStatefulSetComparable{
					Name:    db.Name,
					Storage: db.Storage,
				},
			}
		},
	)
}

func (s *State) GetCSvcDemand() state.Demand[crds.CockroachDB, resources.CockroachService] {
	return state.GetOneForOne(
		s.cdbs,
		s.csvcs,
		func(db crds.CockroachDB) resources.CockroachService {
			return resources.CockroachService{
				CockroachServiceComparable: resources.CockroachServiceComparable{
					Name: db.Name,
				},
			}
		},
	)
}

func (s *State) GetCPVCDemand() []resources.CockroachPVC {
	return state.GetOrphaned(
		s.csss,
		s.cpvcs,
		func(ss resources.CockroachStatefulSet, pvc resources.CockroachPVC) bool {
			return ss.Name == pvc.Database
		},
	)
}

func (s *State) GetCDBDemand() state.Demand[crds.CockroachClient, cockroach.Database] {
	return state.GetServiceBound(
		s.cclients,
		s.cdatabases,
		s.csss,
		s.csvcs,
		func(client crds.CockroachClient) cockroach.Database {
			return cockroach.Database{
				Name: client.Database,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetCUserDemand() state.Demand[crds.CockroachClient, cockroach.User] {
	return state.GetServiceBound(
		s.cclients,
		s.cusers,
		s.csss,
		s.csvcs,
		func(client crds.CockroachClient) cockroach.User {
			return cockroach.User{
				Name: client.Username,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetCPermissionDemand() state.Demand[crds.CockroachClient, cockroach.Permission] {
	return state.GetServiceBound(
		s.cclients,
		s.cpermissions,
		s.csss,
		s.csvcs,
		func(client crds.CockroachClient) cockroach.Permission {
			return cockroach.Permission{
				User:     client.Username,
				Database: client.Database,
				DB:       client.Deployment,
			}
		},
	)
}

func (s *State) GetCSecretsDemand() state.Demand[crds.CockroachClient, resources.CockroachSecret] {
	return state.GetServiceBound(
		s.cclients,
		s.csecrets,
		s.csss,
		s.csvcs,
		func(client crds.CockroachClient) resources.CockroachSecret {
			return resources.CockroachSecret{
				CockroachSecretComparable: resources.CockroachSecretComparable{
					Name:     client.Secret,
					DB:       client.Deployment,
					Database: client.Database,
					User:     client.Username,
				},
			}
		},
	)
}
