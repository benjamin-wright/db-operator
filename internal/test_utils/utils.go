//go:build integration

package test_utils

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nats "github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
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

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
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
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
			},
		},
		Spec: v1alpha1.PostgresDatabaseSpec{
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

func CreateNewUser(namespace, database, username, secretname string, permissions []v1alpha1.DatabasePermissionEntry) {
	pgcred := &v1alpha1.PostgresCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: namespace,
			Labels: map[string]string{
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
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

	username := string(secret.Data["PGUSER"])
	Expect(username).NotTo(BeEmpty(), "PGUSER in secret should not be empty")

	password := string(secret.Data["PGPASSWORD"])
	Expect(password).NotTo(BeEmpty(), "PGPASSWORD in secret should not be empty")

	database := string(secret.Data["PGDATABASE"])
	if database == "" {
		database = "postgres"
	}

	pfwdClose, port := portForward(dbLookup.Namespace, dbLookup.Name+"-0", 5432)

	connStr := fmt.Sprintf("host=localhost port=%d user=%s password=%s dbname=%s sslmode=disable",
		port, username, password, database,
	)

	db, err := sql.Open("postgres", connStr)
	Expect(err).NotTo(HaveOccurred(), "opening database connection")

	return db, func() {
		db.Close()
		pfwdClose()
	}
}

// ConnectToDatabaseNamed opens a connection using credentials from secretLookup
// but overrides the target database name. Useful for testing access to specific
// databases when a credential covers multiple.
func ConnectToDatabaseNamed(dbLookup types.NamespacedName, secretLookup types.NamespacedName, dbName string) (*sql.DB, func()) {
	var secret corev1.Secret
	Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed(), "fetching credential secret")

	username := string(secret.Data["PGUSER"])
	Expect(username).NotTo(BeEmpty(), "PGUSER in secret should not be empty")

	password := string(secret.Data["PGPASSWORD"])
	Expect(password).NotTo(BeEmpty(), "PGPASSWORD in secret should not be empty")

	pfwdClose, port := portForward(dbLookup.Namespace, dbLookup.Name+"-0", 5432)

	connStr := fmt.Sprintf("host=localhost port=%d user=%s password=%s dbname=%s sslmode=disable",
		port, username, password, dbName,
	)

	db, err := sql.Open("postgres", connStr)
	Expect(err).NotTo(HaveOccurred(), "opening database connection")

	return db, func() {
		db.Close()
		pfwdClose()
	}
}

func portForward(namespace, podName string, remotePort int) (func(), uint16) {
	url := Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(RestConfig)
	Expect(err).NotTo(HaveOccurred(), "creating round tripper")
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)

	ready := make(chan struct{})
	stopChan := make(chan struct{})

	pfwd, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", 0, remotePort)}, stopChan, ready, nil, nil)
	Expect(err).NotTo(HaveOccurred(), "creating port forward")

	go func() {
		defer GinkgoRecover()
		if fwdErr := pfwd.ForwardPorts(); fwdErr != nil {
			select {
			case <-stopChan:
				// Graceful shutdown via close — not a failure.
			default:
				Expect(fwdErr).NotTo(HaveOccurred(), "starting port forward")
			}
		}
	}()

	select {
	case <-pfwd.Ready:
	case <-time.After(10 * time.Second):
		Fail("timed out waiting for port forward to be ready")
	}

	ports, err := pfwd.GetPorts()
	Expect(err).NotTo(HaveOccurred(), "getting forwarded ports")
	Expect(ports).To(HaveLen(1), "expect exactly one forwarded port")
	localPort := ports[0].Local

	return func() {
		close(stopChan)
	}, localPort
}

// NewRedisDatabase creates a namespace and a RedisDatabase CR inside it.
// Returns the namespace, the CR, and lookup keys for the CR and its admin Secret.
func NewRedisDatabase(name string) (ns *corev1.Namespace, rdb *v1alpha1.RedisDatabase, dbLookup, adminSecretLookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-rdb-",
		},
	}
	Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

	rdb = &v1alpha1.RedisDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
			},
		},
		Spec: v1alpha1.RedisDatabaseSpec{
			StorageSize: resource.MustParse("256Mi"),
		},
	}
	Expect(K8sClient.Create(Ctx, rdb)).To(Succeed())
	dbLookup = types.NamespacedName{Name: rdb.Name, Namespace: ns.Name}
	adminSecretLookup = types.NamespacedName{Name: rdb.Name + "-admin", Namespace: ns.Name}
	return
}

