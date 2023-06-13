package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/benjamin-wright/db-operator/internal/dbs/redis/k8s"
	"github.com/benjamin-wright/db-operator/internal/state"
	"github.com/benjamin-wright/db-operator/internal/utils"
	"go.uber.org/zap"
)

type Manager struct {
	namespace string
	ctx       context.Context
	cancel    context.CancelFunc
	client    *k8s.Client
	updates   <-chan any
	state     State
	debouncer utils.Debouncer
}

type WatchFunc func(context.Context, context.CancelFunc, chan<- any) error

func New(
	namespace string,
	debounce time.Duration,
) (*Manager, error) {
	client, err := k8s.New(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create cockroach client: %w", err)
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
		dbs:          state.NewBucket[k8s.RedisDB](),
		clients:      state.NewBucket[k8s.RedisClient](),
		statefulSets: state.NewBucket[k8s.RedisStatefulSet](),
		pvcs:         state.NewBucket[k8s.RedisPVC](),
		services:     state.NewBucket[k8s.RedisService](),
		secrets:      state.NewBucket[k8s.RedisSecret](),
	}

	return &Manager{
		namespace: namespace,
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
	case update := <-m.updates:
		m.state.Apply(update)
		m.debouncer.Trigger()
	case <-m.debouncer.Wait():
		zap.S().Infof("Processing Started")
		m.processRedisDBs()
		m.processRedisStatefulSets()
		zap.S().Infof("Processing Done")
	}
}

func (m *Manager) processRedisDBs() {
	ssDemand := m.state.GetStatefulSetDemand()
	svcDemand := m.state.GetServiceDemand()
	pvcsToRemove := m.state.GetPVCDemand()

	for _, db := range ssDemand.ToRemove {
		zap.S().Infof("Deleting db: %s", db.Target.Name)
		err := m.client.StatefulSets().Delete(m.ctx, db.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis stateful set: %+v", err)
		}
	}

	for _, svc := range svcDemand.ToRemove {
		zap.S().Infof("Deleting service: %s", svc.Target.Name)
		err := m.client.Services().Delete(m.ctx, svc.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis service: %+v", err)
		}
	}

	for _, pvc := range pvcsToRemove {
		zap.S().Infof("Deleting pvc: %s", pvc.Name)
		err := m.client.PVCs().Delete(m.ctx, pvc.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis PVC: %+v", err)
		}
	}

	for _, db := range ssDemand.ToAdd {
		zap.S().Infof("Creating db: %s", db.Target.Name)
		err := m.client.StatefulSets().Create(m.ctx, db.Target)
		if err != nil {
			zap.S().Errorf("Failed to create redis stateful set: %+v", err)
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningFailed", fmt.Sprintf("Failed to create stateful set: %s", err.Error()))
		} else {
			m.client.DBs().Event(m.ctx, db.Parent, "Normal", "ProvisioningSucceeded", "Created stateful set")
		}
	}

	for _, svc := range svcDemand.ToAdd {
		zap.S().Infof("Creating service: %s", svc.Target.Name)
		err := m.client.Services().Create(m.ctx, svc.Target)

		if err != nil {
			zap.S().Errorf("Failed to create redis service: %+v", err)
		}
	}
}

func (m *Manager) processRedisStatefulSets() {
	secretsDemand := m.state.GetSecretsDemand()

	for _, secret := range secretsDemand.ToRemove {
		zap.S().Infof("Deleting secret: %s", secret.Target.Name)
		err := m.client.Secrets().Delete(m.ctx, secret.Target.Name)

		if err != nil {
			zap.S().Errorf("Failed to delete redis secret: %+v", err)
		}
	}

	for _, secret := range secretsDemand.ToAdd {
		zap.S().Infof("Creating secret: %s", secret.Target.Name)
		err := m.client.Secrets().Create(m.ctx, secret.Target)

		if err != nil {
			zap.S().Errorf("Failed to create redis secret: %+v", err)
		}
	}
}
