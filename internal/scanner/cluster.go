package scanner

import (
	"context"
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// InstalledTool describes a tool found in the cluster, as its source reports
// it. Scanners know nothing about the knowledge base: the controller maps Name
// to a knowledge-base tool and fills in a missing app version.
type InstalledTool struct {
	// Name is the name the source reports: the Helm chart name, or the tool
	// name for a raw install (detection rules are keyed by tool).
	Name      string
	ChartName string
	// ChartVersion is the deployed Helm chart version, when there is a chart.
	ChartVersion string
	// CurrentVersion is the app version, cleaned with CleanVersion. Empty
	// when the source does not report one (an Argo CD Application, a Flux
	// v2beta1 HelmRelease with no matching Helm release).
	CurrentVersion string
	// Namespace is the namespace the tool runs in: the Helm release
	// namespace, or an Argo CD Application's destination namespace.
	Namespace   string
	ReleaseName string
	Source      string // helm | fluxcd | argocd | raw
	RepoURL     string
	// ChartOrigins is evidence of where the chart comes from: its home and
	// sources (Helm), the repoURL (Argo CD) or the source name (Flux).
	ChartOrigins []string
	// Images are the container images the source reports the tool running
	// (Argo CD's status.summary.images).
	Images []string
}

// HelmReleaseLister lists the Helm releases stored in a namespace.
type HelmReleaseLister func(ctx context.Context, namespace string) ([]*release.Release, error)

// ClusterScanner discovers installed tools in a Kubernetes cluster.
type ClusterScanner struct {
	Client client.Client
	// ListHelmReleases lists plain Helm releases. Nil means the Helm SDK
	// with the operator's own credentials; tests pass a fake so they never
	// reach the cluster in the current kube-context.
	ListHelmReleases HelmReleaseLister
}

// listHelmReleasesSDK lists the deployed and failed releases whose Helm
// storage (Secrets) is in namespace.
func listHelmReleasesSDK(_ context.Context, namespace string) ([]*release.Release, error) {
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(
		genericclioptions.NewConfigFlags(true),
		namespace,
		"secret",
		func(format string, v ...interface{}) {},
	); err != nil {
		return nil, err
	}
	return action.NewList(actionConfig).Run()
}

// helmReleaseGK is Flux's HelmRelease. fluxHelmReleaseVersions are the API
// versions to try, newest first: current Flux serves v2 and has removed
// v2beta1, while Flux 2.0–2.2 serve only the betas.
var (
	helmReleaseGK           = schema.GroupKind{Group: "helm.toolkit.fluxcd.io", Kind: "HelmRelease"}
	fluxHelmReleaseVersions = []string{"v2", "v2beta2", "v2beta1"}
)

var argoCDAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// ScanAll calls all three scanners and merges results.
// Individual scanner errors are logged and skipped so one unavailable source
// never prevents the others from running.
//
// Flux installs charts through Helm, so a release a Flux HelmRelease manages
// is reported once, as fluxcd. What Flux does not report (the app version on
// v2beta1, the chart's home and sources) comes from that Helm release.
func (s *ClusterScanner) ScanAll(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	logger := log.FromContext(ctx)

	fluxTools, err := s.scanFluxHelmReleases(ctx, namespaces)
	if err != nil {
		logger.Error(err, "flux scanner failed")
		fluxTools = nil
	}

	helmTools, err := s.scanPlainHelmReleases(ctx, namespaces)
	if err != nil {
		logger.Error(err, "helm scanner failed")
		helmTools = nil
	}

	argoTools, err := s.scanArgoCDApplications(ctx, namespaces)
	if err != nil {
		logger.Error(err, "argocd scanner failed")
		argoTools = nil
	}

	helmTools = mergeFluxManagedReleases(fluxTools, helmTools)
	return append(append(fluxTools, helmTools...), argoTools...), nil
}

// mergeFluxManagedReleases completes each Flux tool from the Helm release it
// manages (same namespace and release name) and returns the Helm releases
// no Flux HelmRelease manages.
func mergeFluxManagedReleases(fluxTools, helmTools []InstalledTool) []InstalledTool {
	byRelease := make(map[string]int, len(helmTools))
	for i, h := range helmTools {
		byRelease[h.Namespace+"/"+h.ReleaseName] = i
	}

	managed := make(map[int]bool)
	for i := range fluxTools {
		f := &fluxTools[i]
		j, ok := byRelease[f.Namespace+"/"+f.ReleaseName]
		if !ok {
			continue
		}
		managed[j] = true
		h := helmTools[j]
		if f.ChartName == "" {
			f.ChartName, f.Name = h.ChartName, h.Name
		}
		if f.ChartVersion == "" {
			f.ChartVersion = h.ChartVersion
		}
		if f.CurrentVersion == "" {
			f.CurrentVersion = h.CurrentVersion
		}
		f.ChartOrigins = append(f.ChartOrigins, h.ChartOrigins...)
	}

	unmanaged := make([]InstalledTool, 0, len(helmTools)-len(managed))
	for i, h := range helmTools {
		if !managed[i] {
			unmanaged = append(unmanaged, h)
		}
	}
	return unmanaged
}

// scanFluxHelmReleases lists FluxCD HelmReleases with the newest API version
// the cluster serves and keeps those that install into, or live in, one of
// namespaces. HelmReleases usually live in flux-system while the tool runs in
// its own namespace, so a policy watching only the tool's namespace still sees
// that Flux manages it. Returns an empty list without error when Flux is not
// installed.
func (s *ClusterScanner) scanFluxHelmReleases(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	for _, version := range fluxHelmReleaseVersions {
		tools, err := s.listFluxHelmReleases(ctx, namespaces, helmReleaseGK.WithVersion(version))
		if apimeta.IsNoMatchError(err) {
			continue
		}
		return tools, err
	}
	return nil, nil
}

func (s *ClusterScanner) listFluxHelmReleases(ctx context.Context, namespaces []string, gvk schema.GroupVersionKind) ([]InstalledTool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	if err := s.Client.List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil, err
		}
		return nil, fmt.Errorf("scanner: list HelmReleases (%s): %w", gvk.Version, err)
	}

	watched := toSet(namespaces)
	var tools []InstalledTool
	for i := range list.Items {
		hr := &list.Items[i]
		if t := fluxTool(hr); watched[t.Namespace] || watched[hr.GetNamespace()] {
			tools = append(tools, t)
		}
	}
	return tools, nil
}

