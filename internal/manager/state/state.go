package state

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/manager/migrations"
	"ponglehub.co.uk/db-operator/internal/services/cockroach"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type State struct {
	cdbs         bucket[crds.CockroachDB, *crds.CockroachDB]
	cclients     bucket[crds.CockroachClient, *crds.CockroachClient]
	cmigrations  bucket[crds.CockroachMigration, *crds.CockroachMigration]
	csss         bucket[resources.CockroachStatefulSet, *resources.CockroachStatefulSet]
	cpvcs        bucket[resources.CockroachPVC, *resources.CockroachPVC]
	csvcs        bucket[resources.CockroachService, *resources.CockroachService]
	csecrets     bucket[resources.CockroachSecret, *resources.CockroachSecret]
	cdatabases   bucket[cockroach.Database, *cockroach.Database]
	cusers       bucket[cockroach.User, *cockroach.User]
	cpermissions bucket[cockroach.Permission, *cockroach.Permission]
	capplied     bucket[cockroach.Migration, *cockroach.Migration]
	rdbs         bucket[crds.RedisDB, *crds.RedisDB]
	rclients     bucket[crds.RedisClient, *crds.RedisClient]
	rsss         bucket[resources.RedisStatefulSet, *resources.RedisStatefulSet]
	rpvcs        bucket[resources.RedisPVC, *resources.RedisPVC]
	rsvcs        bucket[resources.RedisService, *resources.RedisService]
	rsecrets     bucket[resources.RedisSecret, *resources.RedisSecret]
}

func New() State {
	return State{
		cdbs:         newBucket[crds.CockroachDB](),
		cclients:     newBucket[crds.CockroachClient](),
		cmigrations:  newBucket[crds.CockroachMigration](),
		csss:         newBucket[resources.CockroachStatefulSet](),
		cpvcs:        newBucket[resources.CockroachPVC](),
		csvcs:        newBucket[resources.CockroachService](),
		csecrets:     newBucket[resources.CockroachSecret](),
		cdatabases:   newBucket[cockroach.Database](),
		cusers:       newBucket[cockroach.User](),
		cpermissions: newBucket[cockroach.Permission](),
		capplied:     newBucket[cockroach.Migration](),
		rdbs:         newBucket[crds.RedisDB](),
		rclients:     newBucket[crds.RedisClient](),
		rsss:         newBucket[resources.RedisStatefulSet](),
		rpvcs:        newBucket[resources.RedisPVC](),
		rsvcs:        newBucket[resources.RedisService](),
		rsecrets:     newBucket[resources.RedisSecret](),
	}
}

func (s *State) Apply(update interface{}) {
	switch u := update.(type) {
	case k8s_generic.Update[crds.CockroachDB]:
		s.cdbs.apply(u)
	case k8s_generic.Update[crds.CockroachClient]:
		s.cclients.apply(u)
	case k8s_generic.Update[crds.CockroachMigration]:
		s.cmigrations.apply(u)
	case k8s_generic.Update[resources.CockroachStatefulSet]:
		s.csss.apply(u)
	case k8s_generic.Update[resources.CockroachPVC]:
		s.cpvcs.apply(u)
	case k8s_generic.Update[resources.CockroachService]:
		s.csvcs.apply(u)
	case k8s_generic.Update[resources.CockroachSecret]:
		s.csecrets.apply(u)
	case k8s_generic.Update[cockroach.Database]:
		s.cdatabases.apply(u)
	case k8s_generic.Update[cockroach.User]:
		s.cusers.apply(u)
	case k8s_generic.Update[cockroach.Permission]:
		s.cpermissions.apply(u)
	case k8s_generic.Update[cockroach.Migration]:
		s.capplied.apply(u)
	case k8s_generic.Update[crds.RedisDB]:
		s.rdbs.apply(u)
	case k8s_generic.Update[crds.RedisClient]:
		s.rclients.apply(u)
	case k8s_generic.Update[resources.RedisStatefulSet]:
		s.rsss.apply(u)
	case k8s_generic.Update[resources.RedisPVC]:
		s.rpvcs.apply(u)
	case k8s_generic.Update[resources.RedisService]:
		s.rsvcs.apply(u)
	case k8s_generic.Update[resources.RedisSecret]:
		s.rsecrets.apply(u)
	default:
		zap.S().Errorf("Wat dis? Unknown state update for type %T", u)
	}
}

