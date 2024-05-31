package database

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/secrets"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/stateful_sets"
// 	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
// 	"github.com/benjamin-wright/db-operator/v2/internal/utils"
// 	"github.com/rs/zerolog/log"
// )

// type Manager struct {
// 	ctx       context.Context
// 	cancel    context.CancelFunc
// 	client    *k8s.Client
// 	updates   chan any
// 	state     State
// 	debouncer utils.Debouncer
// }

// type WatchFunc func(context.Context, context.CancelFunc, chan<- any) error

// func New(
// 	debounce time.Duration,
// ) (*Manager, error) {
// 	client, err := k8s.New()
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create postgres client: %+v", err)
// 	}

// 	ctx, cancel := context.WithCancel(context.Background())
// 	updates := make(chan any)

// 	for _, f := range []WatchFunc{
// 		client.Clients().Watch,
// 		client.StatefulSets().Watch,
// 		client.Secrets().Watch,
// 	} {
// 		err := f(ctx, cancel, updates)
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to start watch: %+v", err)
// 		}
// 	}

// 	state := State{
// 		clients:      bucket.NewBucket[clients.Resource](),
// 		statefulSets: bucket.NewBucket[stateful_sets.Resource](),
// 		secrets:      bucket.NewBucket[secrets.Resource](),
// 	}

// 	return &Manager{
// 		ctx:       ctx,
// 		cancel:    cancel,
// 		client:    client,
// 		updates:   updates,
// 		state:     state,
// 		debouncer: utils.NewDebouncer(debounce),
// 	}, nil
// }

// func (m *Manager) Stop() {
// 	m.cancel()
// }

// func (m *Manager) Start() {
// 	go func() {
// 		for {
// 			select {
// 			case <-m.ctx.Done():
// 				log.Info().Msg("context cancelled, exiting manager loop")
// 				return
// 			default:
// 				m.refresh()
// 			}
// 		}
// 	}()
// }

// func (m *Manager) refresh() {
// 	select {
// 	case <-m.ctx.Done():
// 	case update := <-m.updates:
// 		m.state.Apply(update)
// 		m.debouncer.Trigger()
// 	case <-m.debouncer.Wait():
// 		log.Debug().Msg("Updating postgres database state")
// 		m.refreshDatabaseState()
// 		log.Debug().Msg("Processing postgres databases started")
// 		m.processPostgresClients()
// 		log.Debug().Msg("Processing postgres databases finished")
// 	}
// }
