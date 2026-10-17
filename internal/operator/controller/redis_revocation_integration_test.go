//go:build integration

package controller

import (
	. "github.com/benjamin-wright/db-operator/internal/test_utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	redis "github.com/redis/go-redis/v9"
	"strings"
)

var _ = Describe("Redis credential revocation fence", func() {
	It("keeps a user disabled when a concurrent key writer aborts conditional enablement", func() {
		ns, _, dbKey, adminKey := NewRedisDatabase("redis-watch-revocation")
		DeferCleanup(func() { _ = K8sClient.Delete(Ctx, ns) })
		WaitForRedisDatabase(dbKey)
		admin, closeAdmin := ConnectToRedisDatabase(dbKey, adminKey)
		defer closeAdmin()
		writer, closeWriter := ConnectToRedisDatabase(dbKey, adminKey)
		defer closeWriter()
		Expect(admin.Do(Ctx, "ACL", "SETUSER", "contended", "reset", "on", ">password", "~*", "+get").Err()).To(Succeed())
		err := admin.Watch(Ctx, func(tx *redis.Tx) error {
			if err := revokeRedisCredential(Ctx, tx, "contended"); err != nil {
				return err
			}
			Expect(writer.Set(Ctx, "watch-record", "changed", 0).Err()).To(Succeed())
			_, err := tx.TxPipelined(Ctx, func(pipe redis.Pipeliner) error {
				pipe.Do(Ctx, "ACL", "SETUSER", "contended", "on")
				return nil
			})
			return err
		}, "watch-record")
		Expect(err).To(Equal(redis.TxFailedErr))
		users, err := admin.Do(Ctx, "ACL", "LIST").StringSlice()
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, user := range users {
			if strings.HasPrefix(user, "user contended ") {
				found = true
				Expect(user).To(ContainSubstring(" off "))
				Expect(user).NotTo(ContainSubstring(" on "))
			}
		}
		Expect(found).To(BeTrue())
	})
})
