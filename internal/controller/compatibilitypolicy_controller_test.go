package controller

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
	"github.com/jeikeibnaa/kube-viltrumite/internal/scanner"
	"github.com/jeikeibnaa/kube-viltrumite/knowledge"
)

// embeddedKB loads the knowledge base the operator ships and its detection
// rules, converted the way cmd/operator/main.go converts them.
func embeddedKB(t *testing.T) (*planner.Matrix, []scanner.ToolDetection) {
	t.Helper()
	m, err := planner.Load(knowledge.Tools())
	if err != nil {
		t.Fatalf("Load(embedded): %v", err)
	}
	var detections []scanner.ToolDetection
	for tool, spec := range m.Detections() {
		td := scanner.ToolDetection{ToolName: tool, Labels: spec.Labels}
		for _, d := range spec.Deployments {
			td.Deployments = append(td.Deployments, scanner.DeploymentMatch{
				Name: d.Name, NamespaceHint: d.NamespaceHint, Container: d.Container, Image: d.Image, VersionFrom: d.VersionFrom,
			})
		}
		detections = append(detections, td)
	}
	return m, detections
}

func TestResolveInstalled(t *testing.T) {
	m, detections := embeddedKB(t)

	tests := []struct {
		name        string
		found       []scanner.InstalledTool
		wantTool    string
		wantSource  string
		wantVersion string
		wantUnknown []string
	}{
		{
			name:        "Helm chart alias with its app version",
			found:       []scanner.InstalledTool{{Name: "kube-prometheus-stack", ChartName: "kube-prometheus-stack", ChartVersion: "48.0.0", CurrentVersion: "v0.66.0", Source: "helm"}},
			wantTool:    "prometheus-stack",
			wantSource:  "helm",
			wantVersion: "v0.66.0",
		},
		{
			name: "Argo CD: the app version comes from a known image",
			found: []scanner.InstalledTool{{
				Name: "argo-cd", ChartName: "argo-cd", ChartVersion: "5.43.0", Source: "argocd",
				Images: []string{"redis:7.0.11-alpine", "quay.io/argoproj/argocd:v2.8.4"},
			}},
			wantTool:    "argo-cd",
			wantSource:  "argocd",
			wantVersion: "v2.8.4",
		},
		{
			name:        "Argo CD without images: the knowledge base maps the exact chart version",
			found:       []scanner.InstalledTool{{Name: "vault", ChartName: "vault", ChartVersion: "0.28.0", Source: "argocd"}},
			wantTool:    "vault",
			wantSource:  "argocd",
			wantVersion: "1.16.1",
		},
		{
			name:        "a chart version between entries stays unknown",
			found:       []scanner.InstalledTool{{Name: "argo-cd", ChartName: "argo-cd", ChartVersion: "5.46.8", Source: "argocd"}},
			wantTool:    "argo-cd",
			wantSource:  "argocd",
			wantVersion: "",
		},
		{
			name: "istio base counts only with Istio's origin; istiod alone identifies it",
			found: []scanner.InstalledTool{
				{Name: "base", ChartName: "base", CurrentVersion: "1.22.0", Source: "helm", ChartOrigins: []string{"https://istio.io"}},
				{Name: "istiod", ChartName: "istiod", CurrentVersion: "1.22.0", Source: "helm"},
				{Name: "base", ChartName: "base", CurrentVersion: "0.3.0", Source: "helm", ChartOrigins: []string{"https://charts.example.com"}},
			},
			wantTool:    "istio",
			wantSource:  "helm",
			wantVersion: "1.22.0",
			wantUnknown: []string{"base"},
		},
		{
			name: "Helm beats a raw detection of the same tool",
			found: []scanner.InstalledTool{
				{Name: "cert-manager", CurrentVersion: "v1.14.0", Source: "raw"},
				{Name: "cert-manager", ChartName: "cert-manager", CurrentVersion: "v1.14.0", Source: "helm"},
			},
			wantTool:    "cert-manager",
			wantSource:  "helm",
			wantVersion: "v1.14.0",
		},
		{
			name: "a GitOps owner beats Helm",
			found: []scanner.InstalledTool{
				{Name: "external-secrets", ChartName: "external-secrets", CurrentVersion: "v0.9.5", Source: "helm"},
				{Name: "external-secrets", ChartName: "external-secrets", CurrentVersion: "v0.9.5", Source: "fluxcd"},
			},
			wantTool:    "external-secrets",
			wantSource:  "fluxcd",
			wantVersion: "v0.9.5",
		},
		{
			name: "between equal sources, a comparable version wins",
			found: []scanner.InstalledTool{
				{Name: "vault", CurrentVersion: "latest", Source: "raw"},
				{Name: "vault", CurrentVersion: "1.16.1", Source: "raw"},
			},
			wantTool:    "vault",
			wantSource:  "raw",
			wantVersion: "1.16.1",
		},
		{
			name: "charts with no knowledge-base entry are listed once, sorted",
			found: []scanner.InstalledTool{
				{Name: "velero", ChartName: "velero", Source: "helm"},
				{Name: "ingress-nginx", ChartName: "ingress-nginx", Source: "argocd"},
				{Name: "velero", ChartName: "velero", Source: "fluxcd"},
			},
			wantUnknown: []string{"ingress-nginx", "velero"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installed, unknown := resolveInstalled(m, detections, tc.found)
			if !slices.Equal(unknown, tc.wantUnknown) {
				t.Errorf("unknown = %v, want %v", unknown, tc.wantUnknown)
			}
			if tc.wantTool == "" {
				if len(installed) != 0 {
					t.Errorf("installed = %+v, want none", installed)
				}
				return
			}
			if len(installed) != 1 {
				t.Fatalf("installed = %+v, want only %s", installed, tc.wantTool)
			}
			got, ok := installed[tc.wantTool]
			if !ok {
				t.Fatalf("installed = %+v, want %s", installed, tc.wantTool)
			}
			if got.Name != tc.wantTool || got.Source != tc.wantSource || got.CurrentVersion != tc.wantVersion {
				t.Errorf("got Name=%q Source=%q CurrentVersion=%q, want %q, %q, %q",
					got.Name, got.Source, got.CurrentVersion, tc.wantTool, tc.wantSource, tc.wantVersion)
			}
		})
	}
}

