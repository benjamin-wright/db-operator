package manager

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/manager/state"
	"ponglehub.co.uk/db-operator/internal/services/cockroach"
	"ponglehub.co.uk/db-operator/internal/services/k8s/crds"
	"ponglehub.co.uk/db-operator/internal/services/k8s/resources"
	"ponglehub.co.uk/db-operator/internal/services/k8s/utils"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type clients struct {
	cdbs        K8sClient[crds.CockroachDB]
	cclients    K8sClient[crds.CockroachClient]
	cmigrations K8sClient[crds.CockroachMigration]
	csss        K8sClient[resources.CockroachStatefulSet]
	cpvcs       K8sClient[resources.CockroachPVC]
	csvcs       K8sClient[resources.CockroachService]
	csecrets    K8sClient[resources.CockroachSecret]
	rdbs        K8sClient[crds.RedisDB]
	rclients    K8sClient[crds.RedisClient]
	rsss        K8sClient[resources.RedisStatefulSet]
	rpvcs       K8sClient[resources.RedisPVC]
	rsvcs       K8sClient[resources.RedisService]
	rsecrets    K8sClient[resources.RedisSecret]
}

type streams struct {
	cdbs        <-chan k8s_generic.Update[crds.CockroachDB]
	cclients    <-chan k8s_generic.Update[crds.CockroachClient]
	cmigrations <-chan k8s_generic.Update[crds.CockroachMigration]
	csss        <-chan k8s_generic.Update[resources.CockroachStatefulSet]
	cpvcs       <-chan k8s_generic.Update[resources.CockroachPVC]
	csvcs       <-chan k8s_generic.Update[resources.CockroachService]
	csecrets    <-chan k8s_generic.Update[resources.CockroachSecret]
	rdbs        <-chan k8s_generic.Update[crds.RedisDB]
	rclients    <-chan k8s_generic.Update[crds.RedisClient]
	rsss        <-chan k8s_generic.Update[resources.RedisStatefulSet]
	rpvcs       <-chan k8s_generic.Update[resources.RedisPVC]
	rsvcs       <-chan k8s_generic.Update[resources.RedisService]
	rsecrets    <-chan k8s_generic.Update[resources.RedisSecret]
}

type Manager struct {
	namespace string
	ctx       context.Context
	cancel    context.CancelFunc
	clients   clients
	streams   streams
	state     state.State
	debouncer utils.Debouncer
}

type K8sClient[T any] interface {
	Watch(ctx context.Context, cancel context.CancelFunc) (<-chan k8s_generic.Update[T], error)
	Create(ctx context.Context, resource T) error
	Delete(ctx context.Context, name string) error
	Update(ctx context.Context, resource T) error
	Event(ctx context.Context, obj T, eventtype, reason, message string)
}

type CockroachClient interface {
	CreateDB(cockroach.Database) error
}

