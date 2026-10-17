//go:build integration

package controller

import (
	"database/sql"

	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Migration provisioning sessions", func() {
	It("never reconnects provisioning or cleanup after both fence sessions are lost", func() {
		ns, _, lookup, secret := NewDatabase("migration-sessions")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(lookup)
		admin, closeAdmin := ConnectToDatabase(lookup, secret)
		DeferCleanup(closeAdmin)
		guardDB, closeGuard := ConnectToDatabase(lookup, secret)
		DeferCleanup(closeGuard)
		guard, err := guardDB.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = guard.Close() })
		_, err = guard.ExecContext(Ctx, "SELECT pg_advisory_lock(hashtextextended('migration-session-test',0))")
		Expect(err).NotTo(HaveOccurred())
		manager := postgresManager{boundSessions: map[string]*sql.Conn{"postgres": guard}}
		Expect(manager.EnsureDatabase("", "", "", "migration_target")).To(Succeed())
		targetDB, closeTarget := ConnectToDatabaseNamed(lookup, secret, "migration_target")
		DeferCleanup(closeTarget)
		target, err := targetDB.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = target.Close() })
		_, err = target.ExecContext(Ctx, "SELECT pg_advisory_lock($1)", migrationLockKey)
		Expect(err).NotTo(HaveOccurred())
		manager.boundSessions["migration_target"] = target
		Expect(manager.EnsureUserExists("", "", "", "migration_session_user", "initial")).To(Succeed())
		Expect(manager.EnsureOwner("", "", "", "migration_target", "migration_session_user")).To(Succeed())
		_, err = manager.open("", "", "", "unbound")
		Expect(err).To(MatchError(ContainSubstring("no held migration fence")))
		for _, conn := range []*sql.Conn{guard, target} {
			var pid int
			Expect(conn.QueryRowContext(Ctx, "SELECT pg_backend_pid()").Scan(&pid)).To(Succeed())
			var killed bool
			Expect(admin.QueryRow("SELECT pg_terminate_backend($1,5000)", pid).Scan(&killed)).To(Succeed())
			Expect(killed).To(BeTrue())
		}
		Expect(manager.EnsureUserExists("", "", "", "migration_session_user", "stale")).NotTo(Succeed())
		Expect(manager.EnsureSchemaAccess("", "", "", "migration_target", "migration_session_user")).NotTo(Succeed())
		Expect(manager.EnsureOwner("", "", "", "migration_target", "migration_session_user")).NotTo(Succeed())
		Expect(manager.dropMigrationUser("migration_target", "migration_session_user", "postgres")).NotTo(Succeed())
		var exists bool
		Expect(admin.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='migration_session_user')").Scan(&exists)).To(Succeed())
		Expect(exists).To(BeTrue())
	})

	It("retains cleanup when a migration role has unexpected privileges in another database", func() {
		ns, _, lookup, secret := NewDatabase("migration-cleanup")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForDatabase(lookup)
		admin, closeAdmin := ConnectToDatabase(lookup, secret)
		DeferCleanup(closeAdmin)
		var adminUser string
		Expect(admin.QueryRow("SELECT current_user").Scan(&adminUser)).To(Succeed())
		guard, err := admin.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = guard.Close() })
		manager := postgresManager{boundSessions: map[string]*sql.Conn{"postgres": guard}}
		Expect(manager.EnsureDatabase("", "", "", "cleanup_target")).To(Succeed())
		Expect(manager.EnsureDatabase("", "", "", "unrelated")).To(Succeed())
		targetDB, closeTarget := ConnectToDatabaseNamed(lookup, secret, "cleanup_target")
		DeferCleanup(closeTarget)
		target, err := targetDB.Conn(Ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = target.Close() })
		manager.boundSessions["cleanup_target"] = target
		Expect(manager.EnsureUserExists("", "", "", "migration_cleanup_user", "initial")).To(Succeed())
		Expect(manager.EnsureOwner("", "", "", "cleanup_target", "migration_cleanup_user")).To(Succeed())
		_, err = guard.ExecContext(Ctx, "GRANT CONNECT ON DATABASE unrelated TO migration_cleanup_user")
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.dropMigrationUser("cleanup_target", "migration_cleanup_user", adminUser)).To(MatchError(ContainSubstring("outside its bound database")))
		var retained bool
		Expect(guard.QueryRowContext(Ctx, "SELECT EXISTS(SELECT 1 FROM pg_shdepend WHERE refobjid=(SELECT oid FROM pg_roles WHERE rolname='migration_cleanup_user') AND classid='pg_database'::regclass AND objid=(SELECT oid FROM pg_database WHERE datname='unrelated'))").Scan(&retained)).To(Succeed())
		Expect(retained).To(BeTrue())
		_, err = guard.ExecContext(Ctx, "REVOKE CONNECT ON DATABASE unrelated FROM migration_cleanup_user")
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.dropMigrationUser("cleanup_target", "migration_cleanup_user", adminUser)).To(Succeed())
	})
})