// --- Reconcile against a fake API server ---

// policyNamespace is where the test policies live, and so the plans autoplan
// creates.
const policyNamespace = "viltrumite-system"

// watchAll lists every namespace the test clusters install tools into. Flux
// HelmReleases live in flux-system, which no test policy watches.
var watchAll = []string{"argocd", "cert-manager", "external-secrets", "istio-system", "monitoring", "vault"}

// testCluster is what a fake cluster holds besides the policy: API objects
// (workloads, Flux HelmReleases, Argo CD Applications, StackUpgrades) and the
// Helm releases stored in each namespace.
type testCluster struct {
	objects []client.Object
	helm    map[string][]*release.Release
}

// newPolicyReconciler returns a reconciler over a fake API server that holds
// policy (unless nil) and cluster and serves status as a subresource of both
// CRDs, as the real CRDs do (TestEnvtestStatusSubresource). funcs intercept
// client calls.
func newPolicyReconciler(t *testing.T, policy *kubeviltrumitev1alpha1.CompatibilityPolicy, cluster testCluster, funcs interceptor.Funcs) *CompatibilityPolicyReconciler {
	t.Helper()
	objs := slices.Clone(cluster.objects)
	if policy != nil {
		objs = append(objs, policy)
	}
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&kubeviltrumitev1alpha1.CompatibilityPolicy{}, &kubeviltrumitev1alpha1.StackUpgrade{}).
		WithInterceptorFuncs(funcs).
		Build()
	return policyReconcilerFor(t, c, cluster.helm)
}

// policyReconcilerFor returns a reconciler over c with the embedded knowledge
// base, wired as cmd/operator/main.go wires it except that Helm releases come
// from helm: a test never reaches the cluster in the current kube-context.
func policyReconcilerFor(t *testing.T, c client.Client, helm map[string][]*release.Release) *CompatibilityPolicyReconciler {
	t.Helper()
	m, detections := embeddedKB(t)
	return &CompatibilityPolicyReconciler{
		Client: c,
		Scheme: c.Scheme(),
		Scanner: &scanner.ClusterScanner{
			Client: c,
			ListHelmReleases: func(_ context.Context, namespace string) ([]*release.Release, error) {
				return helm[namespace], nil
			},
		},
		WorkloadScanner: &scanner.WorkloadScanner{Client: c},
		Matrix:          m,
		Detections:      detections,
	}
}

// reconcilePolicy reconciles policy once and returns the result and the
// policy as stored afterwards.
func reconcilePolicy(t *testing.T, r *CompatibilityPolicyReconciler, policy *kubeviltrumitev1alpha1.CompatibilityPolicy) (ctrl.Result, kubeviltrumitev1alpha1.CompatibilityPolicy) {
	t.Helper()
	ctx := context.Background()
	key := client.ObjectKeyFromObject(policy)
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got kubeviltrumitev1alpha1.CompatibilityPolicy
	if err := r.Client.Get(ctx, key, &got); err != nil {
		t.Fatalf("Get policy: %v", err)
	}
	return res, got
}

// newPolicy returns the policy "platform" in policyNamespace.
func newPolicy(spec kubeviltrumitev1alpha1.CompatibilityPolicySpec) *kubeviltrumitev1alpha1.CompatibilityPolicy {
	return &kubeviltrumitev1alpha1.CompatibilityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: policyNamespace},
		Spec:       spec,
	}
}

// rawDeployment is a Deployment a raw (kubectl apply) install runs, with one
// container named after it.
func rawDeployment(name, namespace, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: podTemplate(name, image),
		},
	}
}

// rawStatefulSet is rawDeployment's StatefulSet.
func rawStatefulSet(name, namespace, image string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: podTemplate(name, image),
		},
	}
}

func podTemplate(name, image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: image}}},
	}
}

// helmRelease is a plain Helm release of a chart; origins are the chart's
// home and then its sources.
func helmRelease(name, namespace, chartName, chartVersion, appVersion string, origins ...string) *release.Release {
	md := &chart.Metadata{Name: chartName, Version: chartVersion, AppVersion: appVersion}
	if len(origins) > 0 {
		md.Home, md.Sources = origins[0], origins[1:]
	}
	return &release.Release{Name: name, Namespace: namespace, Chart: &chart.Chart{Metadata: md}}
}

// fluxHelmRelease is a Flux v2 HelmRelease in flux-system that installed a
// chart into namespace. Its status.history records the deployed release under
// Flux's default name, "<namespace>-<name>".
func fluxHelmRelease(name, namespace, chartName, chartVersion, appVersion string) *unstructured.Unstructured {
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"})
	hr.SetName(name)
	hr.SetNamespace("flux-system")
	_ = unstructured.SetNestedField(hr.Object, chartName, "spec", "chart", "spec", "chart")
	_ = unstructured.SetNestedField(hr.Object, namespace, "spec", "targetNamespace")
	_ = unstructured.SetNestedSlice(hr.Object, []interface{}{map[string]interface{}{
		"version": int64(1), "status": "deployed", "name": namespace + "-" + name, "namespace": namespace,
		"chartName": chartName, "chartVersion": chartVersion, "appVersion": appVersion,
	}}, "status", "history")
	return hr
}

// argoApplication is an Argo CD Application in the argocd namespace that
// deploys a chart at revision into namespace and reports running images.
func argoApplication(name, namespace, chartName, revision string, images ...string) *unstructured.Unstructured {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
	app.SetName(name)
	app.SetNamespace("argocd")
	_ = unstructured.SetNestedMap(app.Object, map[string]interface{}{
		"repoURL": "https://charts.example.com", "chart": chartName, "targetRevision": revision,
	}, "spec", "source")
	_ = unstructured.SetNestedField(app.Object, namespace, "spec", "destination", "namespace")
	if len(images) > 0 {
		_ = unstructured.SetNestedStringSlice(app.Object, images, "status", "summary", "images")
	}
	return app
}

