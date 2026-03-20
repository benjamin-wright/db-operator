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

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
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

// ────────────────────────────────────────────────────────────────────────────
// PostgresManager — external Postgres dependency interface
// ────────────────────────────────────────────────────────────────────────────

// PostgresManager abstracts direct Postgres interactions so the reconciler can
// be tested without a live database.
type PostgresManager interface {
	EnsureDatabase(host, adminUser, adminPass, dbName string) error
	EnsureUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission) error
	DropUser(host, adminUser, adminPass, dbName, username string) error
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
	}

	return nil
}

// DropUser connects to the target Postgres instance and drops the specified role.
func (p postgresManager) DropUser(host, adminUser, adminPass, dbName, username string) error {
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	quotedUser := pq.QuoteIdentifier(username)

	revokeSQL := fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %s", quotedUser)
	if _, err := db.Exec(revokeSQL); err != nil {
		// Ignore errors — the role may never have been granted anything.
		_ = err
	}

	revokeDefaultSQL := fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM %s", quotedUser)
	if _, err := db.Exec(revokeDefaultSQL); err != nil {
		// Ignore errors — default privileges may not have been set.
		_ = err
	}

	dropSQL := fmt.Sprintf("DROP ROLE IF EXISTS %s", quotedUser)
	if _, err := db.Exec(dropSQL); err != nil {
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