func New(
	namespace string,
	cdbClient K8sClient[crds.CockroachDB],
	ccClient K8sClient[crds.CockroachClient],
	cmClient K8sClient[crds.CockroachMigration],
	cssClient K8sClient[resources.CockroachStatefulSet],
	cpvcClient K8sClient[resources.CockroachPVC],
	csvcClient K8sClient[resources.CockroachService],
	csecretClient K8sClient[resources.CockroachSecret],
	rdbClient K8sClient[crds.RedisDB],
	rcClient K8sClient[crds.RedisClient],
	rssClient K8sClient[resources.RedisStatefulSet],
	rpvcClient K8sClient[resources.RedisPVC],
	rsvcClient K8sClient[resources.RedisService],
	rsecretClient K8sClient[resources.RedisSecret],
	debouncer time.Duration,
) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	clients := clients{
		cdbs:        cdbClient,
		cclients:    ccClient,
		cmigrations: cmClient,
		csss:        cssClient,
		cpvcs:       cpvcClient,
		csvcs:       csvcClient,
		csecrets:    csecretClient,
		rdbs:        rdbClient,
		rclients:    rcClient,
		rsss:        rssClient,
		rpvcs:       rpvcClient,
		rsvcs:       rsvcClient,
		rsecrets:    rsecretClient,
	}

	cdbs, err := clients.cdbs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach dbs: %+v", err)
	}

	cclients, err := clients.cclients.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach clients: %+v", err)
	}

	cmigrations, err := clients.cmigrations.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach migration: %+v", err)
	}

	csss, err := clients.csss.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach stateful sets: %+v", err)
	}

	cpvcs, err := clients.cpvcs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach persistent volume claims: %+v", err)
	}

	csvcs, err := clients.csvcs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach services: %+v", err)
	}

	csecrets, err := clients.csecrets.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch cockroach secrets: %+v", err)
	}

	rdbs, err := clients.rdbs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis dbs: %+v", err)
	}

	rclients, err := clients.rclients.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis clients: %+v", err)
	}

	rsss, err := clients.rsss.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis stateful sets: %+v", err)
	}

	rpvcs, err := clients.rpvcs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis persistent volume claims: %+v", err)
	}

	rsvcs, err := clients.rsvcs.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis services: %+v", err)
	}

	rsecrets, err := clients.rsecrets.Watch(ctx, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to watch redis secrets: %+v", err)
	}

	streams := streams{
		cdbs:        cdbs,
		cclients:    cclients,
		cmigrations: cmigrations,
		csss:        csss,
		cpvcs:       cpvcs,
		csvcs:       csvcs,
		csecrets:    csecrets,
		rdbs:        rdbs,
		rclients:    rclients,
		rsss:        rsss,
		rpvcs:       rpvcs,
		rsvcs:       rsvcs,
		rsecrets:    rsecrets,
	}

	return &Manager{
		namespace: namespace,
		ctx:       ctx,
		cancel:    cancel,
		clients:   clients,
		streams:   streams,
		state:     state.New(),
		debouncer: utils.NewDebouncer(debouncer),
	}, nil
}

func (m *Manager) Stop() {
	m.cancel()
}

func (m *Manager) Start() {
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				zap.S().Infof("context cancelled, exiting manager loop")
				return
			default:
				m.refresh()
			}
		}
	}()
}

func (m *Manager) refresh() {

	select {
	case <-m.ctx.Done():
	case update := <-m.streams.cdbs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.cclients:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.cmigrations:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rdbs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rclients:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.csss:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.csvcs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.cpvcs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.csecrets:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rdbs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rsss:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rpvcs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rsvcs:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case update := <-m.streams.rsecrets:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case <-m.debouncer.Wait():
		zap.S().Infof("Database state update")
		m.state.RefreshCockroach(m.namespace)
		zap.S().Infof("Processing Started")
		m.processCockroachDBs()
		m.processCockroachClients()
		m.processCockroachMigrations()
		m.processRedisDBs()
		m.processRedisClients()
		zap.S().Infof("Processing Done")
	}
}

func (m *Manager) processCockroachDBs() {
	ssDemand := m.state.GetCSSSDemand()
	svcDemand := m.state.GetCSvcDemand()
	pvcsToRemove := m.state.GetCPVCDemand()

	for _, db := range ssDemand.ToRemove {
		zap.S().Infof("Deleting db: %s", db.Target.Name)
		err := m.clients.csss.Delete(m.ctx, db.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete cockroachdb stateful set: %+v", err)
		}
	}

	for _, svc := range svcDemand.ToRemove {
		zap.S().Infof("Deleting service: %s", svc.Target.Name)
		err := m.clients.csvcs.Delete(m.ctx, svc.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete cockroachdb service: %+v", err)
		}
	}

	for _, pvc := range pvcsToRemove {
		zap.S().Infof("Deleting pvc: %s", pvc.Name)
		err := m.clients.cpvcs.Delete(m.ctx, pvc.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete cockroachdb PVC: %+v", err)
		}
	}

	for _, db := range ssDemand.ToAdd {
		zap.S().Infof("Creating db: %s", db.Target.Name)
		err := m.clients.csss.Create(m.ctx, db.Target)
		if err != nil {
			zap.S().Errorf("Failed to create cockroachdb stateful set: %+v", err)
			m.clients.cdbs.Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", fmt.Sprintf("Failed to create stateful set: %s", err.Error()))
		} else {
			m.clients.cdbs.Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", "Created stateful set")
		}
	}

	for _, svc := range svcDemand.ToAdd {
		zap.S().Infof("Creating service: %s", svc.Target.Name)
		err := m.clients.csvcs.Create(m.ctx, svc.Target)

		if err != nil {
			zap.S().Errorf("Failed to create cockroachdb service: %+v", err)
		}
	}
}