// TestPolicyReconcile_DiscoveryReport reconciles a policy without
// trackedTools over a cluster that runs five of the six knowledge-base tools
// through every source, and checks the whole report: one status message of
// each kind.
func TestPolicyReconcile_DiscoveryReport(t *testing.T) {
	policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{
		WatchNamespaces: []string{"argocd", "backup", "cert-manager", "external-secrets", "istio-system", "monitoring", "vault"},
	})
	cluster := testCluster{
		objects: []client.Object{
			// A chart version between two knowledge-base entries, and no images.
			argoApplication("argo-cd", "argocd", "argo-cd", "5.46.8"),
			rawDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.14.0"),
			fluxHelmRelease("external-secrets", "external-secrets", "external-secrets", "0.9.5", "v0.9.5"),
			rawDeployment("prometheus-operator", "monitoring", "quay.io/prometheus-operator/prometheus-operator:latest"),
		},
		helm: map[string][]*release.Release{
			"backup": {helmRelease("velero", "backup", "velero", "7.2.1", "1.14.0")},
			// The Helm release the Flux HelmRelease manages: reported once, as fluxcd.
			"external-secrets": {helmRelease("external-secrets-external-secrets", "external-secrets", "external-secrets", "0.9.5", "v0.9.5")},
			// A chart named like Istio's base chart that does not come from Istio.
			"istio-system": {helmRelease("base", "istio-system", "base", "0.3.0", "0.3.0", "https://charts.example.com")},
			"vault":        {helmRelease("vault", "vault", "vault", "0.28.0", "1.16.1", "https://www.vaultproject.io")},
		},
	}
	r := newPolicyReconciler(t, policy, cluster, interceptor.Funcs{})

	res, got := reconcilePolicy(t, r, policy)

	// riskTolerance is unset, so HIGH: cert-manager's HIGH-risk 1.15.0 is recommended.
	want := kubeviltrumitev1alpha1.CompatibilityPolicyStatus{
		Mode: "discovery",
		Tools: []kubeviltrumitev1alpha1.TrackedToolStatus{
			{Name: "argo-cd", Installed: true, Source: "argocd", Namespace: "argocd", Message: "installed version unknown"},
			{Name: "cert-manager", Installed: true, InstalledVersion: "v1.14.0", Source: "raw", Namespace: "cert-manager",
				UpgradeAvailable: true, RecommendedVersion: "1.15.0", Risk: ai.RiskHigh, Message: "upgrade available"},
			{Name: "external-secrets", Installed: true, InstalledVersion: "v0.9.5", Source: "fluxcd", Namespace: "external-secrets",
				UpgradeAvailable: true, RecommendedVersion: "0.10.5", Risk: ai.RiskLow, Message: "upgrade available"},
			{Name: "istio", Message: "not currently installed in cluster"},
			{Name: "prometheus-stack", Installed: true, InstalledVersion: "latest", Source: "raw", Namespace: "monitoring",
				Message: `installed version "latest" not recognised`},
			{Name: "vault", Installed: true, InstalledVersion: "1.16.1", Source: "helm", Namespace: "vault", Message: "up to date"},
		},
		UnknownInstalled: []string{"base", "velero"},
	}
	if got.Status.LastScanTime == nil {
		t.Error("status.lastScanTime is not set")
	}
	got.Status.LastScanTime = nil
	if !reflect.DeepEqual(got.Status, want) {
		t.Errorf("status:\n got %+v\nwant %+v", got.Status, want)
	}
	if res != (ctrl.Result{RequeueAfter: 5 * time.Minute}) {
		t.Errorf("result = %+v, want RequeueAfter 5m", res)
	}
}

// TestPolicyReconcile_TrackedTools covers discovery and focused mode. Each
// policy starts with a stale status, which the scan replaces.
func TestPolicyReconcile_TrackedTools(t *testing.T) {
	tests := []struct {
		name            string
		tracked         []string
		wantMode        string
		wantTools       []string
		wantUntrackable []string
	}{
		{
			name:      "none: discovery mode evaluates every knowledge-base tool",
			wantMode:  "discovery",
			wantTools: []string{"argo-cd", "cert-manager", "external-secrets", "istio", "prometheus-stack", "vault"},
		},
		{
			name:      "aliases name their tools, kept in the order given",
			tracked:   []string{"kube-prometheus-stack", "argocd", "istiod"},
			wantMode:  "focused",
			wantTools: []string{"prometheus-stack", "argo-cd", "istio"},
		},
		{
			name:      "a tool named twice, directly or by an alias, is evaluated once",
			tracked:   []string{"istio", "cert-manager", "base", "cert-manager"},
			wantMode:  "focused",
			wantTools: []string{"istio", "cert-manager"},
		},
		{
			name:            "names without a knowledge-base entry are untrackable, each listed once",
			tracked:         []string{"velero", "vault", "ingress-nginx", "velero"},
			wantMode:        "focused",
			wantTools:       []string{"vault"},
			wantUntrackable: []string{"velero", "ingress-nginx"},
		},
		{
			name:            "only unknown names: focused mode with nothing to evaluate",
			tracked:         []string{"velero"},
			wantMode:        "focused",
			wantUntrackable: []string{"velero"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{WatchNamespaces: watchAll, TrackedTools: tc.tracked})
			policy.Status = kubeviltrumitev1alpha1.CompatibilityPolicyStatus{
				Mode:             "stale",
				Tools:            []kubeviltrumitev1alpha1.TrackedToolStatus{{Name: "stale"}},
				UntrackableTools: []string{"stale"},
				UnknownInstalled: []string{"stale"},
			}
			r := newPolicyReconciler(t, policy, testCluster{}, interceptor.Funcs{})

			_, got := reconcilePolicy(t, r, policy)

			var tools []string
			for _, ts := range got.Status.Tools {
				tools = append(tools, ts.Name)
				if ts.Installed || ts.Message != "not currently installed in cluster" {
					t.Errorf("%s: %+v, want not installed (the cluster is empty)", ts.Name, ts)
				}
			}
			if got.Status.Mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", got.Status.Mode, tc.wantMode)
			}
			if !slices.Equal(tools, tc.wantTools) {
				t.Errorf("tools = %v, want %v", tools, tc.wantTools)
			}
			if !slices.Equal(got.Status.UntrackableTools, tc.wantUntrackable) {
				t.Errorf("untrackableTools = %v, want %v", got.Status.UntrackableTools, tc.wantUntrackable)
			}
			if got.Status.UnknownInstalled != nil {
				t.Errorf("unknownInstalled = %v, want none", got.Status.UnknownInstalled)
			}
		})
	}
}

