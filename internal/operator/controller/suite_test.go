//go:build integration

package controller_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1alpha1 "github.com/benjamin-wright/db-operator/internal/operator/api/v1alpha1"
)

var (
	k8sClient  client.Client
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
	ctx        context.Context
	cancel     context.CancelFunc
	scheme     = runtime.NewScheme()
)

const (
	timeout  = 60 * time.Second
	interval = 250 * time.Millisecond
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	// Register schemes.
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

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
	restConfig = cfg

	// Create a direct client for test assertions.
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create a typed clientset for operations like pod exec.
	clientset, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	cancel()
})

// podExec runs a command inside a pod and returns stdout. It uses the
// suite-level clientset and restConfig so individual tests don't need to
// create their own clients.
func podExec(namespace, podName string, command []string) (string, string, error) {
	req := clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		Param("container", "postgres").
		Param("stdout", "true").
		Param("stderr", "true")
	for _, c := range command {
		req = req.Param("command", c)
	}

	executor, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("creating executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return stdout.String(), stderr.String(), err
	}

	return stdout.String(), stderr.String(), nil
}