// WaitForRedisDatabase polls until the RedisDatabase reaches Ready phase.
func WaitForRedisDatabase(lookup types.NamespacedName) {
	Eventually(func(g Gomega) {
		var fetched v1alpha1.RedisDatabase
		g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.RedisDatabasePhaseReady))
	}, Timeout, Interval).Should(Succeed())
}

// NewNatsCluster creates a namespace and a NatsCluster CR inside it, tagged with the
// test operator-instance label. Returns the namespace, cluster, and lookup key.
func NewNatsCluster(name string) (ns *corev1.Namespace, nats *v1alpha1.NatsCluster, lookup types.NamespacedName) {
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-nats-",
		},
	}
	Expect(K8sClient.Create(Ctx, ns)).To(Succeed())

	nats = &v1alpha1.NatsCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"db-operator.benjamin-wright.github.com/operator-instance": "test",
			},
		},
		Spec: v1alpha1.NatsClusterSpec{
			NatsVersion: "2.10",
		},
	}
	Expect(K8sClient.Create(Ctx, nats)).To(Succeed())
	lookup = types.NamespacedName{Name: nats.Name, Namespace: ns.Name}
	return
}

// WaitForNatsCluster polls until the NatsCluster reaches Ready phase.
func WaitForNatsCluster(lookup types.NamespacedName) {
	Eventually(func(g Gomega) {
		var fetched v1alpha1.NatsCluster
		g.Expect(K8sClient.Get(Ctx, lookup, &fetched)).To(Succeed())
		g.Expect(fetched.Status.Phase).To(Equal(v1alpha1.NatsClusterPhaseReady))
	}, Timeout, Interval).Should(Succeed())
}

// ConnectToNats port-forwards to a running NATS pod for the given cluster and returns
// a connected client authenticated with the credentials from secretLookup. The optional
// opts are appended after the default UserInfo and Timeout options, allowing callers to
// override them (e.g. to set a custom ErrorHandler).
func ConnectToNats(clusterLookup types.NamespacedName, secretLookup types.NamespacedName, opts ...nats.Option) (*nats.Conn, func()) {
	var secret corev1.Secret
	Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed(), "fetching NATS credential secret")

	username := string(secret.Data["NATS_USERNAME"])
	password := string(secret.Data["NATS_PASSWORD"])

	podList, err := Clientset.CoreV1().Pods(clusterLookup.Namespace).List(Ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/name=nats,app.kubernetes.io/instance=%s", clusterLookup.Name),
	})
	Expect(err).NotTo(HaveOccurred(), "listing NATS pods for cluster %s", clusterLookup.Name)

	var readyPod *corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				readyPod = pod
				break
			}
		}
		if readyPod != nil {
			break
		}
	}
	Expect(readyPod).NotTo(BeNil(), "no ready NATS pods found for cluster %s", clusterLookup.Name)

	pfwdClose, port := portForward(clusterLookup.Namespace, readyPod.Name, int(nats.DefaultPort))

	allOpts := append([]nats.Option{
		nats.UserInfo(username, password),
		nats.Timeout(10 * time.Second),
	}, opts...)

	nc, err := nats.Connect(fmt.Sprintf("nats://localhost:%d", port), allOpts...)
	Expect(err).NotTo(HaveOccurred(), "connecting to NATS server")

	return nc, func() {
		nc.Close()
		pfwdClose()
	}
}

// ConnectToRedisDatabase opens an authenticated Redis client by port-forwarding
// to the Redis pod and reading credentials from the given Secret.
func ConnectToRedisDatabase(dbLookup types.NamespacedName, secretLookup types.NamespacedName) (*goredis.Client, func()) {
	var secret corev1.Secret
	Expect(K8sClient.Get(Ctx, secretLookup, &secret)).To(Succeed(), "fetching admin secret")

	username := string(secret.Data["REDIS_USERNAME"])
	password := string(secret.Data["REDIS_PASSWORD"])

	pfwdClose, port := portForward(dbLookup.Namespace, dbLookup.Name+"-0", 6379)

	rdb := goredis.NewClient(&goredis.Options{
		Addr:     fmt.Sprintf("localhost:%d", port),
		Username: username,
		Password: password,
	})

	return rdb, func() {
		_ = rdb.Close()
		pfwdClose()
	}
}