// TestPolicyReconcile_ToolStatus reconciles a focused policy that tracks one
// tool and checks the tool's record, for each way a tool can be installed.
func TestPolicyReconcile_ToolStatus(t *testing.T) {
	tests := []struct {
		name    string
		tracked string
		cluster testCluster
		want    kubeviltrumitev1alpha1.TrackedToolStatus
	}{
		{
			name:    "nothing installed",
			tracked: "cert-manager",
			want:    kubeviltrumitev1alpha1.TrackedToolStatus{Name: "cert-manager", Message: "not currently installed in cluster"},
		},
		{
			name:    "Helm release of a chart named by an alias",
			tracked: "kube-prometheus-stack",
			cluster: testCluster{helm: map[string][]*release.Release{
				"monitoring": {helmRelease("kube-prometheus-stack", "monitoring", "kube-prometheus-stack", "48.0.0", "v0.66.0")},
			}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "prometheus-stack", Installed: true, InstalledVersion: "v0.66.0", Source: "helm", Namespace: "monitoring",
				UpgradeAvailable: true, RecommendedVersion: "0.76.0", Risk: ai.RiskMedium, Message: "upgrade available",
			},
		},
		{
			name:    "Helm wins over a raw detection of the same tool",
			tracked: "cert-manager",
			cluster: testCluster{
				objects: []client.Object{rawDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.13.0")},
				helm: map[string][]*release.Release{
					"cert-manager": {helmRelease("cert-manager", "cert-manager", "cert-manager", "v1.14.0", "v1.14.0")},
				},
			},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "cert-manager", Installed: true, InstalledVersion: "v1.14.0", Source: "helm", Namespace: "cert-manager",
				UpgradeAvailable: true, RecommendedVersion: "1.15.0", Risk: ai.RiskHigh, Message: "upgrade available",
			},
		},
		{
			name:    "Argo CD: the app version comes from a known image",
			tracked: "argocd",
			cluster: testCluster{objects: []client.Object{
				argoApplication("argo-cd", "argocd", "argo-cd", "5.43.0", "redis:7.0.11-alpine", "quay.io/argoproj/argocd:v2.8.4"),
			}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "argo-cd", Installed: true, InstalledVersion: "v2.8.4", Source: "argocd", Namespace: "argocd",
				UpgradeAvailable: true, RecommendedVersion: "2.12.0", Risk: ai.RiskMedium, Message: "upgrade available",
			},
		},
		{
			name:    "Argo CD: the knowledge base maps the exact chart version",
			tracked: "vault",
			cluster: testCluster{objects: []client.Object{argoApplication("vault", "vault", "vault", "0.25.0")}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "vault", Installed: true, InstalledVersion: "1.14.0", Source: "argocd", Namespace: "vault",
				UpgradeAvailable: true, RecommendedVersion: "1.16.1", Risk: ai.RiskMedium, Message: "upgrade available",
			},
		},
		{
			name:    "istio's base chart counts when it comes from Istio",
			tracked: "istio",
			cluster: testCluster{helm: map[string][]*release.Release{
				"istio-system": {helmRelease("istio-base", "istio-system", "base", "1.21.0", "1.21.0", "https://istio.io")},
			}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "istio", Installed: true, InstalledVersion: "1.21.0", Source: "helm", Namespace: "istio-system",
				UpgradeAvailable: true, RecommendedVersion: "1.22.0", Risk: ai.RiskHigh, Message: "upgrade available",
			},
		},
		{
			name:    "raw image without a tag: installed version unknown",
			tracked: "istio",
			cluster: testCluster{objects: []client.Object{rawDeployment("istiod", "istio-system", "docker.io/istio/pilot")}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "istio", Installed: true, Source: "raw", Namespace: "istio-system", Message: "installed version unknown",
			},
		},
		{
			name:    "raw StatefulSet whose tag is not a version",
			tracked: "vault",
			cluster: testCluster{objects: []client.Object{rawStatefulSet("vault", "vault", "hashicorp/vault:latest")}},
			want: kubeviltrumitev1alpha1.TrackedToolStatus{
				Name: "vault", Installed: true, InstalledVersion: "latest", Source: "raw", Namespace: "vault",
				Message: `installed version "latest" not recognised`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{WatchNamespaces: watchAll, TrackedTools: []string{tc.tracked}})
			r := newPolicyReconciler(t, policy, tc.cluster, interceptor.Funcs{})

			_, got := reconcilePolicy(t, r, policy)

			want := []kubeviltrumitev1alpha1.TrackedToolStatus{tc.want}
			if !reflect.DeepEqual(got.Status.Tools, want) {
				t.Errorf("tools:\n got %+v\nwant %+v", got.Status.Tools, want)
			}
		})
	}
}

