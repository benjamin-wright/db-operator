package state

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/internal/services/cockroach"
)

func buildCockroachState(s *State, namespace string) {
	for db := range s.cdbs.state {
		ss, hasSS := s.csss.state[db]
		_, hasSvc := s.csvcs.state[db]
		if !hasSS || !hasSvc || !ss.Ready {
			continue
		}

		cli, err := cockroach.New(db, namespace)
		if err != nil {
			zap.S().Errorf("Failed to create client for database %s: %+v", db, err)
			continue
		}
		defer cli.Stop()

		users, err := cli.ListUsers()
		if err != nil {
			zap.S().Errorf("Failed to list users in %s: %+v", db, err)
			continue
		}

		for _, user := range users {
			s.cusers.add(user)
		}

		names, err := cli.ListDBs()
		if err != nil {
			zap.S().Errorf("Failed to list databases in %s: %+v", db, err)
			continue
		}

		for _, database := range names {
			s.cdatabases.add(database)

			permissions, err := cli.ListPermitted(database)
			if err != nil {
				zap.S().Errorf("Failed to list permissions in %s: %+v", database.Name, err)
				continue
			}

			for _, p := range permissions {
				s.cpermissions.add(p)
			}

			mClient, err := cockroach.NewMigrations(database.DB, namespace, database.Name)
			if err != nil {
				zap.S().Errorf("Failed to get migration client in %s: %+v", database.Name, err)
				continue
			}
			defer mClient.Stop()

			if ok, err := mClient.HasMigrationsTable(); err != nil {
				zap.S().Errorf("Failed to check for existing migrations table in %s: %+v", database.Name, err)
				continue
			} else if !ok {
				err = mClient.CreateMigrationsTable()
				if err != nil {
					zap.S().Errorf("Failed to get create migrations table %s: %+v", database.Name, err)
					continue
				}
			}

			migrations, err := mClient.AppliedMigrations()
			if err != nil {
				zap.S().Errorf("Failed to get migrations for %s: %+v", database.Name, err)
				continue
			}

			for _, migration := range migrations {
				s.capplied.add(migration)
			}
		}
	}
}
