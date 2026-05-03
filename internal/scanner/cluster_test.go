package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// makeHelmRelease builds a FluxCD HelmRelease unstructured object.
func makeHelmRelease(name, namespace, chartName, version string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmReleaseGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, chartName, "spec", "chart", "spec", "chart")
	_ = unstructured.SetNestedField(obj.Object, version, "spec", "chart", "spec", "version")
	return obj
}

// makeArgoCDApp builds a Helm-based ArgoCD Application unstructured object.
func makeArgoCDApp(name, namespace, chartName, targetRevision, repoURL string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(argoCDAppGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, chartName, "spec", "source", "chart")
	_ = unstructured.SetNestedField(obj.Object, targetRevision, "spec", "source", "targetRevision")
	_ = unstructured.SetNestedField(obj.Object, repoURL, "spec", "source", "repoURL")
	return obj
}

// makeArgoCDGitApp builds a git-based ArgoCD Application (no chart field — must be skipped).
func makeArgoCDGitApp(name, namespace, path string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(argoCDAppGVK)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, path, "spec", "source", "path")
	return obj
}

// --- Test 1: scanFluxHelmReleases ---

func TestScanFluxHelmReleases(t *testing.T) {
	objs := []client.Object{
		makeHelmRelease("cert-manager", "platform", "cert-manager", "1.12.0"),
		makeHelmRelease("external-secrets", "platform", "external-secrets", "0.9.0"),
		makeHelmRelease("prometheus-stack", "monitoring", "kube-prometheus-stack", "45.0.0"),
	}

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(objs...).
		Build()

	s := &ClusterScanner{Client: c}
	tools, err := s.scanFluxHelmReleases(context.Background(), []string{"platform", "monitoring"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3: %v", len(tools), tools)
	}

	byName := make(map[string]InstalledTool)
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	checks := []struct {
		name    string
		version string
		ns      string
	}{
		{"cert-manager", "1.12.0", "platform"},
		{"external-secrets", "0.9.0", "platform"},
		{"prometheus-stack", "45.0.0", "monitoring"},
	}
	for _, ck := range checks {
		got, ok := byName[ck.name]
		if !ok {
			t.Errorf("tool %q not found in results", ck.name)
			continue
		}
		if got.Source != "fluxcd" {
			t.Errorf("%s: Source = %q, want fluxcd", ck.name, got.Source)
		}
		if got.CurrentVersion != ck.version {
			t.Errorf("%s: CurrentVersion = %q, want %q", ck.name, got.CurrentVersion, ck.version)
		}
		if got.Namespace != ck.ns {
			t.Errorf("%s: Namespace = %q, want %q", ck.name, got.Namespace, ck.ns)
		}
	}
}

// --- Test 2: scanArgoCDApplications ---

func TestScanArgoCDApplications(t *testing.T) {
	objs := []client.Object{
		makeArgoCDApp("argo-cd-release", "argocd", "argo-cd", "2.8.0", "https://argoproj.github.io/argo-helm"),
		makeArgoCDApp("vault-release", "argocd", "vault", "0.25.0", "https://helm.releases.hashicorp.com"),
		makeArgoCDGitApp("git-app", "argocd", "./manifests"),
	}

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(objs...).
		Build()

	s := &ClusterScanner{Client: c}
	tools, err := s.scanArgoCDApplications(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (git-based app must be skipped): %v", len(tools), tools)
	}

	byChart := make(map[string]InstalledTool)
	for _, tool := range tools {
		byChart[tool.ChartName] = tool
	}

	checks := []struct {
		chart   string
		version string
		repoURL string
	}{
		{"argo-cd", "2.8.0", "https://argoproj.github.io/argo-helm"},
		{"vault", "0.25.0", "https://helm.releases.hashicorp.com"},
	}
	for _, ck := range checks {
		got, ok := byChart[ck.chart]
		if !ok {
			t.Errorf("chart %q not found in results", ck.chart)
			continue
		}
		if got.Source != "argocd" {
			t.Errorf("%s: Source = %q, want argocd", ck.chart, got.Source)
		}
		if got.CurrentVersion != ck.version {
			t.Errorf("%s: CurrentVersion = %q, want %q", ck.chart, got.CurrentVersion, ck.version)
		}
		if got.RepoURL != ck.repoURL {
			t.Errorf("%s: RepoURL = %q, want %q", ck.chart, got.RepoURL, ck.repoURL)
		}
	}
}

// --- Test 3: ScanAll merges all sources ---

func TestScanAll_MergesAllSources(t *testing.T) {
	objs := []client.Object{
		// FluxCD
		makeHelmRelease("cert-manager", "platform", "cert-manager", "1.12.0"),
		makeHelmRelease("external-secrets", "platform", "external-secrets", "0.9.0"),
		makeHelmRelease("prometheus-stack", "monitoring", "kube-prometheus-stack", "45.0.0"),
		// ArgoCD (git-based skipped)
		makeArgoCDApp("argo-cd-release", "argocd", "argo-cd", "2.8.0", "https://argoproj.github.io/argo-helm"),
		makeArgoCDApp("vault-release", "argocd", "vault", "0.25.0", "https://helm.releases.hashicorp.com"),
		makeArgoCDGitApp("git-app", "argocd", "./manifests"),
	}

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(objs...).
		Build()

	s := &ClusterScanner{Client: c}
	// 3 flux + 2 argocd (git skipped) + 0 helm (no kubeconfig in unit tests)
	tools, err := s.ScanAll(context.Background(), []string{"platform", "monitoring"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("got %d tools, want 5: %v", len(tools), tools)
	}

	sources := make(map[string]int)
	for _, tool := range tools {
		sources[tool.Source]++
	}
	if sources["fluxcd"] != 3 {
		t.Errorf("fluxcd count = %d, want 3", sources["fluxcd"])
	}
	if sources["argocd"] != 2 {
		t.Errorf("argocd count = %d, want 2", sources["argocd"])
	}

	seen := make(map[string]bool)
	for _, tool := range tools {
		key := tool.Source + "/" + tool.ReleaseName
		if seen[key] {
			t.Errorf("duplicate entry: %s", key)
		}
		seen[key] = true
	}
}

// --- Test 4: ScanAll resilient to missing ArgoCD CRD ---

func TestScanAll_ResilientToMissingArgoCDCRD(t *testing.T) {
	objs := []client.Object{
		makeHelmRelease("cert-manager", "platform", "cert-manager", "1.12.0"),
	}

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				ul, ok := list.(*unstructured.UnstructuredList)
				if ok && ul.GetKind() == "ApplicationList" {
					return &apimeta.NoKindMatchError{
						GroupKind:        schema.GroupKind{Group: argoCDAppGVK.Group, Kind: argoCDAppGVK.Kind},
						SearchedVersions: []string{argoCDAppGVK.Version},
					}
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	core, logs := observer.New(zapcore.WarnLevel)
	ctx := ctrllog.IntoContext(context.Background(), zapr.NewLogger(zap.New(core)))

	s := &ClusterScanner{Client: c}
	tools, err := s.ScanAll(ctx, []string{"platform"})
	if err != nil {
		t.Fatalf("ScanAll must not error when ArgoCD CRD is missing: %v", err)
	}

	fluxCount := 0
	for _, tool := range tools {
		if tool.Source == "argocd" {
			t.Errorf("unexpected argocd tool (CRD was not registered): %v", tool)
		}
		if tool.Source == "fluxcd" {
			fluxCount++
		}
	}
	if fluxCount != 1 {
		t.Errorf("fluxcd count = %d, want 1", fluxCount)
	}

	// scanArgoCDApplications returns nil,nil for NoKindMatchError so ScanAll never
	// calls logger.Error — expect zero warn+ entries.
	if logs.Len() != 0 {
		t.Errorf("expected no warn+ log entries, got %d: %v", logs.Len(), logs.All())
	}
}

// --- Test 5: ScanAll with empty namespaces ---

func TestScanAll_EmptyNamespaces(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		Build()

	s := &ClusterScanner{Client: c}
	tools, err := s.ScanAll(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("got %d tools, want 0: %v", len(tools), tools)
	}
}