// TestPolicyReconcile_RiskTolerance checks that the recommendation is the
// newest version at or below riskTolerance, and that an unset tolerance means
// HIGH. cert-manager's knowledge base: 1.13.0 and 1.14.0 LOW, 1.15.0 HIGH.
func TestPolicyReconcile_RiskTolerance(t *testing.T) {
	tests := []struct {
		name        string
		tolerance   ai.RiskLevel
		installed   string
		wantVersion string
		wantRisk    ai.RiskLevel
		wantMessage string
	}{
		{"unset means HIGH", "", "v1.12.0", "1.15.0", ai.RiskHigh, "upgrade available"},
		{"LOW", ai.RiskLow, "v1.12.0", "1.14.0", ai.RiskLow, "upgrade available"},
		{"MEDIUM", ai.RiskMedium, "v1.12.0", "1.14.0", ai.RiskLow, "upgrade available"},
		{"HIGH", ai.RiskHigh, "v1.12.0", "1.15.0", ai.RiskHigh, "upgrade available"},
		{"the only newer version is riskier than the tolerance", ai.RiskLow, "v1.14.0", "", "", "up to date"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{
				WatchNamespaces: watchAll,
				TrackedTools:    []string{"cert-manager"},
				RiskTolerance:   tc.tolerance,
			})
			cluster := testCluster{objects: []client.Object{
				rawDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:"+tc.installed),
			}}
			r := newPolicyReconciler(t, policy, cluster, interceptor.Funcs{})

			_, got := reconcilePolicy(t, r, policy)

			if len(got.Status.Tools) != 1 {
				t.Fatalf("tools = %+v, want cert-manager only", got.Status.Tools)
			}
			ts := got.Status.Tools[0]
			if ts.RecommendedVersion != tc.wantVersion || ts.Risk != tc.wantRisk || ts.Message != tc.wantMessage ||
				ts.UpgradeAvailable != (tc.wantVersion != "") {
				t.Errorf("got recommended %q (%s), upgradeAvailable %t, %q; want %q (%s), %q",
					ts.RecommendedVersion, ts.Risk, ts.UpgradeAvailable, ts.Message, tc.wantVersion, tc.wantRisk, tc.wantMessage)
			}
		})
	}
}

// autoplanCluster runs three tools with an upgrade available at each risk
// level when riskTolerance is unset (HIGH): see autoplanPlans.
func autoplanCluster() testCluster {
	return testCluster{
		objects: []client.Object{rawDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.12.0")},
		helm: map[string][]*release.Release{
			"external-secrets": {helmRelease("external-secrets", "external-secrets", "external-secrets", "0.9.5", "v0.9.5")},
			"vault":            {helmRelease("vault", "vault", "vault", "0.25.0", "1.14.0")},
		},
	}
}

// autoplanPlans are the StackUpgrades autoplan may create over
// autoplanCluster, by name.
var autoplanPlans = map[string]kubeviltrumitev1alpha1.ToolUpgradeSpec{
	"auto-cert-manager-1.15.0":     {Name: "cert-manager", CurrentVersion: "v1.12.0", TargetVersion: "1.15.0", Risk: ai.RiskHigh},
	"auto-external-secrets-0.10.5": {Name: "external-secrets", CurrentVersion: "v0.9.5", TargetVersion: "0.10.5", Risk: ai.RiskLow},
	"auto-vault-1.16.1":            {Name: "vault", CurrentVersion: "1.14.0", TargetVersion: "1.16.1", Risk: ai.RiskMedium},
}

// listUpgrades returns the StackUpgrades opts select, sorted by name.
func listUpgrades(t *testing.T, c client.Client, opts ...client.ListOption) []kubeviltrumitev1alpha1.StackUpgrade {
	t.Helper()
	var list kubeviltrumitev1alpha1.StackUpgradeList
	if err := c.List(context.Background(), &list, opts...); err != nil {
		t.Fatalf("List StackUpgrades: %v", err)
	}
	slices.SortFunc(list.Items, func(a, b kubeviltrumitev1alpha1.StackUpgrade) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return list.Items
}

// TestPolicyReconcile_Autoplan checks the push step: plans are created only
// when autoplan is enabled, only up to maxRisk (LOW when unset), in the
// policy's namespace, and already Approved when autoApprove is set.
func TestPolicyReconcile_Autoplan(t *testing.T) {
	tests := []struct {
		name      string
		autoplan  *kubeviltrumitev1alpha1.AutoplanConfig
		wantPlans []string
	}{
		{name: "no autoplan: report only"},
		{
			name:     "disabled: nothing is created, whatever else it sets",
			autoplan: &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: false, MaxRisk: ai.RiskBlocking, AutoApprove: true},
		},
		{
			name:      "maxRisk unset means LOW",
			autoplan:  &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true},
			wantPlans: []string{"auto-external-secrets-0.10.5"},
		},
		{
			name:      "maxRisk MEDIUM",
			autoplan:  &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true, MaxRisk: ai.RiskMedium},
			wantPlans: []string{"auto-external-secrets-0.10.5", "auto-vault-1.16.1"},
		},
		{
			name:      "maxRisk HIGH",
			autoplan:  &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true, MaxRisk: ai.RiskHigh},
			wantPlans: []string{"auto-cert-manager-1.15.0", "auto-external-secrets-0.10.5", "auto-vault-1.16.1"},
		},
		{
			name:      "autoApprove creates the plan Approved",
			autoplan:  &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true, MaxRisk: ai.RiskLow, AutoApprove: true},
			wantPlans: []string{"auto-external-secrets-0.10.5"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{WatchNamespaces: watchAll, Autoplan: tc.autoplan})
			r := newPolicyReconciler(t, policy, autoplanCluster(), interceptor.Funcs{})

			reconcilePolicy(t, r, policy)

			autoApprove := tc.autoplan != nil && tc.autoplan.AutoApprove
			wantPhase := kubeviltrumitev1alpha1.UpgradePhase("")
			if autoApprove {
				wantPhase = kubeviltrumitev1alpha1.UpgradePhaseApproved
			}
			var names []string
			for _, su := range listUpgrades(t, r.Client) {
				names = append(names, su.Name)
				want := []kubeviltrumitev1alpha1.ToolUpgradeSpec{autoplanPlans[su.Name]}
				if su.Namespace != policyNamespace {
					t.Errorf("%s: namespace %q, want the policy's, %q", su.Name, su.Namespace, policyNamespace)
				}
				if !reflect.DeepEqual(su.Spec.Tools, want) {
					t.Errorf("%s: tools %+v, want %+v", su.Name, su.Spec.Tools, want)
				}
				if su.Spec.ApprovalRequired == autoApprove {
					t.Errorf("%s: approvalRequired %t, want %t", su.Name, su.Spec.ApprovalRequired, !autoApprove)
				}
				if su.Status.Phase != wantPhase {
					t.Errorf("%s: phase %q, want %q", su.Name, su.Status.Phase, wantPhase)
				}
			}
			if !slices.Equal(names, tc.wantPlans) {
				t.Errorf("plans = %v, want %v", names, tc.wantPlans)
			}
		})
	}
}

