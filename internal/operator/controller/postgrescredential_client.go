package controller

import (
	"context"
	"database/sql"
	"errors"
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
	EnsureUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission, tables []string) error
	DropUser(host, adminUser, adminPass, dbName, username string) error
	// EnsureOwner makes username the owner of dbName and grants it full schema access.
	EnsureOwner(host, adminUser, adminPass, dbName, username string) error
	// FindOwner returns the current PostgreSQL owner role of dbName, or an empty
	// string if the database does not exist.
	FindOwner(host, adminUser, adminPass, dbName string) (string, error)
	// EnsureUserExists creates the role with a login password if it does not
	// already exist. It is used when a credential carries no per-database
	// permissions but still needs the role provisioned (e.g. for clusterRoles
	// membership grants).
	EnsureUserExists(host, adminUser, adminPass, username, password string) error
	// EnsureRoleMemberships grants username membership in each of roles. The
	// roles must come from the validClusterRoles allow-list; any other value
	// returns an error without touching the database.
	EnsureRoleMemberships(host, adminUser, adminPass, username string, roles []v1alpha1.PredefinedRole) error
	// EnsureSchemaAccess grants username full access on the public schema of
	// dbName without changing database ownership. Safe to call concurrently
	// with other controllers.
	EnsureSchemaAccess(host, adminUser, adminPass, dbName, username string) error
	// SetUserPassword unconditionally updates the password for an existing role.
	// Use this when the Kubernetes Secret is the authoritative source and the
	// database-side password may have drifted (e.g. after PVC reuse or forced
	// Secret regeneration).
	SetUserPassword(host, adminUser, adminPass, username, password string) error
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

// validClusterRoles is the allow-list of PostgreSQL predefined roles that may
// be granted via spec.clusterRoles. Any role outside this set is rejected
// before reaching SQL — this prevents accidental escalation to roles like
// pg_write_all_data, pg_read_server_files, or pg_execute_server_program, which
// can yield superuser-equivalent capabilities.
var validClusterRoles = map[v1alpha1.PredefinedRole]struct{}{
	v1alpha1.RolePgReadAllData:     {},
	v1alpha1.RolePgReadAllStats:    {},
	v1alpha1.RolePgReadAllSettings: {},
	v1alpha1.RolePgMonitor:         {},
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
// When tables is non-empty, privileges are granted only on those specific tables;
// no ALTER DEFAULT PRIVILEGES is emitted in that case because PostgreSQL has no
// mechanism to pre-grant future tables by name.
func (p postgresManager) EnsureUser(host, adminUser, adminPass, dbName, username, password string, permissions []v1alpha1.DatabasePermission, tables []string) error {
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

		// Sequences only accept SELECT, UPDATE, and ALL — filter out table-only
		// privileges (INSERT, DELETE, TRUNCATE, REFERENCES, TRIGGER) before
		// building any sequence-targeted GRANT.
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

		if len(tables) > 0 {
			// Table-scoped grant: privileges apply only to the named tables.
			// No ALTER DEFAULT PRIVILEGES is emitted — PostgreSQL cannot pre-grant
			// on specific future tables by name.
			quotedTables := make([]string, len(tables))
			for i, t := range tables {
				if t == "" {
					return fmt.Errorf("tables entry %d is an empty string", i)
				}
				quotedTables[i] = pq.QuoteIdentifier(t)
			}
			grantSQL := fmt.Sprintf("GRANT %s ON TABLE %s TO %s",
				privClause, strings.Join(quotedTables, ", "), quotedUser)
			if _, err := db.Exec(grantSQL); err != nil {
				return fmt.Errorf("granting table-scoped permissions to %q: %w", username, err)
			}

			// Fix C: grant sequence-compatible privileges on sequences owned by
			// the named tables. PostgreSQL has no equivalent of ALTER DEFAULT
			// PRIVILEGES scoped to specific tables, so this query is re-run on
			// every reconcile to catch sequences added by later migrations.
			if len(seqPrivs) > 0 {
				rows, err := db.Query(`
SELECT d.objid::regclass::text AS seq_name
FROM   pg_depend d
JOIN   pg_class  c ON c.oid = d.refobjid
WHERE  d.classid       = 'pg_class'::regclass
  AND  d.deptype       = 'a'
  AND  d.refclassid    = 'pg_class'::regclass
  AND  c.relname       = ANY($1)
  AND  c.relnamespace  = 'public'::regnamespace
`, pq.Array(tables))
				if err != nil {
					return fmt.Errorf("looking up sequences for named tables: %w", err)
				}
				var seqNames []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						rows.Close()
						return fmt.Errorf("scanning sequence name: %w", err)
					}
					seqNames = append(seqNames, name)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return fmt.Errorf("iterating sequence rows: %w", err)
				}
				rows.Close()

				seqPrivClause := strings.Join(seqPrivs, ", ")
				for _, seqName := range seqNames {
					// seqName comes from regclass::text which is already
					// schema-qualified and quoted as needed by PostgreSQL.
					seqGrantSQL := fmt.Sprintf("GRANT %s ON SEQUENCE %s TO %s",
						seqPrivClause, seqName, quotedUser)
					if _, err := db.Exec(seqGrantSQL); err != nil {
						return fmt.Errorf("granting sequence permissions on %q to %q: %w", seqName, username, err)
					}
				}
			}
		} else {
			// All-tables grant: privileges apply to every current and future table.
			grantSQL := fmt.Sprintf("GRANT %s ON ALL TABLES IN SCHEMA public TO %s", privClause, quotedUser)
			if _, err := db.Exec(grantSQL); err != nil {
				return fmt.Errorf("granting permissions to %q: %w", username, err)
			}

			// Fix A: also grant on existing sequences. The ALTER DEFAULT
			// PRIVILEGES below covers sequences created after this point;
			// this covers sequences that already exist at reconcile time.
			seqGrantSQL := fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO %s", quotedUser)
			if _, err := db.Exec(seqGrantSQL); err != nil {
				return fmt.Errorf("granting sequence permissions to %q: %w", username, err)
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
	}

	return nil
}

