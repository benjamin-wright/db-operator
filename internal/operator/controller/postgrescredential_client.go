package controller

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	// Pure Go Postgres driver.
	"github.com/lib/pq"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// postgresCredentialClient encapsulates all Kubernetes API interactions for the
// PostgresCredentialReconciler. The scheme is required to set owner references on
// created objects.
type postgresCredentialClient struct {
	inner  client.Client
	scheme *runtime.Scheme
}

func (c *postgresCredentialClient) get(ctx context.Context, key client.ObjectKey, obj client.Object) (bool, error) {
	if err := c.inner.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// createOwned sets a controller owner reference on obj then creates it in the cluster.
func (c *postgresCredentialClient) createOwned(ctx context.Context, owner, obj client.Object) error {
	_ = controllerutil.SetControllerReference(owner, obj, c.scheme)
	return c.inner.Create(ctx, obj)
}

func (c *postgresCredentialClient) update(ctx context.Context, obj client.Object) error {
	return c.inner.Update(ctx, obj)
}

// delete removes obj from the cluster. A not-found error is treated as success.
func (c *postgresCredentialClient) delete(ctx context.Context, obj client.Object) error {
	if err := c.inner.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *postgresCredentialClient) updateStatus(ctx context.Context, obj client.Object) error {
	return c.inner.Status().Update(ctx, obj)
}

func (c *postgresCredentialClient) list(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	return c.inner.List(ctx, obj, opts...)
}

// ────────────────────────────────────────────────────────────────────────────
// PostgresManager — external Postgres dependency interface
// ────────────────────────────────────────────────────────────────────────────

// PostgresManager abstracts direct Postgres interactions so the reconciler can
// be tested without a live database.
type PostgresManager interface {
	EnsureDatabase(host, adminUser, adminPass, dbName string) error
	EnsureUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission) error
	DropUser(host, adminUser, adminPass, dbName, username string) error
	// EnsureOwner makes username the owner of dbName and grants it full schema access.
	EnsureOwner(host, adminUser, adminPass, dbName, username string) error
	// FindOwner returns the current PostgreSQL owner role of dbName, or an empty
	// string if the database does not exist.
	FindOwner(host, adminUser, adminPass, dbName string) (string, error)
}

// postgresManager is the production implementation of PostgresManager.
type postgresManager struct{}

// validPermissions is the exhaustive set of SQL privilege keywords the manager
// may embed in DDL statements. Every permission is checked against this set
// before being interpolated into a query so that only known-safe keywords reach SQL.
var validPermissions = map[v1alpha1.DatabasePermission]struct{}{
	v1alpha1.PermissionSelect:     {},
	v1alpha1.PermissionInsert:     {},
	v1alpha1.PermissionUpdate:     {},
	v1alpha1.PermissionDelete:     {},
	v1alpha1.PermissionTruncate:   {},
	v1alpha1.PermissionReferences: {},
	v1alpha1.PermissionTrigger:    {},
	v1alpha1.PermissionAll:        {},
}

// EnsureDatabase connects to the maintenance database and creates the specified
// logical database if it does not already exist.
func (p postgresManager) EnsureDatabase(host, adminUser, adminPass, dbName string) error {
	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists); err != nil {
		return fmt.Errorf("checking if database exists: %w", err)
	}

	if !exists {
		createSQL := fmt.Sprintf("CREATE DATABASE %s", pq.QuoteIdentifier(dbName))
		if _, err := db.Exec(createSQL); err != nil {
			return fmt.Errorf("creating database %q: %w", dbName, err)
		}
	}

	return nil
}

// EnsureUser connects to the target Postgres instance and creates the specified role
// with the given password and permissions if it does not already exist.
func (p postgresManager) EnsureUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission) error {
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", username).Scan(&exists); err != nil {
		return fmt.Errorf("checking if role exists: %w", err)
	}

	if !exists {
		createSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s",
			pq.QuoteIdentifier(username), pq.QuoteLiteral(password))
		if _, err := db.Exec(createSQL); err != nil {
			return fmt.Errorf("creating role %q: %w", username, err)
		}
	}

	if len(permissions) > 0 {
		privs := make([]string, len(permissions))
		for i, p := range permissions {
			if _, ok := validPermissions[p]; !ok {
				return fmt.Errorf("unknown permission %q", p)
			}
			privs[i] = string(p)
		}
		privClause := strings.Join(privs, ", ")
		quotedUser := pq.QuoteIdentifier(username)

		grantSQL := fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA public TO %s", privClause, quotedUser)
		if _, err := db.Exec(grantSQL); err != nil {
			return fmt.Errorf("granting permissions to %q: %w", username, err)
		}

		defaultSQL := fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT %s ON TABLES TO %s", privClause, quotedUser)
		if _, err := db.Exec(defaultSQL); err != nil {
			return fmt.Errorf("setting default privileges for %q: %w", username, err)
		}

		// Propagate default privileges for the current database owner so that tables
		// and sequences created by the owner after this grant are auto-granted to this user.
		var owner string
		if err := db.QueryRow(
			"SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1", dbName,
		).Scan(&owner); err != nil {
			return fmt.Errorf("looking up owner of database %q: %w", dbName, err)
		}
		if owner != "" && owner != username {
			quotedOwner := pq.QuoteIdentifier(owner)
			ownerTableSQL := fmt.Sprintf(
				"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT %s ON TABLES TO %s",
				quotedOwner, privClause, quotedUser)
			if _, err := db.Exec(ownerTableSQL); err != nil {
				return fmt.Errorf("setting owner-scoped default table privileges for %q: %w", username, err)
			}

			// Sequences only accept SELECT, UPDATE, and ALL — filter out table-only
			// privileges (INSERT, DELETE, TRUNCATE, REFERENCES, TRIGGER) before
			// building the sequences grant.
			validSeqPerms := map[v1alpha1.DatabasePermission]bool{
				v1alpha1.PermissionSelect: true,
				v1alpha1.PermissionUpdate: true,
				v1alpha1.PermissionAll:    true,
			}
			var seqPrivs []string
			for _, perm := range permissions {
				if validSeqPerms[perm] {
					seqPrivs = append(seqPrivs, string(perm))
				}
			}
			if len(seqPrivs) > 0 {
				seqPrivClause := strings.Join(seqPrivs, ", ")
				ownerSeqSQL := fmt.Sprintf(
					"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT %s ON SEQUENCES TO %s",
					quotedOwner, seqPrivClause, quotedUser)
				if _, err := db.Exec(ownerSeqSQL); err != nil {
					return fmt.Errorf("setting owner-scoped default sequence privileges for %q: %w - %+v", username, err, seqPrivClause)
				}
			}
		}
	}

	return nil
}

