package scanner

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// makeHelmRelease builds a FluxCD HelmRelease (API version v2) with a chart
// template and no status.
func makeHelmRelease(name, namespace, chartName, version string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmReleaseGK.WithVersion("v2"))
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, chartName, "spec", "chart", "spec", "chart")
	_ = unstructured.SetNestedField(obj.Object, version, "spec", "chart", "spec", "version")
	_ = unstructured.SetNestedField(obj.Object, chartName, "spec", "chart", "spec", "sourceRef", "name")
	return obj
}

// snapshot is one entry of a HelmRelease's status.history.
func snapshot(version int64, status, chart, chartVersion, appVersion, release, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"version": version, "status": status, "chartName": chart, "chartVersion": chartVersion,
		"appVersion": appVersion, "name": release, "namespace": namespace,
	}
}

// withHistory sets a HelmRelease's status.history.
func withHistory(hr *unstructured.Unstructured, snaps ...map[string]interface{}) *unstructured.Unstructured {
	history := make([]interface{}, len(snaps))
	for i, s := range snaps {
		history[i] = s
	}
	_ = unstructured.SetNestedSlice(hr.Object, history, "status", "history")
	return hr
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

// noMatchFor returns interceptor functions under which listing kind at any of
// versions fails as if the cluster did not serve it.
func noMatchFor(group, kind string, versions ...string) interceptor.Funcs {
	return interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if ul, ok := list.(*unstructured.UnstructuredList); ok {
				gvk := ul.GroupVersionKind()
				if gvk.Group == group && gvk.Kind == kind+"List" && slices.Contains(versions, gvk.Version) {
					return &apimeta.NoKindMatchError{
						GroupKind:        schema.GroupKind{Group: group, Kind: kind},
						SearchedVersions: []string{gvk.Version},
					}
				}
			}
			return cl.List(ctx, list, opts...)
		},
	}
}

// helmReleases returns a Helm release lister serving byNamespace; it never
// reaches a cluster.
func helmReleases(byNamespace map[string][]*release.Release) HelmReleaseLister {
	return func(_ context.Context, namespace string) ([]*release.Release, error) {
		return byNamespace[namespace], nil
	}
}

func helmRelease(name, namespace, chartName, chartVersion, appVersion, home string, sources ...string) *release.Release {
	return &release.Release{
		Name:      name,
		Namespace: namespace,
		Chart: &chart.Chart{Metadata: &chart.Metadata{
			Name: chartName, Version: chartVersion, AppVersion: appVersion, Home: home, Sources: sources,
		}},
	}
}