func (s *State) GetCSSSDemand() Demand[crds.CockroachDB, resources.CockroachStatefulSet] {
	return getStorageBoundDemand(
		s.cdbs.state,
		s.csss.state,
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

func (s *State) GetCSvcDemand() Demand[crds.CockroachDB, resources.CockroachService] {
	return getOneForOneDemand(
		s.cdbs.state,
		s.csvcs.state,
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
	return getOrphanedDemand(
		s.csss.state,
		s.cpvcs.state,
		func(ss resources.CockroachStatefulSet, pvc resources.CockroachPVC) bool {
			return ss.Name == pvc.Database
		},
	)
}

func (s *State) GetCDBDemand() Demand[crds.CockroachClient, cockroach.Database] {
	return getServiceBoundDemand(
		s.cclients.state,
		s.cdatabases.state,
		s.csss.state,
		s.csvcs.state,
		func(client crds.CockroachClient) cockroach.Database {
			return cockroach.Database{
				Name: client.Database,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetCUserDemand() Demand[crds.CockroachClient, cockroach.User] {
	return getServiceBoundDemand(
		s.cclients.state,
		s.cusers.state,
		s.csss.state,
		s.csvcs.state,
		func(client crds.CockroachClient) cockroach.User {
			return cockroach.User{
				Name: client.Username,
				DB:   client.Deployment,
			}
		},
	)
}

func (s *State) GetCPermissionDemand() Demand[crds.CockroachClient, cockroach.Permission] {
	return getServiceBoundDemand(
		s.cclients.state,
		s.cpermissions.state,
		s.csss.state,
		s.csvcs.state,
		func(client crds.CockroachClient) cockroach.Permission {
			return cockroach.Permission{
				User:     client.Username,
				Database: client.Database,
				DB:       client.Deployment,
			}
		},
	)
}

func (s *State) GetCSecretsDemand() Demand[crds.CockroachClient, resources.CockroachSecret] {
	return getServiceBoundDemand(
		s.cclients.state,
		s.csecrets.state,
		s.csss.state,
		s.csvcs.state,
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

func (s *State) RefreshCockroach(namespace string) {
	s.cdatabases.clear()
	s.cusers.clear()
	s.cpermissions.clear()
	s.capplied.clear()

	buildCockroachState(s, namespace)
}

func (s *State) GetCMigrationsDemand() migrations.DBMigrations {
	migrations := migrations.New()

	isReady := func(db string) bool {
		ss, hasSS := s.csss.state[db]
		_, hasSvc := s.csvcs.state[db]

		return hasSvc && hasSS && ss.Ready
	}

	for _, m := range s.cmigrations.state {
		if isReady(m.Deployment) {
			migrations.AddRequest(m.Deployment, m.Database, m.Index, m.Migration)
		}
	}

	for _, m := range s.capplied.state {
		migrations.AddApplied(m.DB, m.Database, m.Index)
	}

	return migrations
}

func (s *State) GetRSSSDemand() Demand[crds.RedisDB, resources.RedisStatefulSet] {
	return getStorageBoundDemand(
		s.rdbs.state,
		s.rsss.state,
		func(db crds.RedisDB) resources.RedisStatefulSet {
			return resources.RedisStatefulSet{
				RedisStatefulSetComparable: resources.RedisStatefulSetComparable{
					Name:    db.Name,
					Storage: db.Storage,
				},
			}
		},
	)
}

func (s *State) GetRSvcDemand() Demand[crds.RedisDB, resources.RedisService] {
	return getOneForOneDemand(
		s.rdbs.state,
		s.rsvcs.state,
		func(db crds.RedisDB) resources.RedisService {
			return resources.RedisService{
				RedisServiceComparable: resources.RedisServiceComparable{
					Name: db.Name,
				},
			}
		},
	)
}

func (s *State) GetRPVCDemand() []resources.RedisPVC {
	return getOrphanedDemand(
		s.rsss.state,
		s.rpvcs.state,
		func(ss resources.RedisStatefulSet, pvc resources.RedisPVC) bool {
			return ss.Name == pvc.Database
		},
	)
}

func (s *State) GetRSecretsDemand() Demand[crds.RedisClient, resources.RedisSecret] {
	return getServiceBoundDemand(
		s.rclients.state,
		s.rsecrets.state,
		s.rsss.state,
		s.rsvcs.state,
		func(client crds.RedisClient) resources.RedisSecret {
			return resources.RedisSecret{
				RedisSecretComparable: resources.RedisSecretComparable{
					Name: client.Secret,
					DB:   client.Deployment,
					Unit: int(client.Unit),
				},
			}
		},
	)
}
