package controller

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// envtestConfig reaches the envtest API server: a local etcd and
// kube-apiserver serving the CRDs in config/crd/bases, for what the fake
// client does not do (CRD defaults and validation, the status subresource).
// It is nil unless KUBEBUILDER_ASSETS names the binaries; `make envtest`
// downloads them and runs the TestEnvtest tests, which skip otherwise.
var envtestConfig *rest.Config

// stopEnvtestProcesses, when set, stops etcd and kube-apiserver before
// envtest does. envtest cannot on Windows (suite_windows_test.go).
var stopEnvtestProcesses func() error

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests starts envtest when its binaries are available, runs the tests and
// stops it again. A start failure fails the run: KUBEBUILDER_ASSETS was set,
// so the envtest tests were meant to run.
func runTests(m *testing.M) int {
	// The reconcilers log through controller-runtime; without a logger it
	// warns once a run takes more than 30s, as one with envtest does.
	ctrllog.SetLogger(zap.New(zap.WriteTo(io.Discard)))

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return m.Run()
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: start the API server: %v\n", err)
		// etcd may be up even though the API server is not.
		stopEnvtest(env)
		return 1
	}
	defer stopEnvtest(env)
	envtestConfig = cfg
	return m.Run()
}

// stopEnvtest stops etcd and kube-apiserver and removes their data,
// reporting failures on stderr.
func stopEnvtest(env *envtest.Environment) {
	if stopEnvtestProcesses != nil {
		if err := stopEnvtestProcesses(); err != nil {
			fmt.Fprintf(os.Stderr, "envtest: stop etcd and kube-apiserver: %v\n", err)
		}
	}
	err := env.Stop()
	if err != nil && stopEnvtestProcesses != nil {
		// envtest may not have seen the processes exit yet and fails trying
		// to signal one, before removing its files; the second try finds
		// them gone and finishes.
		time.Sleep(500 * time.Millisecond)
		err = env.Stop()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop the API server: %v\n", err)
	}
}

// envtestClient returns a client of the envtest API server, or skips the test
// when envtest is not running.
func envtestClient(t *testing.T) client.Client {
	t.Helper()
	if envtestConfig == nil {
		t.Skip("envtest is not running: KUBEBUILDER_ASSETS is unset (run `make envtest`)")
	}
	c, err := client.New(envtestConfig, client.Options{Scheme: newTestScheme(t)})
	if err != nil {
		t.Fatalf("envtest client: %v", err)
	}
	return c
}

// envtestNamespace creates a namespace of its own for a test and returns its
// name. envtest runs no namespace controller, so it is never cleaned up; the
// API server's data goes away when the run ends.
func envtestNamespace(t *testing.T, c client.Client) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "envtest-"}}
	if err := c.Create(context.Background(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return ns.Name
}