// EnsureUserExists creates the role with LOGIN PASSWORD if it does not already
// exist. It connects to the maintenance database because role creation is
// cluster-wide and does not require any particular target database.
func (p postgresManager) EnsureUserExists(host, adminUser, adminPass, username, password string) error {
	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", username).Scan(&exists); err != nil {
		return fmt.Errorf("checking if role exists: %w", err)
	}
	if exists {
		return nil
	}

	createSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s",
		pq.QuoteIdentifier(username), pq.QuoteLiteral(password))
	if _, err := db.Exec(createSQL); err != nil {
		return fmt.Errorf("creating role %q: %w", username, err)
	}
	return nil
}

// SetUserPassword unconditionally updates the password for username using
// ALTER ROLE. It connects as adminUser to the maintenance database.
func (p postgresManager) SetUserPassword(host, adminUser, adminPass, username, password string) error {
	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	sql := fmt.Sprintf("ALTER ROLE %s PASSWORD %s",
		pq.QuoteIdentifier(username), pq.QuoteLiteral(password))
	if _, err := db.Exec(sql); err != nil {
		return fmt.Errorf("setting password for role %q: %w", username, err)
	}
	return nil
}

// EnsureRoleMemberships grants username membership in each of roles. The role
// names are validated against validClusterRoles before any SQL is executed so
// that only known-safe predefined roles can ever reach the database.
func (p postgresManager) EnsureRoleMemberships(host, adminUser, adminPass, username string, roles []v1alpha1.PredefinedRole) error {
	if len(roles) == 0 {
		return nil
	}

	for _, r := range roles {
		if _, ok := validClusterRoles[r]; !ok {
			return fmt.Errorf("unknown predefined role %q", r)
		}
	}

	db, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer db.Close()

	quotedUser := pq.QuoteIdentifier(username)
	for _, r := range roles {
		// Predefined role names are not user-supplied identifiers but quoting them
		// is still correct and harmless.
		grantSQL := fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier(string(r)), quotedUser)
		if _, err := db.Exec(grantSQL); err != nil {
			return fmt.Errorf("granting role %q to %q: %w", r, username, err)
		}
	}
	return nil
}

