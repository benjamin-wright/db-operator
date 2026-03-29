package natsconfig

import (
	"crypto/sha256"
	"fmt"
	"strings"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

const (
	// ClientPort is the default NATS client port.
	ClientPort = 4222

	// MonitorPort is the NATS HTTP monitoring port.
	MonitorPort = 8222

	// DataMountPath is the directory used for JetStream persistent storage.
	DataMountPath = "/data"
)

// AccountCredentials pairs a NatsAccount CR with the resolved passwords for each of its users.
type AccountCredentials struct {
	Account   v1alpha1.NatsAccount
	Passwords map[string]string // username → password
}

// Build generates the full NATS server configuration from the given accounts.
// jetStream indicates whether the JetStream persistence block should be included.
func Build(jetStream bool, accounts []AccountCredentials) string {
	var b strings.Builder
	fmt.Fprintf(&b, "port: %d\n", ClientPort)
	fmt.Fprintf(&b, "http_port: %d\n", MonitorPort)

	if len(accounts) > 0 {
		b.WriteString("\naccounts {\n")
		for i := range accounts {
			writeAccountConfig(&b, &accounts[i])
		}
		b.WriteString("}\n")
	}

	if jetStream {
		fmt.Fprintf(&b, "\njetstream {\n  store_dir: %q\n}\n", DataMountPath)
	}

	return b.String()
}

// Checksum returns a short SHA-256 hex digest of the config string, used as
// a pod template annotation so that config changes trigger a rolling restart.
func Checksum(config string) string {
	sum := sha256.Sum256([]byte(config))
	return fmt.Sprintf("%x", sum[:8])
}

func writeAccountConfig(b *strings.Builder, ac *AccountCredentials) {
	fmt.Fprintf(b, "  %q {\n", ac.Account.Name)

	if len(ac.Account.Spec.Users) > 0 {
		b.WriteString("    users = [\n")
		for _, user := range ac.Account.Spec.Users {
			pw, ok := ac.Passwords[user.Username]
			if !ok {
				continue // Secret not yet provisioned; skip until next reconcile
			}
			if user.Permissions == nil {
				fmt.Fprintf(b, "      {user: %q, password: %q}\n", user.Username, pw)
			} else {
				fmt.Fprintf(b, "      {\n        user: %q\n        password: %q\n", user.Username, pw)
				b.WriteString("        permissions: {\n")
				if user.Permissions.Publish != nil {
					b.WriteString("          publish: {\n")
					writeSubjectPerm(b, user.Permissions.Publish)
					b.WriteString("          }\n")
				}
				if user.Permissions.Subscribe != nil {
					b.WriteString("          subscribe: {\n")
					writeSubjectPerm(b, user.Permissions.Subscribe)
					b.WriteString("          }\n")
				}
				b.WriteString("        }\n      }\n")
			}
		}
		b.WriteString("    ]\n")
	}

	if len(ac.Account.Spec.Exports) > 0 {
		b.WriteString("    exports = [\n")
		for _, exp := range ac.Account.Spec.Exports {
			if exp.Type == v1alpha1.NatsExportTypeStream {
				fmt.Fprintf(b, "      {stream: %q", exp.Subject)
			} else {
				fmt.Fprintf(b, "      {service: %q", exp.Subject)
			}
			if exp.TokenRequired {
				b.WriteString(", token_req: true")
			}
			b.WriteString("}\n")
		}
		b.WriteString("    ]\n")
	}

	if len(ac.Account.Spec.Imports) > 0 {
		b.WriteString("    imports = [\n")
		for _, imp := range ac.Account.Spec.Imports {
			if imp.Type == v1alpha1.NatsExportTypeStream {
				fmt.Fprintf(b, "      {stream: {account: %q, subject: %q}", imp.Account, imp.Subject)
			} else {
				fmt.Fprintf(b, "      {service: {account: %q, subject: %q}", imp.Account, imp.Subject)
			}
			if imp.LocalSubject != "" {
				fmt.Fprintf(b, ", to: %q", imp.LocalSubject)
			}
			b.WriteString("}\n")
		}
		b.WriteString("    ]\n")
	}

	b.WriteString("  }\n")
}

func writeSubjectPerm(b *strings.Builder, perm *v1alpha1.NatsSubjectPermission) {
	if len(perm.Allow) > 0 {
		b.WriteString("            allow: [")
		for i, s := range perm.Allow {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", s)
		}
		b.WriteString("]\n")
	}
	if len(perm.Deny) > 0 {
		b.WriteString("            deny: [")
		for i, s := range perm.Deny {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", s)
		}
		b.WriteString("]\n")
	}
}