// TestPolicyReconcile_AutoplanIdempotent checks that each plan is created
// once: a StackUpgrade that already has a plan's name is left as it is, and a
// second scan creates nothing.
func TestPolicyReconcile_AutoplanIdempotent(t *testing.T) {
	// A plan an earlier scan created, since carried out.
	done := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "auto-external-secrets-0.10.5", Namespace: policyNamespace},
		Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
			{Name: "external-secrets", CurrentVersion: "v0.9.0", TargetVersion: "0.10.5", Risk: ai.RiskLow},
		}},
		Status: kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseSucceeded},
	}
	cluster := autoplanCluster()
	cluster.objects = append(cluster.objects, done)
	policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{
		WatchNamespaces: watchAll,
		Autoplan:        &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true, MaxRisk: ai.RiskHigh, AutoApprove: true},
	})
	var created []string
	r := newPolicyReconciler(t, policy, cluster, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			created = append(created, obj.GetName())
			return c.Create(ctx, obj, opts...)
		},
	})
	ctx := context.Background()
	var before kubeviltrumitev1alpha1.StackUpgrade
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(done), &before); err != nil {
		t.Fatalf("Get %s: %v", done.Name, err)
	}

	reconcilePolicy(t, r, policy)
	reconcilePolicy(t, r, policy)

	if want := []string{"auto-cert-manager-1.15.0", "auto-vault-1.16.1"}; !slices.Equal(created, want) {
		t.Errorf("created %v over two scans, want %v", created, want)
	}
	if n := len(listUpgrades(t, r.Client)); n != 3 {
		t.Errorf("%d StackUpgrades, want 3", n)
	}
	var after kubeviltrumitev1alpha1.StackUpgrade
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(done), &after); err != nil {
		t.Fatalf("Get %s: %v", done.Name, err)
	}
	if after.ResourceVersion != before.ResourceVersion || after.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseSucceeded {
		t.Errorf("existing plan changed: resourceVersion %s -> %s, phase %q", before.ResourceVersion, after.ResourceVersion, after.Status.Phase)
	}
}

// TestPolicyReconcile_RequeueAfter checks that the next scan comes after
// scanInterval, 5m when unset, including when the reconciler is not
// configured and skips the scan.
func TestPolicyReconcile_RequeueAfter(t *testing.T) {
	tests := []struct {
		name      string
		interval  time.Duration
		noScanner bool
		want      time.Duration
	}{
		{name: "unset means 5m", want: 5 * time.Minute},
		{name: "scanInterval", interval: time.Hour, want: time.Hour},
		{name: "no scanner: no scan, same interval", interval: 30 * time.Second, noScanner: true, want: 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{
				WatchNamespaces: watchAll,
				ScanInterval:    metav1.Duration{Duration: tc.interval},
			})
			r := newPolicyReconciler(t, policy, testCluster{}, interceptor.Funcs{})
			if tc.noScanner {
				r.Scanner = nil
			}

			res, got := reconcilePolicy(t, r, policy)

			if res != (ctrl.Result{RequeueAfter: tc.want}) {
				t.Errorf("result = %+v, want RequeueAfter %s", res, tc.want)
			}
			if scanned := got.Status.LastScanTime != nil; scanned == tc.noScanner {
				t.Errorf("lastScanTime = %v, want a scan: %t", got.Status.LastScanTime, !tc.noScanner)
			}
		})
	}
}

// TestPolicyReconcile_PolicyDeleted checks that a request for a policy that
// no longer exists ends quietly: no error, no requeue.
func TestPolicyReconcile_PolicyDeleted(t *testing.T) {
	r := newPolicyReconciler(t, nil, testCluster{}, interceptor.Funcs{})

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "deleted", Namespace: policyNamespace},
	})
	if err != nil || res != (ctrl.Result{}) {
		t.Errorf("Reconcile = %+v, %v; want an empty result and no error", res, err)
	}
}

// TestPolicyReconcile_WorkloadScanFails checks that a failed raw scan costs
// only the raw results: the reconcile succeeds and the Helm find is reported.
func TestPolicyReconcile_WorkloadScanFails(t *testing.T) {
	policy := newPolicy(kubeviltrumitev1alpha1.CompatibilityPolicySpec{WatchNamespaces: watchAll, TrackedTools: []string{"cert-manager", "vault"}})
	cluster := testCluster{
		objects: []client.Object{rawDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.14.0")},
		helm:    map[string][]*release.Release{"vault": {helmRelease("vault", "vault", "vault", "0.28.0", "1.16.1")}},
	}
	r := newPolicyReconciler(t, policy, cluster, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*appsv1.DeploymentList); ok {
				return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied"))
			}
			return c.List(ctx, list, opts...)
		},
	})

	_, got := reconcilePolicy(t, r, policy)

	// The report cannot tell a failed raw scan from no install; only the
	// operator log says the scan failed.
	want := []kubeviltrumitev1alpha1.TrackedToolStatus{
		{Name: "cert-manager", Message: "not currently installed in cluster"},
		{Name: "vault", Installed: true, InstalledVersion: "1.16.1", Source: "helm", Namespace: "vault", Message: "up to date"},
	}
	if !reflect.DeepEqual(got.Status.Tools, want) {
		t.Errorf("tools:\n got %+v\nwant %+v", got.Status.Tools, want)
	}
}