// EnsureSchemaAccess grants username full access on the public schema of dbName
// without changing database ownership.
func (p postgresManager) EnsureSchemaAccess(host, adminUser, adminPass, dbName, username string) error {
	db, err := openPostgres(host, adminUser, adminPass, dbName)
	if err != nil {
		return fmt.Errorf("connecting to database %q: %w", dbName, err)
	}
	defer db.Close()

	quotedUser := pq.QuoteIdentifier(username)
	stmts := []string{
		fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", quotedUser),
		fmt.Sprintf("GRANT ALL ON ALL TABLES IN SCHEMA public TO %s", quotedUser),
		fmt.Sprintf("GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO %s", quotedUser),
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("granting schema access to %q: %w", username, err)
		}
	}
	return nil
}

// EnsureOwner makes username the OWNER of dbName and grants it full access on the
// public schema. It connects to the maintenance database for the ALTER DATABASE
// statement (which cannot run inside the target database), then connects to the
// target database to set schema-level grants.
//
// When ownership transitions from a different role, EnsureOwner also replays
// every public-schema default-privilege entry that was attached to the previous
// owner against the new owner. Without this, sibling credentials provisioned
// before the owner transition (whose ALTER DEFAULT PRIVILEGES targeted the
// then-current owner) would receive no grants on tables created by the new
// owner. The previous owner's entries are left in place because they are inert
// once the role no longer creates objects.
func (p postgresManager) EnsureOwner(host, adminUser, adminPass, dbName, username string) error {
	maintenanceDB, err := openPostgres(host, adminUser, adminPass, "postgres")
	if err != nil {
		return fmt.Errorf("connecting to maintenance database: %w", err)
	}
	defer maintenanceDB.Close()

	var previousOwner string
	if err := maintenanceDB.QueryRow(
		"SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1", dbName,
	).Scan(&previousOwner); err != nil {
		return fmt.Errorf("looking up current owner of database %q: %w", dbName, err)
	}

	if previousOwner != username {
		alterOwnerSQL := fmt.Sprintf("ALTER DATABASE %s OWNER TO %s",
			pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(username))
		if _, err := maintenanceDB.Exec(alterOwnerSQL); err != nil {
			return fmt.Errorf("setting owner of database %q to %q: %w", dbName, username, err)
		}
	}

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

	if previousOwner != "" && previousOwner != username {
		if err := replayDefaultPrivileges(db, previousOwner, username); err != nil {
			return fmt.Errorf("replaying default privileges from %q to %q: %w", previousOwner, username, err)
		}
	}

	return nil
}

// replayDefaultPrivileges copies every TABLES/SEQUENCES default-privilege entry
// attached to previousOwner in the public schema and re-emits it under
// newOwner. Other object types (FUNCTIONS, TYPES, SCHEMAS) are skipped because
// EnsureUser never produces them.
func replayDefaultPrivileges(db *sql.DB, previousOwner, newOwner string) error {
	rows, err := db.Query(`
		SELECT grantee::regrole::text, privilege_type, dacl.defaclobjtype
		FROM pg_catalog.pg_default_acl dacl,
		     LATERAL pg_catalog.aclexplode(dacl.defaclacl)
		WHERE dacl.defaclrole = $1::regrole
		  AND dacl.defaclnamespace = 'public'::regnamespace
		  AND dacl.defaclobjtype IN ('r', 'S')
	`, previousOwner)
	if err != nil {
		return fmt.Errorf("querying pg_default_acl for %q: %w", previousOwner, err)
	}
	defer rows.Close()

	objectTypeKeyword := map[string]string{"r": "TABLES", "S": "SEQUENCES"}
	quotedNewOwner := pq.QuoteIdentifier(newOwner)

	for rows.Next() {
		var grantee, privilege, objType string
		if err := rows.Scan(&grantee, &privilege, &objType); err != nil {
			return fmt.Errorf("scanning pg_default_acl row: %w", err)
		}
		if grantee == "" || grantee == newOwner {
			continue
		}
		keyword, ok := objectTypeKeyword[objType]
		if !ok {
			continue
		}

		stmt := fmt.Sprintf(
			"ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT %s ON %s TO %s",
			quotedNewOwner, privilege, keyword, pq.QuoteIdentifier(grantee))
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("re-granting default %s on %s to %q: %w", privilege, keyword, grantee, err)
		}
	}
	return rows.Err()
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

// isTableNotFoundError reports whether err originated from PostgreSQL error code
// 42P01 (undefined_table), which is returned when a GRANT targets a table that
// does not exist in the database.
func isTableNotFoundError(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "42P01"
	}
	return false
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
