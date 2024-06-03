package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/redis/manager/model"
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
		clusters:     bucket.NewBucket[clusters.Resource](),
		clients:      bucket.NewBucket[clients.Resource](),
		statefulSets: bucket.NewBucket[stateful_sets.Resource](),
		pvcs:         bucket.NewBucket[pvcs.Resource](),
		services:     bucket.NewBucket[services.Resource](),
		secrets:      bucket.NewBucket[secrets.Resource](),
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
		log.Debug().Msg("Processing redis started")
		demand := model.New(m.state.clusters, m.state.clients)
		m.clean(demand)
		m.resolve(demand)
		log.Debug().Msg("Processing redis finished")
	}
}

func (m *Manager) clean(demand *model.Model) {
	for _, statefulset := range m.state.statefulSets.List() {
		if !demand.Owns(statefulset) {
			log.Info().Str("statefulset", statefulset.Name).Str("namespace", statefulset.Namespace).Msg("Deleting orphaned statefulset")
			err := m.client.StatefulSets().Delete(m.ctx, statefulset.Name, statefulset.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete statefulset")
			}
		}
	}

	for _, pvc := range m.state.pvcs.List() {
		if !demand.Owns(pvc) {
			log.Info().Str("pvc", pvc.Name).Str("namespace", pvc.Namespace).Msg("Deleting orphaned pvc")
			err := m.client.PVCs().Delete(m.ctx, pvc.Name, pvc.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete pvc")
			}
		}
	}

	for _, svc := range m.state.services.List() {
		if !demand.Owns(svc) {
			log.Info().Str("service", svc.Name).Str("namespace", svc.Namespace).Msg("Deleting orphaned service")
			err := m.client.Services().Delete(m.ctx, svc.Name, svc.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete service")
			}
		}
	}

	for _, secret := range m.state.secrets.List() {
		if !demand.Owns(secret) {
			log.Info().Str("secret", secret.Name).Str("namespace", secret.Namespace).Msg("Deleting orphaned secret")
			err := m.client.Secrets().Delete(m.ctx, secret.Name, secret.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete secret")
			}
		}
	}
}

func (m *Manager) resolve(demand *model.Model) {
	for _, cluster := range demand.Clusters {
		statefulset, exists := m.state.statefulSets.Get(cluster.StatefulSet.GetID())
		if !exists {
			log.Info().Str("statefulset", cluster.StatefulSet.Name).Str("namespace", cluster.StatefulSet.Namespace).Msg("Creating statefulset")
			err := m.client.StatefulSets().Create(m.ctx, cluster.StatefulSet)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create statefulset")
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create statefulset: %s", err.Error()))
			} else {
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ProvisioningSucceeded", "Created statefulset")
			}
			continue
		}

		if !statefulset.Ready {
			continue
		}

		_, exists = m.state.services.Get(cluster.Service.GetID())
		if !exists {
			log.Info().Str("service", cluster.Service.Name).Str("namespace", cluster.Service.Namespace).Msg("Creating service")
			err := m.client.Services().Create(m.ctx, cluster.Service)
			if err != nil {
				log.Error().Err(err).Msg("Failed to create service")
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create service: %s", err.Error()))
			} else {
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ProvisioningSucceeded", "Created service")
			}
			continue
		}

		if !cluster.Cluster.Ready {
			cluster.Cluster.Ready = true
			err := m.client.Clusters().UpdateStatus(m.ctx, cluster.Cluster)
			if err != nil {
				log.Error().Err(err).Msg("Failed to update cluster")
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to update cluster: %s", err.Error()))
			} else {
				m.client.Clusters().Event(m.ctx, cluster.Cluster, "Normal", "ClusterReady", "Cluster is ready")
			}
		}

		for _, user := range cluster.Users {
			_, exists := m.state.secrets.Get(user.Secret.GetID())
			if !exists {
				log.Info().Str("secret", user.Secret.Name).Str("namespace", user.Secret.Namespace).Msg("Creating secret")
				err := m.client.Secrets().Create(m.ctx, user.Secret)
				if err != nil {
					log.Error().Err(err).Msg("Failed to create secret")
					m.client.Clients().Event(m.ctx, user.Client, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create secret: %s", err.Error()))
				} else {
					m.client.Clients().Event(m.ctx, user.Client, "Normal", "ProvisioningSucceeded", "Created secret")
				}
				continue
			}

			if !user.Client.Ready {
				user.Client.Ready = true
				err := m.client.Clients().UpdateStatus(m.ctx, user.Client)
				if err != nil {
					log.Error().Err(err).Msg("Failed to update client")
					m.client.Clients().Event(m.ctx, user.Client, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to update client: %s", err.Error()))
				} else {
					m.client.Clients().Event(m.ctx, user.Client, "Normal", "ClientReady", "Client is ready")
				}
			}
		}
	}
}
