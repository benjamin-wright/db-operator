package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/cockroach/k8s"
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
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		dbs:          bucket.NewBucket[k8s.CockroachDB](),
		clients:      bucket.NewBucket[k8s.CockroachClient](),
		statefulSets: bucket.NewBucket[k8s.CockroachStatefulSet](),
		pvcs:         bucket.NewBucket[k8s.CockroachPVC](),
		services:     bucket.NewBucket[k8s.CockroachService](),
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
		log.Info().Msg("Processing cockroach deployments started")
		m.processCockroachDBs()
		log.Info().Msg("Processing cockroach deployments finished")
	}
}

func (m *Manager) processCockroachDBs() {
	ssDemand := m.state.GetStatefulSetDemand()
	svcDemand := m.state.GetServiceDemand()
	pvcsToRemove := m.state.GetPVCDemand()

	for _, db := range ssDemand.ToRemove {
		log.Info().Msgf("Deleting db: %s", db.Target.Name)
		err := m.client.StatefulSets().Delete(m.ctx, db.Target.Name, db.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msgf("Failed to delete cockroachdb stateful set: %+v", err)
		}
	}

	for _, svc := range svcDemand.ToRemove {
		log.Info().Msgf("Deleting service: %s", svc.Target.Name)
		err := m.client.Services().Delete(m.ctx, svc.Target.Name, svc.Target.Namespace)

		if err != nil {
			log.Error().Err(err).Msgf("Failed to delete cockroachdb service: %+v", err)
		}
	}

	for _, pvc := range pvcsToRemove {
		log.Info().Msgf("Deleting pvc: %s", pvc.Name)
		err := m.client.PVCs().Delete(m.ctx, pvc.Name, pvc.Namespace)

		if err != nil {
			log.Error().Err(err).Msgf("Failed to delete cockroachdb PVC: %+v", err)
		}
	}

	for _, db := range ssDemand.ToAdd {
		log.Info().Msgf("Creating db: %s", db.Target.Name)
		err := m.client.StatefulSets().Create(m.ctx, db.Target)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to create cockroachdb stateful set: %+v", err)
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create stateful set: %s", err.Error()))
		} else {
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", "Created stateful set")
		}
	}

	for _, svc := range svcDemand.ToAdd {
		log.Info().Msgf("Creating service: %s", svc.Target.Name)
		err := m.client.Services().Create(m.ctx, svc.Target)

		if err != nil {
			log.Error().Err(err).Msgf("Failed to create cockroachdb service: %+v", err)
		}
	}
}