// fluxTool reads one HelmRelease. Its release history (v2, v2beta2) records
// what was really deployed; the spec is the fallback, because its chart
// version may be a range.
func fluxTool(hr *unstructured.Unstructured) InstalledTool {
	obj := hr.Object
	chart := nestedString(obj, "spec", "chart", "spec", "chart")
	targetNS := nestedString(obj, "spec", "targetNamespace")

	t := InstalledTool{
		Name:         chart,
		ChartName:    chart,
		ChartVersion: exactVersion(nestedString(obj, "spec", "chart", "spec", "version")),
		Namespace:    hr.GetNamespace(),
		ReleaseName:  nestedString(obj, "spec", "releaseName"),
		Source:       "fluxcd",
	}
	if targetNS != "" {
		t.Namespace = targetNS
	}
	if t.ReleaseName == "" {
		// Flux's default release name.
		t.ReleaseName = hr.GetName()
		if targetNS != "" {
			t.ReleaseName = targetNS + "-" + hr.GetName()
		}
	}
	for _, ref := range [][]string{
		{"spec", "chart", "spec", "sourceRef", "name"},
		{"spec", "chartRef", "name"},
	} {
		if name := nestedString(obj, ref...); name != "" {
			t.ChartOrigins = append(t.ChartOrigins, name)
		}
	}

	if snap := latestDeployedSnapshot(obj); snap != nil {
		if v := stringField(snap, "chartName"); v != "" {
			t.Name, t.ChartName = v, v
		}
		if v := stringField(snap, "chartVersion"); v != "" {
			t.ChartVersion = v
		}
		t.CurrentVersion = CleanVersion(stringField(snap, "appVersion"))
		if v := stringField(snap, "name"); v != "" {
			t.ReleaseName = v
		}
		if v := stringField(snap, "namespace"); v != "" {
			t.Namespace = v
		}
	} else if v := nestedString(obj, "status", "lastAppliedRevision"); v != "" {
		// v2beta1 records the applied chart version here.
		t.ChartVersion = v
	}
	return t
}

// latestDeployedSnapshot returns the entry of a HelmRelease's
// status.history with the highest release version, preferring one whose
// status is "deployed" (the history also keeps failed attempts). Returns nil
// when there is no history.
func latestDeployedSnapshot(obj map[string]interface{}) map[string]interface{} {
	history, _, _ := unstructured.NestedSlice(obj, "status", "history")
	var best map[string]interface{}
	bestDeployed := false
	var bestVersion int64 = -1
	for _, item := range history {
		snap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		version, _, _ := unstructured.NestedInt64(snap, "version")
		deployed := stringField(snap, "status") == "deployed"
		if best == nil || (deployed && !bestDeployed) || (deployed == bestDeployed && version > bestVersion) {
			best, bestDeployed, bestVersion = snap, deployed, version
		}
	}
	return best
}

