package manager

import (
	"context"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/manager/model"
	"github.com/rs/zerolog/log"
)

func (m *Manager) clean(demand model.Model) error {
	for _, ss := range m.state.statefulSets.List() {
		if !demand.Owns(ss) {
			err := m.client.StatefulSets().Delete(context.TODO(), ss.Name, ss.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres statefulset")
			}
		}
	}

	for _, service := range m.state.services.List() {
		if !demand.Owns(service) {
			err := m.client.Services().Delete(context.TODO(), service.Name, service.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres service")
			}
		}
	}

	for _, secret := range m.state.secrets.List() {
		if !demand.Owns(secret) {
			err := m.client.Secrets().Delete(context.TODO(), secret.Name, secret.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres client secret")
			}
		}
	}

	for _, pvc := range m.state.pvcs.List() {
		if !demand.Owns(pvc) {
			err := m.client.PVCs().Delete(context.TODO(), pvc.Name, pvc.Namespace)
			if err != nil {
				log.Error().Err(err).Msg("Failed to delete orphaned postgres pvc")
			}
		}

	}

	return nil
}
