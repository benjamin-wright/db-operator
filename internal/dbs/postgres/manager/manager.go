package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/manager/model"
	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/v2/internal/utils"
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
		return nil, fmt.Errorf("failed to create postgres client: %+v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan any)

	for _, f := range []WatchFunc{
		client.Clusters().Watch,
		client.Clients().Watch,
		client.StatefulSets().Watch,
		client.Secrets().Watch,
		client.Services().Watch,
		client.PVCs().Watch,
	} {
		err := f(ctx, cancel, updates)
		if err != nil {
			return nil, fmt.Errorf("failed to start watch: %+v", err)
		}
	}

	state := State{
		clusters:     bucket.NewBucket[clusters.Resource](),
		clients:      bucket.NewBucket[clients.Resource](),
		statefulSets: bucket.NewBucket[stateful_sets.Resource](),
		secrets:      bucket.NewBucket[secrets.Resource](),
		services:     bucket.NewBucket[services.Resource](),
		pvcs:         bucket.NewBucket[pvcs.Resource](),
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
		log.Debug().Msg("Processing postgres deployments started")
		demand := model.NewModel(m.state.clusters, m.state.clients)
		err := m.resolve(demand)
		if err != nil {
			log.Error().Err(err).Msg("Failed to resolve postgres deployments")
		} else {
			log.Debug().Msg("Processing postgres deployments finished")
		}
	}
}

func (m *Manager) resolve(demand model.Model) error {
	for _, cluster := range demand.Clusters {
		clusterObj, exists := m.state.clusters.Get(cluster.GetID())
		if !exists {
			return fmt.Errorf("cluster %s not found", cluster.GetID())
		}

		statefulset, exists := m.state.statefulSets.Get(cluster.StatefulSet.GetID())
		if !exists {
			err := m.client.StatefulSets().Create(context.TODO(), cluster.StatefulSet)
			if err != nil {
				m.client.Clusters().Event(context.TODO(), clusterObj, "Warning", "CreateFailed", err.Error())
			} else {
				m.client.Clusters().Event(context.TODO(), clusterObj, "Normal", "Created", "StatefulSet created")
			}

			continue
		}

		if statefulset.Ready && !clusterObj.Ready {
			clusterObj.Ready = true
			err := m.client.Clusters().UpdateStatus(context.TODO(), clusterObj)
			if err != nil {
				m.client.Clusters().Event(context.TODO(), clusterObj, "Warning", "StatusUpdateFailed", err.Error())
			} else {
				m.client.Clusters().Event(context.TODO(), clusterObj, "Normal", "DeploymentReady", "Deployment is ready")
			}
		}
	}

	return nil
}