// --- envtest: a real API server with the CRDs from config/crd/bases ---

// TestEnvtestCompatibilityPolicyValidation creates policies from manifests,
// as kubectl apply sends them, and checks what the CRD schema rejects.
func TestEnvtestCompatibilityPolicyValidation(t *testing.T) {
	c := envtestClient(t)
	ns := envtestNamespace(t, c)

	type obj = map[string]interface{}
	tests := []struct {
		name    string
		spec    obj
		wantErr string // part of the rejection; "" means accepted
	}{
		{name: "empty spec", spec: obj{}},
		{
			name: "every field set",
			spec: obj{
				"watchNamespaces": []interface{}{"cert-manager"},
				"trackedTools":    []interface{}{"cert-manager", "argocd"},
				"riskTolerance":   "MEDIUM",
				"scanInterval":    "10m",
				"autoApprove":     obj{"patchVersions": true, "minorVersions": false},
				"autoplan":        obj{"enabled": true, "maxRisk": "MEDIUM", "autoApprove": true},
				"ai":              obj{"provider": "ollama", "endpoint": "http://ollama:11434", "model": "llama3.1", "timeout": "30s"},
				"gitRepo":         obj{"url": "https://github.com/example/platform", "branch": "main", "path": "clusters/prod"},
			},
		},
		{
			name:    "riskTolerance outside the risk levels",
			spec:    obj{"riskTolerance": "medium"},
			wantErr: `spec.riskTolerance: Unsupported value: "medium"`,
		},
		{
			name:    "autoplan.maxRisk outside the risk levels",
			spec:    obj{"autoplan": obj{"enabled": true, "maxRisk": "EXTREME"}},
			wantErr: `spec.autoplan.maxRisk: Unsupported value: "EXTREME"`,
		},
		{
			name:    "autoplan without enabled",
			spec:    obj{"autoplan": obj{"maxRisk": "LOW"}},
			wantErr: "spec.autoplan.enabled: Required value",
		},
		{
			name:    "ai.provider outside the providers",
			spec:    obj{"ai": obj{"provider": "gemini"}},
			wantErr: `spec.ai.provider: Unsupported value: "gemini"`,
		},
		{
			name:    "ai without a provider",
			spec:    obj{"ai": obj{"model": "llama3.1"}},
			wantErr: "spec.ai.provider: Required value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := &unstructured.Unstructured{Object: obj{"spec": tc.spec}}
			policy.SetGroupVersionKind(kubeviltrumitev1alpha1.GroupVersion.WithKind("CompatibilityPolicy"))
			policy.SetGenerateName("policy-")
			policy.SetNamespace(ns)

			err := c.Create(context.Background(), policy)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("rejected: %v", err)
			case tc.wantErr != "" && (!apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want Invalid with %q", err, tc.wantErr)
			}
		})
	}
}

// TestEnvtestCompatibilityPolicyDefaults creates a policy through the Go
// client with only the fields it needs, as a Go caller would, and checks the
// defaults the CRD fills in.
func TestEnvtestCompatibilityPolicyDefaults(t *testing.T) {
	c := envtestClient(t)
	ctx := context.Background()
	policy := &kubeviltrumitev1alpha1.CompatibilityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: envtestNamespace(t, c)},
		Spec: kubeviltrumitev1alpha1.CompatibilityPolicySpec{
			WatchNamespaces: []string{"cert-manager"},
			Autoplan:        &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true},
		},
	}
	if err := c.Create(ctx, policy); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got kubeviltrumitev1alpha1.CompatibilityPolicy
	if err := c.Get(ctx, client.ObjectKeyFromObject(policy), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Autoplan == nil || got.Spec.Autoplan.MaxRisk != ai.RiskLow {
		t.Errorf("autoplan = %+v, want maxRisk defaulted to LOW", got.Spec.Autoplan)
	}
	// No CRD default: the reconciler reads these unset as HIGH and 5m.
	if got.Spec.RiskTolerance != "" || got.Spec.ScanInterval.Duration != 0 {
		t.Errorf("riskTolerance %q, scanInterval %s; want both unset", got.Spec.RiskTolerance, got.Spec.ScanInterval.Duration)
	}
}

