//go:build integration

package test_utils

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

var (
	K8sClient  client.Client
	Clientset  *kubernetes.Clientset
	RestConfig *rest.Config
	Ctx        context.Context
	Cancel     context.CancelFunc
	Scheme     = runtime.NewScheme()
)

const (
	Timeout  = 60 * time.Second
	Interval = 250 * time.Millisecond
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Ctx, Cancel = context.WithCancel(context.Background())

	// Register schemes.
	Expect(clientgoscheme.AddToScheme(Scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(Scheme)).To(Succeed())

	// Resolve kubeconfig: prefer KUBECONFIG env, fall back to ~/.scratch/db-operator.yaml.
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		Expect(err).NotTo(HaveOccurred())
		kubeconfigPath = filepath.Join(home, ".scratch", "db-operator.yaml")
	}
	_, err := os.Stat(kubeconfigPath)
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("kubeconfig not found at %s — is the k3d cluster running?", kubeconfigPath))

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	Expect(err).NotTo(HaveOccurred())
	RestConfig = cfg

	// Create a direct client for test assertions.
	K8sClient, err = client.New(cfg, client.Options{Scheme: Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(K8sClient).NotTo(BeNil())

	// Create a typed clientset for operations like pod exec.
	Clientset, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	Cancel()
})

// NewDatabase creates a namespace, a PostgresDatabase CR, and waits
// for it to reach Ready. Returns the namespace, database, and admin secret lookup key.
func NewDatabase(name string) (ns *corev1.Namespace, pgdb *v1alpha1.PostgresDatabase, dbLookup, adminSecretLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pg-",
		},
	}
	Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

	pgdb = &v1alpha1.PostgresDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"games-hub.io/operator-instance": "test",
			},
		},
		Spec: v1alpha1.PostgresDatabaseSpec{
			DatabaseName:    "testdb",
			PostgresVersion: "16",
			StorageSize:     resource.MustParse("256Mi"),
		},
	}
	Expect(K8sClient.Create(Ctx, pgdb)).To(Succeed())
	dbLookup = types.NamespacedName{Name: pgdb.Name, Namespace: ns.Name}
	adminSecretLookup = types.NamespacedName{Name: pgdb.Name + "-admin", Namespace: ns.Name}
	return
}

// WaitForDatabase polls until the PostgresDatabase reaches Ready phase.
func WaitForDatabase(lookup types.NamespacedName) {
	Eventually(func(g Gomega) {
		var fetched v1alpha1.PostgresDatabase
		g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.DatabasePhaseReady))
	}, Timeout, Interval).Should(Succeed())
}

func CreateNewUser(namespace, database, username, secretname string, permissions []v1alpha1.DatabasePermission) {
	pgcred := &v1alpha1.PostgresCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: namespace,
			Labels: map[string]string{
				"games-hub.io/operator-instance": "test",
			},
		},
		Spec: v1alpha1.PostgresCredentialSpec{
			DatabaseRef: database,
			Username:    username,
			Permissions: permissions,
			SecretName:  secretname,
		},
	}
	Expect(K8sClient.Create(Ctx, pgcred)).To(Succeed())
}

func ConnectToDatabase(dbLookup types.NamespacedName, secretLookup types.NamespacedName) (*sql.DB, func()) {
	var secret corev1.Secret
	Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed(), "fetching credential secret")

	username, ok := secret.Data["username"]
	Expect(ok).To(BeTrue(), "secret missing 'username' key")
	Expect(username).NotTo(BeEmpty(), "username in secret should not be empty")

	passwordBytes, ok := secret.Data["password"]
	Expect(ok).To(BeTrue(), "secret missing 'password' key")

	password := string(passwordBytes)

	// set up a port-forward to the database pod and connect through that, rather than relying on cluster DNS which may not be available in all test environments.

	connStr := fmt.Sprintf("host=localhost port=15432 user=postgres password=%s dbname=%s sslmode=disable",
		password, "testdb",
	)

	pfwd, err := portForward(dbLookup.Namespace, dbLookup.Name+"-0", 15432, 5432)
	Expect(err).NotTo(HaveOccurred(), "setting up port forward")

	go func() {
		Expect(pfwd.ForwardPorts()).To(Succeed(), "starting port forward")
	}()

	db, err := sql.Open("postgres", connStr)
	Expect(err).NotTo(HaveOccurred(), "opening database connection")

	return db, func() {
		db.Close()
		pfwd.Close()
	}
}

func portForward(namespace, podName string, localPort, remotePort int) (*portforward.PortForwarder, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, fmt.Errorf("building rest config: %w", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward",
		namespace, podName)
	hostIP := strings.TrimLeft(restConfig.Host, "htps:/")

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", &url.URL{Scheme: "https", Path: path, Host: hostIP})

	pfwd, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, remotePort)}, nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("creating port forward: %w", err)
	}

	return pfwd, nil
}