// scanPlainHelmReleases lists plain Helm releases via the Helm SDK across the given namespaces.
// Namespaces where access is denied are skipped silently.
func (s *ClusterScanner) scanPlainHelmReleases(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	logger := log.FromContext(ctx)
	list := s.ListHelmReleases
	if list == nil {
		list = listHelmReleasesSDK
	}
	var tools []InstalledTool

	for _, ns := range namespaces {
		releases, err := list(ctx, ns)
		if err != nil {
			logger.Info("helm scanner: skipping namespace", "namespace", ns, "err", err)
			continue
		}

		for _, rel := range releases {
			if rel.Chart == nil || rel.Chart.Metadata == nil {
				continue
			}
			md := rel.Chart.Metadata
			t := InstalledTool{
				Name:           md.Name,
				ChartName:      md.Name,
				ChartVersion:   md.Version,
				CurrentVersion: CleanVersion(md.AppVersion),
				Namespace:      rel.Namespace,
				ReleaseName:    rel.Name,
				Source:         "helm",
			}
			if md.Home != "" {
				t.ChartOrigins = append(t.ChartOrigins, md.Home)
			}
			t.ChartOrigins = append(t.ChartOrigins, md.Sources...)
			tools = append(tools, t)
		}
	}

	return tools, nil
}

// scanArgoCDApplications lists ArgoCD Application objects across all
// namespaces and keeps those that deploy into, or live in, one of namespaces.
// Returns an empty list without error when the Application CRD is not
// installed. Each Helm chart source (spec.source, or each of spec.sources) is
// one tool; Git and plain-manifest sources are skipped.
func (s *ClusterScanner) scanArgoCDApplications(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   argoCDAppGVK.Group,
		Version: argoCDAppGVK.Version,
		Kind:    argoCDAppGVK.Kind + "List",
	})

	if err := s.Client.List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scanner: list ArgoCD Applications: %w", err)
	}

	watched := toSet(namespaces)
	var tools []InstalledTool
	for i := range list.Items {
		app := &list.Items[i]
		for _, t := range argoTools(app) {
			if watched[t.Namespace] || watched[app.GetNamespace()] {
				tools = append(tools, t)
			}
		}
	}
	return tools, nil
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, s := range items {
		set[s] = true
	}
	return set
}

// argoTools reads one Application. Argo CD renders charts itself and keeps no
// Helm release, so it reports only the chart version: the resolved revision
// from status.sync when there is one (targetRevision may be a range), and the
// images the Application runs, which the controller reads the app version
// from.
func argoTools(app *unstructured.Unstructured) []InstalledTool {
	obj := app.Object

	// A multi-source Application uses spec.sources and ignores spec.source;
	// status.sync.revisions lines up with spec.sources.
	var sources []map[string]interface{}
	var revisions []string
	if list, ok, _ := unstructured.NestedSlice(obj, "spec", "sources"); ok && len(list) > 0 {
		for _, item := range list {
			src, _ := item.(map[string]interface{})
			sources = append(sources, src)
		}
		revisions, _, _ = unstructured.NestedStringSlice(obj, "status", "sync", "revisions")
	} else if src, ok, _ := unstructured.NestedMap(obj, "spec", "source"); ok {
		sources = []map[string]interface{}{src}
		revisions = []string{nestedString(obj, "status", "sync", "revision")}
	}

	namespace := nestedString(obj, "spec", "destination", "namespace")
	if namespace == "" {
		namespace = app.GetNamespace()
	}
	images, _, _ := unstructured.NestedStringSlice(obj, "status", "summary", "images")

	var tools []InstalledTool
	for i, src := range sources {
		chart := stringField(src, "chart")
		if chart == "" {
			continue
		}
		version := exactVersion(stringField(src, "targetRevision"))
		if i < len(revisions) && revisions[i] != "" {
			version = revisions[i]
		}
		repoURL := stringField(src, "repoURL")
		release := nestedString(src, "helm", "releaseName")
		if release == "" {
			release = app.GetName()
		}
		tools = append(tools, InstalledTool{
			Name:         chart,
			ChartName:    chart,
			ChartVersion: version,
			Namespace:    namespace,
			ReleaseName:  release,
			Source:       "argocd",
			RepoURL:      repoURL,
			ChartOrigins: []string{repoURL},
			Images:       images,
		})
	}
	return tools
}

// exactVersion returns v when it is a single version, and "" for a range or
// wildcard such as "1.x", ">=1.2.0" or "*" (a Flux or Argo CD spec may hold
// either).
func exactVersion(v string) string {
	if strings.ContainsAny(v, "*xX^~<>=|, ") {
		return ""
	}
	return v
}

// nestedString returns the string at fields in obj, or "".
func nestedString(obj map[string]interface{}, fields ...string) string {
	s, _, _ := unstructured.NestedString(obj, fields...)
	return s
}

// stringField returns obj[key] when it is a string, or "".
func stringField(obj map[string]interface{}, key string) string {
	s, _ := obj[key].(string)
	return s
}
