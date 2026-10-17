package controller

import (
	"fmt"

	"github.com/lib/pq"
)

// Migration roles are confined to one logical database. Unexpected dependencies
// elsewhere retain the finalizer rather than broadening cleanup's scope.
func (p postgresManager) dropMigrationUser(database, username, adminUser string) error {
	if p.boundSessions == nil {
		return fmt.Errorf("migration cleanup requires held SQL fences")
	}
	maintenance, err := p.open("", "", "", "postgres")
	if err != nil {
		return err
	}
	defer maintenance.Close()
	var exists bool
	if err := maintenance.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", username).Scan(&exists); err != nil || !exists {
		return err
	}
	quoted := pq.QuoteIdentifier(username)
	if _, err := maintenance.Exec("ALTER ROLE " + quoted + " NOLOGIN"); err != nil {
		return err
	}
	var terminated bool
	if err := maintenance.QueryRow("SELECT COALESCE(bool_and(pg_terminate_backend(pid,5000)),true) FROM pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", username).Scan(&terminated); err != nil {
		return err
	}
	if !terminated {
		return fmt.Errorf("migration sessions have not terminated")
	}
	var unexpected bool
	if err := maintenance.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass
 AND refobjid=(SELECT oid FROM pg_roles WHERE rolname=$1)
 AND (dbid NOT IN (0,COALESCE((SELECT oid FROM pg_database WHERE datname=$2),0))
 OR (dbid=0 AND (classid<>'pg_database'::regclass OR objid<>COALESCE((SELECT oid FROM pg_database WHERE datname=$2),0)))))`, username, database).Scan(&unexpected); err != nil {
		return err
	}
	if unexpected {
		return fmt.Errorf("migration role has dependencies outside its bound database")
	}
	if p.boundSessions[database] != nil {
		target, err := p.open("", "", "", database)
		if err != nil {
			return err
		}
		defer target.Close()
		tx, err := target.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec("REASSIGN OWNED BY " + quoted + " TO " + pq.QuoteIdentifier(adminUser)); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP OWNED BY " + quoted); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err = maintenance.Exec("DROP ROLE " + quoted)
	return err
}
