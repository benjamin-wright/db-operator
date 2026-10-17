package controller

import (
	"fmt"
	"github.com/lib/pq"
)

// ResetUserPolicy disables admission and terminates sessions before removing all
// previous grants. A failed replacement stays disabled until reconciliation succeeds.
func (p postgresManager) ResetUserPolicy(host, adminUser, adminPass, username string) error {
	if username == adminUser || username == migrationsRoleName {
		return fmt.Errorf("reserved credential role")
	}
	db, err := p.open(host, adminUser, adminPass, "postgres")
	if err != nil {
		return err
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", username).Scan(&exists); err != nil {
		return err
	}
	quoted := pq.QuoteIdentifier(username)
	if !exists {
		_, err = db.Exec("CREATE ROLE " + quoted + " NOLOGIN")
		return err
	}
	if _, err := db.Exec("ALTER ROLE " + quoted + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
		return err
	}
	var terminated bool
	if err := db.QueryRow("SELECT COALESCE(bool_and(pg_terminate_backend(pid, 5000)), true) FROM pg_stat_activity WHERE usename = $1 AND pid <> pg_backend_pid()", username).Scan(&terminated); err != nil {
		return err
	}
	if !terminated {
		return fmt.Errorf("credential sessions have not terminated")
	}
	rows, err := db.Query("SELECT rolname FROM pg_roles JOIN pg_auth_members ON pg_roles.oid = roleid WHERE member = (SELECT oid FROM pg_roles WHERE rolname = $1)", username)
	if err != nil {
		return err
	}
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			return err
		}
		roles = append(roles, role)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := db.Exec("REVOKE " + pq.QuoteIdentifier(role) + " FROM " + quoted); err != nil {
			return err
		}
	}
	rows, err = db.Query("SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname")
	if err != nil {
		return err
	}
	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		databases = append(databases, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range databases {
		target, err := p.open(host, adminUser, adminPass, name)
		if err != nil {
			return err
		}
		tx, err := target.Begin()
		if err == nil {
			if _, err = tx.Exec("REASSIGN OWNED BY " + quoted + " TO " + pq.QuoteIdentifier(adminUser)); err == nil {
				_, err = tx.Exec("DROP OWNED BY " + quoted)
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		target.Close()
		if err != nil {
			return fmt.Errorf("revoking credential policy: %w", err)
		}
	}
	return nil
}

func (p postgresManager) EnableUser(host, adminUser, adminPass, username string) error {
	db, err := p.open(host, adminUser, adminPass, "postgres")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("ALTER ROLE " + pq.QuoteIdentifier(username) + " LOGIN")
	return err
}