func byName(tools []InstalledTool) map[string]InstalledTool {
	m := make(map[string]InstalledTool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}

// --- Flux ---

func TestScanFluxHelmReleases(t *testing.T) {
	objs := []client.Object{
		// No status yet: the spec is all there is.
		makeHelmRelease("cert-manager", "platform", "cert-manager", "v1.12.0"),
		// Deployed release in history, a newer failed attempt after it, and
		// a version range in the spec.
		withHistory(makeHelmRelease("eso", "platform", "external-secrets", "0.9.x"),
			snapshot(3, "failed", "external-secrets", "0.10.0", "v0.10.0", "eso", "platform"),
			snapshot(2, "deployed", "external-secrets", "0.9.5", "v0.9.5", "eso", "platform"),
			snapshot(1, "superseded", "external-secrets", "0.9.0", "v0.9.0", "eso", "platform")),
		// Chart from spec.chartRef (no spec.chart) and a target namespace.
		withHistory(func() *unstructured.Unstructured {
			hr := &unstructured.Unstructured{}
			hr.SetGroupVersionKind(helmReleaseGK.WithVersion("v2"))
			hr.SetName("monitoring-stack")
			hr.SetNamespace("flux-system")
			_ = unstructured.SetNestedField(hr.Object, "monitoring", "spec", "targetNamespace")
			_ = unstructured.SetNestedField(hr.Object, "kps-oci", "spec", "chartRef", "name")
			return hr
		}(), snapshot(1, "deployed", "kube-prometheus-stack", "58.0.0", "v0.73.0", "monitoring-monitoring-stack", "monitoring")),
	}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(objs...).Build()

	s := &ClusterScanner{Client: c}
	tools, err := s.scanFluxHelmReleases(context.Background(), []string{"platform", "flux-system"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byName(tools)
	if len(got) != 3 {
		t.Fatalf("got %d tools, want 3: %+v", len(tools), tools)
	}

	checks := []struct {
		name, chartVersion, appVersion, namespace, release string
		origins                                            []string
	}{
		{"cert-manager", "v1.12.0", "", "platform", "cert-manager", []string{"cert-manager"}},
		{"external-secrets", "0.9.5", "v0.9.5", "platform", "eso", []string{"external-secrets"}},
		{"kube-prometheus-stack", "58.0.0", "v0.73.0", "monitoring", "monitoring-monitoring-stack", []string{"kps-oci"}},
	}
	for _, ck := range checks {
		tool, ok := got[ck.name]
		if !ok {
			t.Errorf("tool %q not found in results", ck.name)
			continue
		}
		if tool.Source != "fluxcd" || tool.ChartName != ck.name {
			t.Errorf("%s: Source=%q ChartName=%q, want fluxcd and %q", ck.name, tool.Source, tool.ChartName, ck.name)
		}
		if tool.ChartVersion != ck.chartVersion || tool.CurrentVersion != ck.appVersion {
			t.Errorf("%s: ChartVersion=%q CurrentVersion=%q, want %q and %q", ck.name, tool.ChartVersion, tool.CurrentVersion, ck.chartVersion, ck.appVersion)
		}
		if tool.Namespace != ck.namespace || tool.ReleaseName != ck.release {
			t.Errorf("%s: Namespace=%q ReleaseName=%q, want %q and %q", ck.name, tool.Namespace, tool.ReleaseName, ck.namespace, ck.release)
		}
		if !slices.Equal(tool.ChartOrigins, ck.origins) {
			t.Errorf("%s: ChartOrigins=%v, want %v", ck.name, tool.ChartOrigins, ck.origins)
		}
	}
}

// TestScanFluxHelmReleases_DefaultReleaseName covers Flux's default release
// name and namespace for a HelmRelease that has not been released yet.
func TestScanFluxHelmReleases_DefaultReleaseName(t *testing.T) {
	hr := makeHelmRelease("vault", "flux-system", "vault", "*")
	_ = unstructured.SetNestedField(hr.Object, "vault", "spec", "targetNamespace")
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(hr).Build()

	tools, err := (&ClusterScanner{Client: c}).scanFluxHelmReleases(context.Background(), []string{"flux-system"})
	if err != nil || len(tools) != 1 {
		t.Fatalf("got %v, %v; want one tool", tools, err)
	}
	if got := tools[0]; got.ReleaseName != "vault-vault" || got.Namespace != "vault" || got.ChartVersion != "" {
		t.Errorf("ReleaseName=%q Namespace=%q ChartVersion=%q, want vault-vault, vault and no chart version for range *",
			got.ReleaseName, got.Namespace, got.ChartVersion)
	}
}

// TestScanFluxHelmReleases_FallsBackToBeta covers a Flux that serves only
// v2beta1: the scanner falls back and reads status.lastAppliedRevision.
func TestScanFluxHelmReleases_FallsBackToBeta(t *testing.T) {
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(helmReleaseGK.WithVersion("v2beta1"))
	hr.SetName("istiod")
	hr.SetNamespace("istio-system")
	_ = unstructured.SetNestedField(hr.Object, "istiod", "spec", "chart", "spec", "chart")
	_ = unstructured.SetNestedField(hr.Object, "1.20.x", "spec", "chart", "spec", "version")
	_ = unstructured.SetNestedField(hr.Object, "1.20.3", "status", "lastAppliedRevision")

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(hr).
		WithInterceptorFuncs(noMatchFor(helmReleaseGK.Group, helmReleaseGK.Kind, "v2", "v2beta2")).
		Build()

	tools, err := (&ClusterScanner{Client: c}).scanFluxHelmReleases(context.Background(), []string{"istio-system"})
	if err != nil || len(tools) != 1 {
		t.Fatalf("got %v, %v; want one tool", tools, err)
	}
	if got := tools[0]; got.Name != "istiod" || got.ChartVersion != "1.20.3" || got.CurrentVersion != "" {
		t.Errorf("got Name=%q ChartVersion=%q CurrentVersion=%q, want istiod, 1.20.3 and no app version", got.Name, got.ChartVersion, got.CurrentVersion)
	}
}

func TestScanFluxHelmReleases_FluxNotInstalled(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithInterceptorFuncs(noMatchFor(helmReleaseGK.Group, helmReleaseGK.Kind, fluxHelmReleaseVersions...)).
		Build()

	tools, err := (&ClusterScanner{Client: c}).scanFluxHelmReleases(context.Background(), []string{"default"})
	if err != nil || len(tools) != 0 {
		t.Errorf("got %v, %v; want no tools and no error", tools, err)
	}
}

// TestScanGitOps_WatchedNamespaces checks which HelmReleases and
// Applications a policy sees: those that install into a watched namespace,
// wherever they live, and those that live in one.
func TestScanGitOps_WatchedNamespaces(t *testing.T) {
	intoVault := makeHelmRelease("vault", "flux-system", "vault", "0.28.0")
	_ = unstructured.SetNestedField(intoVault.Object, "vault", "spec", "targetNamespace")
	elsewhere := makeHelmRelease("velero", "backup", "velero", "7.0.0")

	appIntoVault := makeArgoCDApp("vault", "argocd", "vault", "0.28.0", "https://helm.releases.hashicorp.com")
	_ = unstructured.SetNestedField(appIntoVault.Object, "vault", "spec", "destination", "namespace")
	appElsewhere := makeArgoCDApp("velero", "argocd", "velero", "7.0.0", "https://vmware-tanzu.github.io/helm-charts")
	_ = unstructured.SetNestedField(appElsewhere.Object, "backup", "spec", "destination", "namespace")

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(intoVault, elsewhere, appIntoVault, appElsewhere).
		Build()
	s := &ClusterScanner{Client: c, ListHelmReleases: helmReleases(nil)}

	tools, err := s.ScanAll(context.Background(), []string{"vault"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []string
	for _, tool := range tools {
		got = append(got, tool.Source+"/"+tool.Name)
	}
	if !slices.Equal(got, []string{"fluxcd/vault", "argocd/vault"}) {
		t.Errorf("watching [vault] found %v, want [fluxcd/vault argocd/vault]", got)
	}

	// Watching the namespaces the objects live in finds everything in them:
	// the HelmRelease in flux-system and both Applications in argocd.
	tools, err = s.ScanAll(context.Background(), []string{"flux-system", "argocd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 3 {
		t.Errorf("watching [flux-system argocd] found %d tools, want 3: %+v", len(tools), tools)
	}
}

// --- Flux and Helm together ---

func TestMergeFluxManagedReleases(t *testing.T) {
	flux := []InstalledTool{
		// v2beta1: no app version; the Helm release has it.
		{Name: "istiod", ChartName: "istiod", ChartVersion: "1.20.3", Namespace: "istio-system", ReleaseName: "istiod", Source: "fluxcd", ChartOrigins: []string{"istio"}},
		// Not released yet: no Helm release to merge.
		{Name: "vault", ChartName: "vault", Namespace: "vault", ReleaseName: "vault", Source: "fluxcd"},
	}
	helm := []InstalledTool{
		{Name: "istiod", ChartName: "istiod", ChartVersion: "1.20.3", CurrentVersion: "1.20.3", Namespace: "istio-system", ReleaseName: "istiod", Source: "helm", ChartOrigins: []string{"https://istio.io"}},
		{Name: "cert-manager", ChartName: "cert-manager", ChartVersion: "v1.14.0", CurrentVersion: "v1.14.0", Namespace: "cert-manager", ReleaseName: "cert-manager", Source: "helm"},
		// Same release name in another namespace is a different release.
		{Name: "vault", ChartName: "vault", CurrentVersion: "1.16.1", Namespace: "other", ReleaseName: "vault", Source: "helm"},
	}

	unmanaged := mergeFluxManagedReleases(flux, helm)

	if got := flux[0]; got.CurrentVersion != "1.20.3" || !slices.Equal(got.ChartOrigins, []string{"istio", "https://istio.io"}) {
		t.Errorf("istiod: CurrentVersion=%q ChartOrigins=%v, want 1.20.3 and the Helm chart's origins added", got.CurrentVersion, got.ChartOrigins)
	}
	if flux[1].CurrentVersion != "" {
		t.Errorf("vault: CurrentVersion=%q, want none (its Helm release lives in another namespace)", flux[1].CurrentVersion)
	}
	var names []string
	for _, h := range unmanaged {
		names = append(names, h.Namespace+"/"+h.ReleaseName)
	}
	if !slices.Equal(names, []string{"cert-manager/cert-manager", "other/vault"}) {
		t.Errorf("unmanaged Helm releases = %v, want [cert-manager/cert-manager other/vault]", names)
	}
}

// --- Helm ---

func TestScanPlainHelmReleases(t *testing.T) {
	s := &ClusterScanner{ListHelmReleases: func(_ context.Context, ns string) ([]*release.Release, error) {
		switch ns {
		case "istio-system":
			return []*release.Release{
				helmRelease("istio-base", "istio-system", "base", "1.22.0", "1.22.0", "https://istio.io", "https://github.com/istio/istio"),
				helmRelease("istiod", "istio-system", "istiod", "1.22.0", "1.22.0-distroless", "https://istio.io"),
				{Name: "broken", Namespace: "istio-system"}, // no chart: skipped
			}, nil
		case "denied":
			return nil, errors.New("secrets is forbidden")
		}
		return nil, nil
	}}

	tools, err := s.scanPlainHelmReleases(context.Background(), []string{"denied", "istio-system"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (the denied namespace and the chartless release are skipped): %+v", len(tools), tools)
	}
	base := byName(tools)["base"]
	if base.Source != "helm" || base.ReleaseName != "istio-base" || base.ChartVersion != "1.22.0" || base.CurrentVersion != "1.22.0" {
		t.Errorf("base: %+v", base)
	}
	if !slices.Equal(base.ChartOrigins, []string{"https://istio.io", "https://github.com/istio/istio"}) {
		t.Errorf("base: ChartOrigins=%v, want the chart's home and sources", base.ChartOrigins)
	}
	if got := byName(tools)["istiod"].CurrentVersion; got != "1.22.0" {
		t.Errorf("istiod: CurrentVersion=%q, want the cleaned app version 1.22.0", got)
	}
}

// TestScanAll_FluxManagedHelmRelease checks that a release Flux manages is
// reported once, as fluxcd, completed from the Helm release.
func TestScanAll_FluxManagedHelmRelease(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(makeHelmRelease("cert-manager", "platform", "cert-manager", "1.14.x")).
		Build()
	s := &ClusterScanner{Client: c, ListHelmReleases: helmReleases(map[string][]*release.Release{
		"platform": {
			helmRelease("cert-manager", "platform", "cert-manager", "v1.14.0", "v1.14.0", "https://cert-manager.io"),
			helmRelease("vault", "platform", "vault", "0.28.0", "1.16.1", "https://www.vaultproject.io"),
		},
	})}

	tools, err := s.ScanAll(context.Background(), []string{"platform"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byName(tools)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (cert-manager once): %+v", len(tools), tools)
	}
	cm := got["cert-manager"]
	if cm.Source != "fluxcd" || cm.CurrentVersion != "v1.14.0" || cm.ChartVersion != "v1.14.0" {
		t.Errorf("cert-manager: %+v, want fluxcd with the Helm release's versions", cm)
	}
	if got["vault"].Source != "helm" {
		t.Errorf("vault: Source=%q, want helm", got["vault"].Source)
	}
}

// --- Argo CD ---

func TestScanArgoCDApplications(t *testing.T) {
	// Synced single-source Application: the resolved revision wins over the
	// range in targetRevision, and the images and destination are reported.
	synced := makeArgoCDApp("argo-cd-release", "argocd", "argo-cd", "5.43.*", "https://argoproj.github.io/argo-helm")
	_ = unstructured.SetNestedField(synced.Object, "5.43.0", "status", "sync", "revision")
	_ = unstructured.SetNestedStringSlice(synced.Object, []string{"quay.io/argoproj/argocd:v2.8.0", "redis:7.0.11-alpine"}, "status", "summary", "images")
	_ = unstructured.SetNestedField(synced.Object, "argocd", "spec", "destination", "namespace")

	// Multi-source Application: a chart plus a Git values source.
	multi := &unstructured.Unstructured{}
	multi.SetGroupVersionKind(argoCDAppGVK)
	multi.SetName("vault")
	multi.SetNamespace("argocd")
	_ = unstructured.SetNestedSlice(multi.Object, []interface{}{
		map[string]interface{}{"repoURL": "https://github.com/example/values.git", "targetRevision": "main", "ref": "values"},
		map[string]interface{}{"repoURL": "https://helm.releases.hashicorp.com", "chart": "vault", "targetRevision": "0.28.0",
			"helm": map[string]interface{}{"releaseName": "vault-prod"}},
	}, "spec", "sources")
	_ = unstructured.SetNestedStringSlice(multi.Object, []string{"3f2a1bc", "0.28.0"}, "status", "sync", "revisions")
	_ = unstructured.SetNestedField(multi.Object, "vault", "spec", "destination", "namespace")

	objs := []client.Object{synced, multi, makeArgoCDGitApp("git-app", "argocd", "./manifests")}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(objs...).Build()

	tools, err := (&ClusterScanner{Client: c}).scanArgoCDApplications(context.Background(), []string{"argocd", "vault"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := byName(tools)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (Git sources skipped): %+v", len(tools), tools)
	}

	argo := got["argo-cd"]
	if argo.Source != "argocd" || argo.ChartVersion != "5.43.0" || argo.CurrentVersion != "" || argo.Namespace != "argocd" || argo.ReleaseName != "argo-cd-release" {
		t.Errorf("argo-cd: %+v", argo)
	}
	if !slices.Equal(argo.Images, []string{"quay.io/argoproj/argocd:v2.8.0", "redis:7.0.11-alpine"}) {
		t.Errorf("argo-cd: Images=%v", argo.Images)
	}
	if !slices.Equal(argo.ChartOrigins, []string{"https://argoproj.github.io/argo-helm"}) || argo.RepoURL != "https://argoproj.github.io/argo-helm" {
		t.Errorf("argo-cd: ChartOrigins=%v RepoURL=%q", argo.ChartOrigins, argo.RepoURL)
	}

	vault := got["vault"]
	if vault.ChartVersion != "0.28.0" || vault.Namespace != "vault" || vault.ReleaseName != "vault-prod" || vault.RepoURL != "https://helm.releases.hashicorp.com" {
		t.Errorf("vault: %+v", vault)
	}
}

// TestScanArgoCDApplications_UnsyncedRange covers an Application that has
// not synced: a range in targetRevision is no chart version.
func TestScanArgoCDApplications_UnsyncedRange(t *testing.T) {
	app := makeArgoCDApp("eso", "argocd", "external-secrets", ">=0.9.0", "https://charts.external-secrets.io")
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(app).Build()

	tools, err := (&ClusterScanner{Client: c}).scanArgoCDApplications(context.Background(), []string{"argocd"})
	if err != nil || len(tools) != 1 {
		t.Fatalf("got %v, %v; want one tool", tools, err)
	}
	if got := tools[0]; got.ChartVersion != "" || got.Namespace != "argocd" {
		t.Errorf("ChartVersion=%q Namespace=%q, want no chart version and the Application's namespace", got.ChartVersion, got.Namespace)
	}
}

// --- ScanAll ---

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

	s := &ClusterScanner{Client: c, ListHelmReleases: helmReleases(nil)}
	// 3 flux + 2 argocd (git skipped) + 0 helm
	tools, err := s.ScanAll(context.Background(), []string{"platform", "monitoring", "argocd"})
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
	if got := byName(tools)["kube-prometheus-stack"]; got.Source != "fluxcd" {
		t.Errorf("Flux tools are named by chart: kube-prometheus-stack missing or wrong: %+v", got)
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

func TestScanAll_ResilientToMissingArgoCDCRD(t *testing.T) {
	objs := []client.Object{
		makeHelmRelease("cert-manager", "platform", "cert-manager", "1.12.0"),
	}

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(objs...).
		WithInterceptorFuncs(noMatchFor(argoCDAppGVK.Group, argoCDAppGVK.Kind, argoCDAppGVK.Version)).
		Build()

	core, logs := observer.New(zapcore.WarnLevel)
	ctx := ctrllog.IntoContext(context.Background(), zapr.NewLogger(zap.New(core)))

	s := &ClusterScanner{Client: c, ListHelmReleases: helmReleases(nil)}
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

func TestScanAll_EmptyNamespaces(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		Build()

	s := &ClusterScanner{Client: c, ListHelmReleases: helmReleases(nil)}
	tools, err := s.ScanAll(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("got %d tools, want 0: %v", len(tools), tools)
	}
}

func TestExactVersion(t *testing.T) {
	for in, want := range map[string]string{
		"1.14.0": "1.14.0", "v1.14.0": "v1.14.0", "1.14.x": "", "*": "", ">=1.0.0": "", "~1.2": "", "^2": "", "": "",
	} {
		if got := exactVersion(in); got != want {
			t.Errorf("exactVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