// TestEnvtestStatusSubresource checks that both CRDs serve status as a
// subresource, which the reconcilers rely on and the fake client only
// emulates: an update leaves the status alone, a status update leaves the spec
// and the generation alone, and a status sent with a create is dropped, which
// is why autoplan approves a plan with a status update after creating it.
func TestEnvtestStatusSubresource(t *testing.T) {
	c := envtestClient(t)
	ctx := context.Background()
	ns := envtestNamespace(t, c)

	t.Run("CompatibilityPolicy", func(t *testing.T) {
		policy := &kubeviltrumitev1alpha1.CompatibilityPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "status", Namespace: ns},
			Spec:       kubeviltrumitev1alpha1.CompatibilityPolicySpec{RiskTolerance: ai.RiskLow},
		}
		if err := c.Create(ctx, policy); err != nil {
			t.Fatalf("Create: %v", err)
		}
		key := client.ObjectKeyFromObject(policy)

		policy.Status.Mode = "discovery"
		policy.Spec.RiskTolerance = ai.RiskHigh
		if err := c.Status().Update(ctx, policy); err != nil {
			t.Fatalf("status update: %v", err)
		}
		var got kubeviltrumitev1alpha1.CompatibilityPolicy
		if err := c.Get(ctx, key, &got); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status.Mode != "discovery" || got.Spec.RiskTolerance != ai.RiskLow || got.Generation != 1 {
			t.Errorf("after a status update: mode %q, riskTolerance %q, generation %d; want discovery, LOW, 1",
				got.Status.Mode, got.Spec.RiskTolerance, got.Generation)
		}

		got.Status.Mode = "focused"
		got.Spec.RiskTolerance = ai.RiskMedium
		if err := c.Update(ctx, &got); err != nil {
			t.Fatalf("update: %v", err)
		}
		var updated kubeviltrumitev1alpha1.CompatibilityPolicy
		if err := c.Get(ctx, key, &updated); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if updated.Status.Mode != "discovery" || updated.Spec.RiskTolerance != ai.RiskMedium || updated.Generation != 2 {
			t.Errorf("after an update: mode %q, riskTolerance %q, generation %d; want discovery, MEDIUM, 2",
				updated.Status.Mode, updated.Spec.RiskTolerance, updated.Generation)
		}
	})

	t.Run("StackUpgrade", func(t *testing.T) {
		plan := &kubeviltrumitev1alpha1.StackUpgrade{
			ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: ns},
			Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
				{Name: "cert-manager", CurrentVersion: "v1.12.0", TargetVersion: "1.14.0", Risk: ai.RiskLow},
			}},
			Status: kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseApproved},
		}
		if err := c.Create(ctx, plan); err != nil {
			t.Fatalf("Create: %v", err)
		}
		key := client.ObjectKeyFromObject(plan)
		var got kubeviltrumitev1alpha1.StackUpgrade
		if err := c.Get(ctx, key, &got); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status.Phase != "" {
			t.Errorf("phase %q after create, want the create's status dropped", got.Status.Phase)
		}

		got.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseApproved
		if err := c.Status().Update(ctx, &got); err != nil {
			t.Fatalf("status update: %v", err)
		}
		if err := c.Get(ctx, key, &got); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseApproved {
			t.Errorf("phase %q after a status update, want Approved", got.Status.Phase)
		}
	})
}

// TestEnvtestPolicyReconcile runs the reconciler against the real API server.
// The scan reads a real Deployment and finds the Flux and Argo CD kinds not
// served, which is no error; the status goes through the real subresource;
// the plan autoplan creates passes the StackUpgrade schema. A second scan
// changes nothing.
func TestEnvtestPolicyReconcile(t *testing.T) {
	c := envtestClient(t)
	ns := envtestNamespace(t, c)
	if err := c.Create(context.Background(), rawDeployment("cert-manager", ns, "quay.io/jetstack/cert-manager-controller:v1.12.0")); err != nil {
		t.Fatalf("create Deployment: %v", err)
	}
	policy := &kubeviltrumitev1alpha1.CompatibilityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "platform", Namespace: ns},
		Spec: kubeviltrumitev1alpha1.CompatibilityPolicySpec{
			WatchNamespaces: []string{ns},
			TrackedTools:    []string{"cert-manager", "vault"},
			RiskTolerance:   ai.RiskLow,
			// maxRisk comes from the CRD default, LOW.
			Autoplan: &kubeviltrumitev1alpha1.AutoplanConfig{Enabled: true, AutoApprove: true},
		},
	}
	if err := c.Create(context.Background(), policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	r := policyReconcilerFor(t, c, nil)

	wantTools := []kubeviltrumitev1alpha1.TrackedToolStatus{
		{Name: "cert-manager", Installed: true, InstalledVersion: "v1.12.0", Source: "raw", Namespace: ns,
			UpgradeAvailable: true, RecommendedVersion: "1.14.0", Risk: ai.RiskLow, Message: "upgrade available"},
		{Name: "vault", Message: "not currently installed in cluster"},
	}
	wantPlan := []kubeviltrumitev1alpha1.ToolUpgradeSpec{
		{Name: "cert-manager", CurrentVersion: "v1.12.0", TargetVersion: "1.14.0", Risk: ai.RiskLow},
	}
	var planVersion string
	for scan := 1; scan <= 2; scan++ {
		// Errors the scanners log, such as a failed Flux or Argo CD list.
		core, logged := observer.New(zapcore.ErrorLevel)
		ctx := ctrllog.IntoContext(context.Background(), zapr.NewLogger(zap.New(core)))
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(policy)})
		if err != nil {
			t.Fatalf("scan %d: Reconcile: %v", scan, err)
		}
		for _, e := range logged.All() {
			t.Errorf("scan %d logged an error: %s %v", scan, e.Message, e.ContextMap())
		}
		if res != (ctrl.Result{RequeueAfter: 5 * time.Minute}) {
			t.Errorf("scan %d: result = %+v, want RequeueAfter 5m", scan, res)
		}

		var got kubeviltrumitev1alpha1.CompatibilityPolicy
		if err := c.Get(ctx, client.ObjectKeyFromObject(policy), &got); err != nil {
			t.Fatalf("Get policy: %v", err)
		}
		if got.Status.Mode != "focused" || got.Status.LastScanTime == nil || !reflect.DeepEqual(got.Status.Tools, wantTools) {
			t.Errorf("scan %d: status %+v, want focused, scanned, with tools %+v", scan, got.Status, wantTools)
		}

		plans := listUpgrades(t, c, client.InNamespace(ns))
		if len(plans) != 1 || plans[0].Name != "auto-cert-manager-1.14.0" {
			t.Fatalf("scan %d: plans %+v, want auto-cert-manager-1.14.0 only", scan, plans)
		}
		plan := plans[0]
		if !reflect.DeepEqual(plan.Spec.Tools, wantPlan) || plan.Spec.ApprovalRequired ||
			plan.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseApproved {
			t.Errorf("scan %d: plan spec %+v, phase %q; want %+v, no approval required, Approved",
				scan, plan.Spec, plan.Status.Phase, wantPlan)
		}
		if scan == 1 {
			planVersion = plan.ResourceVersion
		} else if plan.ResourceVersion != planVersion {
			t.Errorf("the second scan changed the plan: resourceVersion %s -> %s", planVersion, plan.ResourceVersion)
		}
	}
}
