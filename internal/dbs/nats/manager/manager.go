package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/deployments"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/manager/model"
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
		clusters:    bucket.NewBucket[clusters.Resource](),
		clients:     bucket.NewBucket[clients.Resource](),
		deployments: bucket.NewBucket[deployments.Resource](),
		services:    bucket.NewBucket[services.Resource](),
		secrets:     bucket.NewBucket[secrets.Resource](),
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
		log.Debug().Msg("Processing nats started")
		demand := model.New(m.state.clusters, m.state.clients)
		m.clean(demand)
		m.resolve(demand)
		log.Debug().Msg("Processing nats finished")
	}
}

func (m *Manager) clean(demand *model.Model) {
	for _, deployment := range m.state.deployments.List() {
		if !demand.Owns(deployment) {
			log.Info().Msgf("Deleting deployment: %s/%s", deployment.Namespace, deployment.Name)
			err := m.client.Deployments().Delete(m.ctx, deployment.Name, deployment.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned nats deployment")
			}
		}
	}

	for _, service := range m.state.services.List() {
		if !demand.Owns(service) {
			log.Info().Msgf("Deleting service: %s/%s", service.Namespace, service.Name)
			err := m.client.Services().Delete(m.ctx, service.Name, service.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned nats service")
			}
		}
	}

	for _, secret := range m.state.secrets.List() {
		if !demand.Owns(secret) {
			log.Info().Msgf("Deleting secret: %s/%s", secret.Namespace, secret.Name)
			err := m.client.Secrets().Delete(m.ctx, secret.Name, secret.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned nats secret")
			}
		}
	}
}

func (m *Manager) resolve(demand *model.Model) {
	for _, cluster := range demand.Clusters {
		deployment, exists := m.state.deployments.Get(cluster.Deployment.GetID())
		if !exists {
			log.Info().Str("cluster", cluster.Cluster.Name).Str("deployment", cluster.Deployment.Name).Msg("Creating nats deployment")
			err := m.client.Deployments().Create(m.ctx, cluster.Deployment)
			if err != nil {
				log.Error().Str("cluster", cluster.Cluster.Name).Str("deployment", cluster.Deployment.Name).Err(err).Msg("Failed to create nats deployment")
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Warning", "DeploymentProvisioningFailed", fmt.Sprintf("Failed to create deployment: %s", err.Error()))
			} else {
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Normal", "DeploymentProvisioningSucceeded", "Created deployment")
			}
			continue
		}

		if !deployment.Ready {
			continue
		}

		_, exists = m.state.services.Get(cluster.Service.GetID())
		if !exists {
			log.Info().Str("cluster", cluster.Cluster.Name).Str("service", cluster.Service.Name).Msg("Creating nats service")
			err := m.client.Services().Create(m.ctx, cluster.Service)
			if err != nil {
				log.Error().Str("cluster", cluster.Cluster.Name).Str("service", cluster.Service.Name).Err(err).Msg("Failed to create nats service")
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Warning", "ServiceProvisioningFailed", fmt.Sprintf("Failed to create service: %s", err.Error()))
			} else {
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Normal", "ServiceProvisioningSucceeded", "Created service")
			}
		}

		if !cluster.Cluster.Ready {
			cluster.Cluster.Ready = true
			err := m.client.Clusters().UpdateStatus(context.TODO(), cluster.Cluster)
			if err != nil {
				log.Error().Str("cluster", cluster.Cluster.Name).Err(err).Msg("Failed to update nats cluster status")
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Warning", "ClusterStatusUpdateFailed", fmt.Sprintf("Failed to update cluster status: %s", err.Error()))
			} else {
				m.client.Clusters().Event(context.TODO(), cluster.Cluster, "Normal", "ClusterReady", "Deployment is ready")
			}
		}

		for _, user := range cluster.Users {
			_, exists := m.state.secrets.Get(user.Secret.GetID())
			if !exists {
				log.Info().Str("cluster", cluster.Cluster.Name).Str("user", user.Client.Name).Msg("Creating nats secret")
				err := m.client.Secrets().Create(m.ctx, user.Secret)
				if err != nil {
					log.Error().Str("cluster", cluster.Cluster.Name).Str("user", user.Client.Name).Err(err).Msg("Failed to create nats secret")
					m.client.Clients().Event(context.TODO(), user.Client, "Warning", "SecretProvisioningFailed", fmt.Sprintf("Failed to create secret: %s", err.Error()))
				} else {
					m.client.Clients().Event(context.TODO(), user.Client, "Normal", "SecretProvisioningSucceeded", "Created secret")
				}
			}

			if !user.Client.Ready {
				user.Client.Ready = true
				err := m.client.Clients().UpdateStatus(context.TODO(), user.Client)
				if err != nil {
					log.Error().Str("cluster", cluster.Cluster.Name).Str("user", user.Client.Name).Err(err).Msg("Failed to update nats client status")
					m.client.Clients().Event(context.TODO(), user.Client, "Warning", "ClientStatusUpdateFailed", fmt.Sprintf("Failed to update client status: %s", err.Error()))
				} else {
					m.client.Clients().Event(context.TODO(), user.Client, "Normal", "ClientReady", "Client is ready")
				}
			}
		}
	}
}
