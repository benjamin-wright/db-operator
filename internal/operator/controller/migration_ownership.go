package controller

import (
	"fmt"
	"github.com/lib/pq"
)

// Transfer only application objects in this database. REASSIGN OWNED would also
// transfer shared objects (other databases and tablespaces), so it is unsuitable
// when converting the old shared migration identity to a per-database role.
func transferApplicationObjects(db postgresExecutor, username string) error {
	const objects = `
 SELECT 'SCHEMA', quote_ident(nspname) FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname NOT IN ('information_schema','__dbop_control')
 UNION ALL
 SELECT CASE c.relkind WHEN 'S' THEN 'SEQUENCE' WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END, quote_ident(n.nspname)||'.'||quote_ident(c.relname)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN ('r','p','v','m','f','S') AND n.nspname !~ '^pg_' AND n.nspname NOT IN ('information_schema','__dbop_control')
 AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e')
 UNION ALL
 SELECT CASE p.prokind WHEN 'p' THEN 'PROCEDURE' WHEN 'a' THEN 'AGGREGATE' ELSE 'FUNCTION' END, quote_ident(n.nspname)||'.'||quote_ident(p.proname)||'('||pg_get_function_identity_arguments(p.oid)||')'
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname NOT IN ('information_schema','__dbop_control')
 AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')
 UNION ALL
 SELECT CASE t.typtype WHEN 'd' THEN 'DOMAIN' ELSE 'TYPE' END,quote_ident(n.nspname)||'.'||quote_ident(t.typname)
 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
 WHERE t.typtype IN ('d','e','r') AND n.nspname !~ '^pg_' AND n.nspname NOT IN ('information_schema','__dbop_control')
 AND NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass AND d.objid=t.oid AND d.deptype='e')
 `
	rows, err := db.Query(objects)
	if err != nil {
		return err
	}
	var statements []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			rows.Close()
			return err
		}
		statements = append(statements, fmt.Sprintf("ALTER %s %s OWNER TO %s", kind, name, pq.QuoteIdentifier(username)))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// Declared capabilities cannot be bypassed through PUBLIC grants. Remove
// relation, column and routine access, including global and schema defaults.
func restrictPublicPrivileges(db postgresExecutor, adminUser string) error {
	rows, err := db.Query(`SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname NOT IN ('information_schema','__dbop_control')`)
	if err != nil {
		return err
	}
	var schemas []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		schemas = append(schemas, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, schema := range schemas {
		if _, err := db.Exec("REVOKE CREATE ON SCHEMA " + pq.QuoteIdentifier(schema) + " FROM PUBLIC"); err != nil {
			return err
		}
		for _, kind := range []string{"TABLES", "SEQUENCES", "ROUTINES"} {
			if _, err := db.Exec("REVOKE ALL ON ALL " + kind + " IN SCHEMA " + pq.QuoteIdentifier(schema) + " FROM PUBLIC"); err != nil {
				return err
			}
		}
	}
	// Table-level REVOKE does not remove independently granted column privileges.
	rows, err = db.Query(`SELECT quote_ident(n.nspname)||'.'||quote_ident(c.relname), quote_ident(a.attname)
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE a.attacl IS NOT NULL AND n.nspname !~ '^pg_' AND n.nspname NOT IN ('information_schema','__dbop_control')
 AND EXISTS(SELECT 1 FROM aclexplode(a.attacl) acl WHERE acl.grantee=0)`)
	if err != nil {
		return err
	}
	var columns [][2]string
	for rows.Next() {
		var column [2]string
		if err := rows.Scan(&column[0], &column[1]); err != nil {
			rows.Close()
			return err
		}
		columns = append(columns, column)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range columns {
		if _, err := db.Exec("REVOKE ALL (" + column[1] + ") ON TABLE " + column[0] + " FROM PUBLIC"); err != nil {
			return err
		}
	}
	rows, err = db.Query(`SELECT DISTINCT pg_get_userbyid(owner) FROM (SELECT proowner AS owner FROM pg_proc UNION SELECT relowner FROM pg_class UNION SELECT defaclrole FROM pg_default_acl UNION SELECT datdba FROM pg_database WHERE datname=current_database() UNION SELECT oid FROM pg_roles WHERE rolname=$1) owners`, adminUser)
	if err != nil {
		return err
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, owner := range owners {
		for _, schema := range append([]string{""}, schemas...) {
			scope := ""
			if schema != "" {
				scope = " IN SCHEMA " + pq.QuoteIdentifier(schema)
			}
			for _, kind := range []string{"TABLES", "SEQUENCES", "FUNCTIONS"} {
				if _, err := db.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE " + pq.QuoteIdentifier(owner) + scope + " REVOKE ALL ON " + kind + " FROM PUBLIC"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// The old shared migration role must lose access to each converted database.
// Keep its other databases intact until their own migration sets are converted.
func revokeLegacyMigrationAccess(db postgresExecutor, database, currentOwner string) error {
	if currentOwner == migrationsRoleName {
		return nil
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", migrationsRoleName).Scan(&exists); err != nil || !exists {
		return err
	}
	role := pq.QuoteIdentifier(migrationsRoleName)
	if _, err := db.Exec("REVOKE ALL ON DATABASE " + pq.QuoteIdentifier(database) + " FROM " + role); err != nil {
		return err
	}
	rows, err := db.Query("SELECT nspname FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname NOT IN ('information_schema','__dbop_control')")
	if err != nil {
		return err
	}
	var schemas []string
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			rows.Close()
			return err
		}
		schemas = append(schemas, schema)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, schema := range schemas {
		if _, err := db.Exec("REVOKE ALL ON SCHEMA " + pq.QuoteIdentifier(schema) + " FROM " + role); err != nil {
			return err
		}
		for _, kind := range []string{"TABLES", "SEQUENCES", "ROUTINES"} {
			if _, err := db.Exec("REVOKE ALL ON ALL " + kind + " IN SCHEMA " + pq.QuoteIdentifier(schema) + " FROM " + role); err != nil {
				return err
			}
		}
	}
	return nil
}
