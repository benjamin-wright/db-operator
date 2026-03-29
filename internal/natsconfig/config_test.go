package natsconfig_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/benjamin-wright/db-operator/internal/natsconfig"
	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
)

// account is a convenience constructor for a minimal NatsAccount.
func account(name string, spec v1alpha1.NatsAccountSpec) v1alpha1.NatsAccount {
	return v1alpha1.NatsAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
}

func TestBuild_NoPorts(t *testing.T) {
	got := natsconfig.Build(false, nil)
	want := "port: 4222\nhttp_port: 8222\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_JetStream(t *testing.T) {
	got := natsconfig.Build(true, nil)
	want := "port: 4222\nhttp_port: 8222\n\njetstream {\n  store_dir: \"/data\"\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_SimpleUser(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account: account("myaccount", v1alpha1.NatsAccountSpec{
				ClusterRef: "mycluster",
				Users: []v1alpha1.NatsUser{
					{Username: "alice", SecretName: "alice-secret"},
				},
			}),
			Passwords: map[string]string{"alice": "s3cr3t"},
		},
	}
	got := natsconfig.Build(false, creds)
	want := `port: 4222
http_port: 8222

accounts {
  "myaccount" {
    users = [
      {user: "alice", password: "s3cr3t"}
    ]
  }
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_UserWithPermissions(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account: account("acct", v1alpha1.NatsAccountSpec{
				ClusterRef: "c",
				Users: []v1alpha1.NatsUser{
					{
						Username:   "bob",
						SecretName: "bob-secret",
						Permissions: &v1alpha1.NatsUserPermissions{
							Publish: &v1alpha1.NatsSubjectPermission{
								Allow: []string{"events.>"},
								Deny:  []string{"events.private"},
							},
							Subscribe: &v1alpha1.NatsSubjectPermission{
								Allow: []string{"_INBOX.>"},
							},
						},
					},
				},
			}),
			Passwords: map[string]string{"bob": "p@ss"},
		},
	}
	got := natsconfig.Build(false, creds)
	want := `port: 4222
http_port: 8222

accounts {
  "acct" {
    users = [
      {
        user: "bob"
        password: "p@ss"
        permissions: {
          publish: {
            allow: ["events.>"]
            deny: ["events.private"]
          }
          subscribe: {
            allow: ["_INBOX.>"]
          }
        }
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_MissingPasswordSkipsUser(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account: account("acct", v1alpha1.NatsAccountSpec{
				ClusterRef: "c",
				Users: []v1alpha1.NatsUser{
					{Username: "ready", SecretName: "ready-secret"},
					{Username: "notyet", SecretName: "notyet-secret"},
				},
			}),
			Passwords: map[string]string{"ready": "pw"},
		},
	}
	got := natsconfig.Build(false, creds)
	want := `port: 4222
http_port: 8222

accounts {
  "acct" {
    users = [
      {user: "ready", password: "pw"}
    ]
  }
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_Exports(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account: account("publisher", v1alpha1.NatsAccountSpec{
				ClusterRef: "c",
				Exports: []v1alpha1.NatsExport{
					{Subject: "metrics.>", Type: v1alpha1.NatsExportTypeStream},
					{Subject: "api.>", Type: v1alpha1.NatsExportTypeService, TokenRequired: true},
				},
			}),
			Passwords: map[string]string{},
		},
	}
	got := natsconfig.Build(false, creds)
	want := `port: 4222
http_port: 8222

accounts {
  "publisher" {
    exports = [
      {stream: "metrics.>"}
      {service: "api.>", token_req: true}
    ]
  }
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_Imports(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account: account("consumer", v1alpha1.NatsAccountSpec{
				ClusterRef: "c",
				Imports: []v1alpha1.NatsImport{
					{Account: "publisher", Subject: "metrics.>", Type: v1alpha1.NatsExportTypeStream},
					{Account: "publisher", Subject: "api.>", Type: v1alpha1.NatsExportTypeService, LocalSubject: "ext.api.>"},
				},
			}),
			Passwords: map[string]string{},
		},
	}
	got := natsconfig.Build(false, creds)
	want := `port: 4222
http_port: 8222

accounts {
  "consumer" {
    imports = [
      {stream: {account: "publisher", subject: "metrics.>"}}
      {service: {account: "publisher", subject: "api.>"}, to: "ext.api.>"}
    ]
  }
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuild_MultipleAccounts(t *testing.T) {
	creds := []natsconfig.AccountCredentials{
		{
			Account:   account("a", v1alpha1.NatsAccountSpec{ClusterRef: "c"}),
			Passwords: map[string]string{},
		},
		{
			Account:   account("b", v1alpha1.NatsAccountSpec{ClusterRef: "c"}),
			Passwords: map[string]string{},
		},
	}
	got := natsconfig.Build(false, creds)
	want := "port: 4222\nhttp_port: 8222\n\naccounts {\n  \"a\" {\n  }\n  \"b\" {\n  }\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestChecksum_Deterministic(t *testing.T) {
	config := "port: 4222\nhttp_port: 8222\n"
	if natsconfig.Checksum(config) != natsconfig.Checksum(config) {
		t.Error("Checksum is not deterministic")
	}
}

func TestChecksum_ChangesWithContent(t *testing.T) {
	a := natsconfig.Checksum("port: 4222\n")
	b := natsconfig.Checksum("port: 4223\n")
	if a == b {
		t.Error("expected different checksums for different content")
	}
}

func TestChecksum_Length(t *testing.T) {
	sum := natsconfig.Checksum("any config")
	if len(sum) != 16 {
		t.Errorf("expected 16 hex chars (8 bytes), got %d: %q", len(sum), sum)
	}
}
