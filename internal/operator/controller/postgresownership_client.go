package controller

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type UserOwnership interface {
	Manager() PostgresManager
	Close() error
	Forget() error
}
type userOwnership struct {
	db            *sql.DB
	session       *sql.Conn
	username, uid string
}

func (o *userOwnership) Manager() PostgresManager { return postgresManager{ownership: o} }

func (o *userOwnership) Close() error { _ = o.session.Close(); return o.db.Close() }
func (o *userOwnership) Forget() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := o.session.ExecContext(ctx, "DELETE FROM __dbop_control.credential_owners WHERE username=$1 AND resource_uid=$2", o.username, o.uid)
	return err
}

// AcquireUserOwnership serializes all mutations of a role and establishes its
// exact Kubernetes owner before any policy or password change. Legacy imports
// require a Secret with that same resource UID, checked by the reconciler.
func (p postgresManager) AcquireUserOwnership(host, adminUser, adminPass, username, uid string, generation int64, allowLegacy bool, validate func() error) (UserOwnership, error) {
	if uid == "" || username == adminUser || strings.HasPrefix(username, "__dbop_") {
		return nil, fmt.Errorf("reserved or unowned credential role")
	}
	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return nil, err
	}
	return acquireUserOwnership(db, username, uid, generation, allowLegacy, validate)
}

func acquireUserOwnership(db *sql.DB, username, uid string, generation int64, allowLegacy bool, validate func() error) (*userOwnership, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	claim := &userOwnership{db: db, session: session, username: username, uid: uid}
	fail := func(err error) (*userOwnership, error) { claim.Close(); return nil, err }
	var locked bool
	if err := session.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "credential/"+username).Scan(&locked); err != nil {
		return fail(err)
	}
	if !locked {
		return fail(fmt.Errorf("credential policy mutation is already in progress"))
	}
	if validate == nil {
		return fail(fmt.Errorf("credential mutation fence is required"))
	}
	if err := validate(); err != nil {
		return fail(err)
	}
	if _, err := session.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS __dbop_control; REVOKE ALL ON SCHEMA __dbop_control FROM PUBLIC; CREATE TABLE IF NOT EXISTS __dbop_control.credential_owners(username TEXT PRIMARY KEY,resource_uid TEXT NOT NULL,generation BIGINT NOT NULL DEFAULT 0); ALTER TABLE __dbop_control.credential_owners ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 0; REVOKE ALL ON __dbop_control.credential_owners FROM PUBLIC`); err != nil {
		return fail(err)
	}
	var owner string
	var previousGeneration int64
	err = session.QueryRowContext(ctx, "SELECT resource_uid,generation FROM __dbop_control.credential_owners WHERE username=$1", username).Scan(&owner, &previousGeneration)
	if err == nil {
		if owner != uid {
			return fail(fmt.Errorf("credential role belongs to another resource UID"))
		}
		if previousGeneration > generation {
			return fail(fmt.Errorf("credential generation is stale"))
		}
		if _, err := session.ExecContext(ctx, "UPDATE __dbop_control.credential_owners SET generation=$1 WHERE username=$2 AND resource_uid=$3", generation, username, uid); err != nil {
			return fail(err)
		}
		return claim, nil
	}
	if err != sql.ErrNoRows {
		return fail(err)
	}
	var exists bool
	if err := session.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", username).Scan(&exists); err != nil {
		return fail(err)
	}
	if exists && !allowLegacy {
		return fail(fmt.Errorf("existing credential role has no verified ownership"))
	}
	if _, err := session.ExecContext(ctx, "INSERT INTO __dbop_control.credential_owners(username,resource_uid,generation) VALUES($1,$2,$3)", username, uid, generation); err != nil {
		return fail(err)
	}
	return claim, nil
}
