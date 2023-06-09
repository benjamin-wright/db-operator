package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/nats/k8s"
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
		client.Deployments().Watch,
		client.Services().Watch,
		client.Secrets().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		dbs:          bucket.NewBucket[k8s.NatsDB](),
		clients:      bucket.NewBucket[k8s.NatsClient](),
		statefulSets: bucket.NewBucket[k8s.NatsDeployment](),
		services:     bucket.NewBucket[k8s.NatsService](),
		secrets:      bucket.NewBucket[k8s.NatsSecret](),
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
		log.Info().Msg("Processing nats started")
		m.processNatsDBs()
		m.processNatsDeployments()
		log.Info().Msg("Processing nats finished")
	}
}

func (m *Manager) processNatsDBs() {
	dDemand := m.state.GetDeploymentDemand()
	svcDemand := m.state.GetServiceDemand()

	for _, db := range dDemand.ToRemove {
		log.Info().Msgf("Deleting db: %s/%s", db.Target.Namespace, db.Target.Name)
		err := m.client.Deployments().Delete(m.ctx, db.Target.Name, db.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete nats deployment")
		}
	}

	for _, svc := range svcDemand.ToRemove {
		log.Info().Msgf("Deleting service: %s/%s", svc.Target.Namespace, svc.Target.Name)
		err := m.client.Services().Delete(m.ctx, svc.Target.Name, svc.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete nats service")
		}
	}

	for _, db := range dDemand.ToAdd {
		log.Info().Msgf("Creating db: %s/%s", db.Target.Namespace, db.Target.Name)
		err := m.client.Deployments().Create(m.ctx, db.Target)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create nats deployment")
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create deployment: %s", err.Error()))
		} else {
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", "Created deployment")
		}
	}

	for _, svc := range svcDemand.ToAdd {
		log.Info().Msgf("Creating service: %s/%s", svc.Target.Namespace, svc.Target.Name)
		err := m.client.Services().Create(m.ctx, svc.Target)

		if err != nil {
			log.Error().Err(err).Msg("Failed to create nats service")
		}
	}
}

func (m *Manager) processNatsDeployments() {
	secretsDemand := m.state.GetSecretsDemand()

	for _, secret := range secretsDemand.ToRemove {
		log.Info().Msgf("Deleting secret: %s/%s", secret.Target.Namespace, secret.Target.Name)
		err := m.client.Secrets().Delete(m.ctx, secret.Target.Name, secret.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msg("Failed to delete nats secret")
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		log.Info().Msgf("Creating secret: %s/%s", secret.Target.Namespace, secret.Target.Name)
		err := m.client.Secrets().Create(m.ctx, secret.Target)

		if err != nil {
			log.Error().Err(err).Msg("Failed to create nats secret")
		}
	}
}
