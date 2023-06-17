package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s"
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/internal/utils"
	"github.com/rs/zerolog/log"
)

type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *k8s.Client
	updates   <-chan any
	state     State
	debouncer utils.Debouncer
}

type WatchFunc func(context.Context, context.CancelFunc, chan<- any) error

func New(
	debounce time.Duration,
) (*Manager, error) {
	client, err := k8s.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach client: %+v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan any)

	for _, f := range []WatchFunc{
		client.DBs().Watch,
		client.Clients().Watch,
		client.StatefulSets().Watch,
		client.PVCs().Watch,
		client.Services().Watch,
		client.Secrets().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		dbs:          bucket.NewBucket[k8s.RedisDB](),
		clients:      bucket.NewBucket[k8s.RedisClient](),
		statefulSets: bucket.NewBucket[k8s.RedisStatefulSet](),
		pvcs:         bucket.NewBucket[k8s.RedisPVC](),
		services:     bucket.NewBucket[k8s.RedisService](),
		secrets:      bucket.NewBucket[k8s.RedisSecret](),
	}

	return &Manager{
		ctx:       ctx,
		cancel:    cancel,
		client:    client,
		updates:   updates,
		state:     state,
		debouncer: utils.NewDebouncer(debounce),
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
				log.Info().Msg("context cancelled, exiting manager loop")
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
	case update := <-m.updates:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case <-m.debouncer.Wait():
		log.Info().Msg("Processing redis started")
		m.processRedisDBs()
		m.processRedisStatefulSets()
		log.Info().Msg("Processing redis finished")
	}
}

func (m *Manager) processRedisDBs() {
	ssDemand := m.state.GetStatefulSetDemand()
	svcDemand := m.state.GetServiceDemand()
	pvcsToRemove := m.state.GetPVCDemand()

	for _, db := range ssDemand.ToRemove {
		log.Info().Msgf("Deleting db: %s/%s", db.Target.Namespace, db.Target.Name)
		err := m.client.StatefulSets().Delete(m.ctx, db.Target.Name, db.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete redis stateful set")
		}
	}

	for _, svc := range svcDemand.ToRemove {
		log.Info().Msgf("Deleting service: %s/%s", svc.Target.Namespace, svc.Target.Name)
		err := m.client.Services().Delete(m.ctx, svc.Target.Name, svc.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete redis service")
		}
	}

	for _, pvc := range pvcsToRemove {
		log.Info().Msgf("Deleting pvc: %s/%s", pvc.Namespace, pvc.Name)
		err := m.client.PVCs().Delete(m.ctx, pvc.Name, pvc.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete redis PVC")
		}
	}

	for _, db := range ssDemand.ToAdd {
		log.Info().Msgf("Creating db: %s", db.Target.Name)
		err := m.client.StatefulSets().Create(m.ctx, db.Target)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create redis stateful set")
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create stateful set: %s", err.Error()))
		} else {
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", "Created stateful set")
		}
	}

	for _, svc := range svcDemand.ToAdd {
		log.Info().Msgf("Creating service: %s/%s", svc.Target.Namespace, svc.Target.Name)
		err := m.client.Services().Create(m.ctx, svc.Target)

		if err != nil {
			log.Error().Err(err).Msg("Failed to create redis service")
		}
	}
}

func (m *Manager) processRedisStatefulSets() {
	secretsDemand := m.state.GetSecretsDemand()

	for _, secret := range secretsDemand.ToRemove {
		log.Info().Msgf("Deleting secret: %s/%s", secret.Target.Namespace, secret.Target.Name)
		err := m.client.Secrets().Delete(m.ctx, secret.Target.Name, secret.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete redis secret")
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		log.Info().Msgf("Creating secret: %s/%s", secret.Target.Namespace, secret.Target.Name)
		err := m.client.Secrets().Create(m.ctx, secret.Target)

		if err != nil {
			log.Error().Err(err).Msg("Failed to create redis secret")
		}
	}
}