// EnsureOwner makes username the OWNER of dbName and grants it full access on the
// public schema. It connects to the maintenance database for the ALTER DATABASE
// statement (which cannot run inside the target database), then connects to the
// target database to set schema-level grants.
func (p postgresManager) EnsureOwner(host, adminUser, adminPass, dbName, username string) error {
	// ALTER DATABASE … OWNER TO must run outside the target database.
	maintenanceDB, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to maintenance database: %w", err)
	}
	defer maintenanceDB.Close()

	alterOwnerSQL := fmt.Sprintf("ALTER DATABASE %s OWNER TO %s",
		pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(username))
	if _, err := maintenanceDB.Exec(alterOwnerSQL); err != nil {
		return fmt.Errorf("setting owner of database %q to %q: %w", dbName, username, err)
	}

	// Schema-level grants must run inside the target database.
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to database %q: %w", dbName, err)
	}
	defer db.Close()

	quotedUser := pq.QuoteIdentifier(username)
	schemaGrants := []string{
		fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", quotedUser),
		fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA public TO %s", quotedUser),
		fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO %s", quotedUser),
	}
	for _, stmt := range schemaGrants {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("granting schema access to %q: %w", username, err)
		}
	}
	return nil
}

// FindOwner returns the current PostgreSQL owner of dbName, or an empty string
// if the database does not exist.
func (p postgresManager) FindOwner(host, adminUser, adminPass, dbName string) (string, error) {
	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return "", fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	var owner string
	err = db.QueryRow(
		"SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1", dbName,
	).Scan(&owner)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying owner of database %q: %w", dbName, err)
	}
	return owner, nil
}

// DropUser removes the specified role from the Postgres cluster.
//
// Dropping a role requires that it owns no objects and holds no privileges in
// the cluster. This function handles that systematically:
//  1. REASSIGN OWNED from the maintenance database transfers any cluster-level
//     objects (e.g. database ownership) to the admin role — but only if the
//     role actually owns them, leaving other users' ownership intact.
//  2. REASSIGN OWNED from the target database transfers any in-database objects
//     (tables, sequences, etc.) to the admin role.
//  3. DROP OWNED revokes all remaining per-database privileges and removes
//     pg_default_acl entries that reference the role as grantor or grantee.
//  4. DROP ROLE IF EXISTS removes the cluster-level role.
func (p postgresManager) DropUser(host, adminUser, adminPass, dbName, username string) error {
	quotedUser := pq.QuoteIdentifier(username)
	quotedAdmin := pq.QuoteIdentifier(adminUser)

	// Steps 1 & 4 use the maintenance database so that cluster-level operations
	// (REASSIGN OWNED for databases/tablespaces and DROP ROLE) can execute
	// outside the target database.
	mainDB, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer mainDB.Close()

	// Reassign any cluster-level objects (e.g. the database itself) owned by
	// this role. If the role does not own the database, this is a no-op for
	// database ownership — other users' ownership is not affected.
	if _, err := mainDB.Exec(fmt.Sprintf("REASSIGN OWNED BY %s TO %s", quotedUser, quotedAdmin)); err != nil {
		_ = err // Non-fatal: role may not own any cluster-level objects or may not exist.
	}

	// Steps 2 & 3 run inside the target database.
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	// Transfer any per-database objects (tables, sequences, etc.) owned by the role.
	if _, err := db.Exec(fmt.Sprintf("REASSIGN OWNED BY %s TO %s", quotedUser, quotedAdmin)); err != nil {
		_ = err // Non-fatal: role may not own any objects or may not exist.
	}

	// Remove all remaining per-database privilege grants and pg_default_acl
	// entries (including owner-scoped ones) referencing this role.
	if _, err := db.Exec(fmt.Sprintf("DROP OWNED BY %s", quotedUser)); err != nil {
		_ = err // Non-fatal: role may not exist or have no remaining objects.
	}

	// Drop the cluster-wide role. This now succeeds because all dependencies
	// in this database have been cleared above.
	if _, err := mainDB.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", quotedUser)); err != nil {
		return fmt.Errorf("dropping role %q: %w", username, err)
	}

	return nil
}

// openPostgres opens a verified connection to a Postgres instance.
func openPostgres(host, user, password, dbName string) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
		host, postgresPort, user, password, dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