func (m *Manager) processCockroachClients() {
	dbDemand := m.state.GetCDBDemand()
	userDemand := m.state.GetCUserDemand()
	permsDemand := m.state.GetCPermissionDemand()
	secretsDemand := m.state.GetCSecretsDemand()

	dbs := map[string]struct{}{}
	for _, db := range dbDemand.ToAdd {
		dbs[db.Target.DB] = struct{}{}
	}
	for _, db := range dbDemand.ToRemove {
		dbs[db.Target.DB] = struct{}{}
	}
	for _, user := range userDemand.ToAdd {
		dbs[user.Target.DB] = struct{}{}
	}
	for _, user := range userDemand.ToRemove {
		dbs[user.Target.DB] = struct{}{}
	}
	for _, perm := range permsDemand.ToAdd {
		dbs[perm.Target.DB] = struct{}{}
	}
	for _, perm := range permsDemand.ToRemove {
		dbs[perm.Target.DB] = struct{}{}
	}

	for _, secret := range secretsDemand.ToRemove {
		zap.S().Infof("Removing secret %s", secret.Target.Name)
		err := m.clients.csecrets.Delete(m.ctx, secret.Target.Name)
		if err != nil {
			zap.S().Errorf("Failed to delete secret %s: %+v", secret.Target.Name, err)
		}
	}

	for database := range dbs {
		cli, err := cockroach.New(database, m.namespace)
		if err != nil {
			zap.S().Errorf("Failed to create database client for %s: %+v", database, err)
			continue
		}
		defer cli.Stop()

		for _, perm := range permsDemand.ToRemove {
			if perm.Target.DB != database {
				continue
			}

			zap.S().Infof("Dropping permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
			err = cli.RevokePermission(perm.Target)
			if err != nil {
				zap.S().Errorf("Failed to revoke permission: %+v", err)
			}
		}

		for _, db := range dbDemand.ToRemove {
			if db.Target.DB != database {
				continue
			}

			zap.S().Infof("Dropping database %s in db %s", db.Target.Name, db.Target.DB)
			err = cli.DeleteDB(db.Target)
			if err != nil {
				zap.S().Errorf("Failed to delete database: %+v", err)
			}
		}

		for _, user := range userDemand.ToRemove {
			if user.Target.DB != database {
				continue
			}

			zap.S().Infof("Dropping user %s in db %s", user.Target.Name, user.Target.DB)
			err = cli.DeleteUser(user.Target)
			if err != nil {
				zap.S().Errorf("Failed to delete user: %+v", err)
			}
		}

		for _, db := range dbDemand.ToAdd {
			if db.Target.DB != database {
				continue
			}

			zap.S().Infof("Creating database %s in db %s", db.Target.Name, db.Target.DB)

			err := cli.CreateDB(db.Target)
			if err != nil {
				zap.S().Errorf("Failed to create database: %+v", err)
			}
		}

		for _, user := range userDemand.ToAdd {
			if user.Target.DB != database {
				continue
			}

			zap.S().Infof("Creating user %s in db %s", user.Target.Name, user.Target.DB)

			err := cli.CreateUser(user.Target)
			if err != nil {
				zap.S().Errorf("Failed to create user: %+v", err)
			}
		}

		for _, perm := range permsDemand.ToAdd {
			if perm.Target.DB != database {
				continue
			}

			zap.S().Infof("Adding permission for user %s in database %s of db %s", perm.Target.User, perm.Target.Database, perm.Target.DB)
			err := cli.GrantPermission(perm.Target)
			if err != nil {
				zap.S().Errorf("Failed to grant permission: %+v", err)
			}
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		zap.S().Infof("Adding secret %s", secret.Target.Name)
		err := m.clients.csecrets.Create(m.ctx, secret.Target)
		if err != nil {
			zap.S().Errorf("Failed to create secret %s: %+v", secret.Target.Name, err)
		}
	}
}

func (m *Manager) processCockroachMigrations() {
	demand := m.state.GetCMigrationsDemand()

	for _, deployment := range demand.GetDBs() {
		for _, database := range demand.GetDatabases(deployment) {
			if !demand.Next(deployment, database) {
				continue
			}

			client, err := cockroach.NewMigrations(deployment, m.namespace, database)
			if err != nil {
				zap.S().Errorf("Failed to create migrations client: %+v", err)
				continue
			}
			defer client.Stop()

			if ok, err := client.HasMigrationsTable(); err != nil {
				zap.S().Errorf("Failed to check for existing migrations table in %s: %+v", database, err)
				continue
			} else if !ok {
				err = client.CreateMigrationsTable()
				if err != nil {
					zap.S().Errorf("Failed to get create migrations table %s: %+v", database, err)
					continue
				}
			}

			for demand.Next(deployment, database) {
				migration, index := demand.GetNextMigration(deployment, database)

				zap.S().Infof("Running migration %s [%s] %d", deployment, database, index)

				err := client.RunMigration(index, migration)
				if err != nil {
					zap.S().Errorf("Failed to run migration %d: %+v", index, err)
					break
				}
			}
		}
	}
}

func (m *Manager) processRedisDBs() {
	ssDemand := m.state.GetRSSSDemand()
	svcDemand := m.state.GetRSvcDemand()
	pvcsToRemove := m.state.GetRPVCDemand()

	for _, db := range ssDemand.ToRemove {
		zap.S().Infof("Deleting db: %s", db.Target.Name)
		err := m.clients.rsss.Delete(m.ctx, db.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis stateful set: %+v", err)
		}
	}

	for _, svc := range svcDemand.ToRemove {
		zap.S().Infof("Deleting service: %s", svc.Target.Name)
		err := m.clients.rsvcs.Delete(m.ctx, svc.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis service: %+v", err)
		}
	}

	for _, pvc := range pvcsToRemove {
		zap.S().Infof("Deleting pvc: %s", pvc.Name)
		err := m.clients.rpvcs.Delete(m.ctx, pvc.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis PVC: %+v", err)
		}
	}

	for _, db := range ssDemand.ToAdd {
		zap.S().Infof("Creating db: %s", db.Target.Name)
		err := m.clients.rsss.Create(m.ctx, db.Target)

		if err != nil {
			zap.S().Errorf("Failed to create redis stateful set: %+v", err)
		}
	}

	for _, svc := range svcDemand.ToAdd {
		zap.S().Infof("Creating service: %s", svc.Target.Name)
		err := m.clients.rsvcs.Create(m.ctx, svc.Target)

		if err != nil {
			zap.S().Errorf("Failed to create redis service: %+v", err)
		}
	}
}

func (m *Manager) processRedisClients() {
	secretsDemand := m.state.GetRSecretsDemand()

	for _, secret := range secretsDemand.ToRemove {
		zap.S().Infof("Removing secret %s", secret.Target.Name)
		err := m.clients.rsecrets.Delete(m.ctx, secret.Target.Name)
		if err != nil {
			zap.S().Errorf("Failed to delete secret %s: %+v", secret.Target.Name, err)
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		zap.S().Infof("Adding secret %s", secret.Target.Name)
		err := m.clients.rsecrets.Create(m.ctx, secret.Target)
		if err != nil {
			zap.S().Errorf("Failed to create secret %s: %+v", secret.Target.Name, err)
		}
	}
}
