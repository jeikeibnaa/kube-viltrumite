package scanner

import (
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// InstalledTool describes a Helm-managed tool found in the cluster.
type InstalledTool struct {
	Name           string
	ChartName      string
	CurrentVersion string
	Namespace      string
	ReleaseName    string
	Source         string // helm | fluxcd | argocd
	RepoURL        string
}

// ClusterScanner discovers installed tools in a Kubernetes cluster.
type ClusterScanner struct {
	Client client.Client
}

var helmReleaseGVK = schema.GroupVersionKind{
	Group:   "helm.toolkit.fluxcd.io",
	Version: "v2beta1",
	Kind:    "HelmRelease",
}

var argoCDAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// ScanAll calls all three scanners and merges results.
// Individual scanner errors are logged and skipped so one unavailable source
// never prevents the others from running.
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

	argoTools, err := s.scanArgoCDApplications(ctx)
	if err != nil {
		logger.Error(err, "argocd scanner failed")
		argoTools = nil
	}

	return append(append(fluxTools, helmTools...), argoTools...), nil
}

// scanFluxHelmReleases lists FluxCD HelmRelease objects across the given namespaces.
// Returns an empty list without error when the HelmRelease CRD is not installed.
func (s *ClusterScanner) scanFluxHelmReleases(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	var tools []InstalledTool

	for _, ns := range namespaces {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   helmReleaseGVK.Group,
			Version: helmReleaseGVK.Version,
			Kind:    helmReleaseGVK.Kind + "List",
		})

		if err := s.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
			if apimeta.IsNoMatchError(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("scanner: list HelmReleases in %s: %w", ns, err)
		}

		for _, item := range list.Items {
			chartName, _, _ := unstructured.NestedString(item.Object, "spec", "chart", "spec", "chart")
			version, _, _ := unstructured.NestedString(item.Object, "spec", "chart", "spec", "version")
			repoURL, _, _ := unstructured.NestedString(item.Object, "spec", "chart", "spec", "repo")
			tools = append(tools, InstalledTool{
				Name:           item.GetName(),
				ChartName:      chartName,
				CurrentVersion: version,
				Namespace:      item.GetNamespace(),
				ReleaseName:    item.GetName(),
				Source:         "fluxcd",
				RepoURL:        repoURL,
			})
		}
	}

	return tools, nil
}

// scanPlainHelmReleases lists plain Helm releases via the Helm SDK across the given namespaces.
// Namespaces where access is denied are skipped silently.
func (s *ClusterScanner) scanPlainHelmReleases(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
	logger := log.FromContext(ctx)
	var tools []InstalledTool

	for _, ns := range namespaces {
		actionConfig := new(action.Configuration)
		if err := actionConfig.Init(
			genericclioptions.NewConfigFlags(true),
			ns,
			"secret",
			func(format string, v ...interface{}) {},
		); err != nil {
			logger.Info("helm scanner: skipping namespace", "namespace", ns, "err", err)
			continue
		}

		lister := action.NewList(actionConfig)
		releases, err := lister.Run()
		if err != nil {
			logger.Info("helm scanner: skipping namespace", "namespace", ns, "err", err)
			continue
		}

		for _, rel := range releases {
			if rel.Chart == nil || rel.Chart.Metadata == nil {
				continue
			}
			tools = append(tools, InstalledTool{
				Name:           rel.Chart.Metadata.Name,
				ChartName:      rel.Chart.Metadata.Name,
				CurrentVersion: rel.Chart.Metadata.Version,
				Namespace:      rel.Namespace,
				ReleaseName:    rel.Name,
				Source:         "helm",
			})
		}
	}

	return tools, nil
}

// scanArgoCDApplications lists ArgoCD Application objects across all namespaces.
// Returns an empty list without error when the Application CRD is not installed.
// Only Helm-based Applications (those with a non-empty spec.source.chart) are included.
func (s *ClusterScanner) scanArgoCDApplications(ctx context.Context) ([]InstalledTool, error) {
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

	var tools []InstalledTool
	for _, item := range list.Items {
		chartName, _, _ := unstructured.NestedString(item.Object, "spec", "source", "chart")
		if chartName == "" {
			continue
		}
		targetRevision, _, _ := unstructured.NestedString(item.Object, "spec", "source", "targetRevision")
		repoURL, _, _ := unstructured.NestedString(item.Object, "spec", "source", "repoURL")
		tools = append(tools, InstalledTool{
			Name:           chartName,
			ChartName:      chartName,
			CurrentVersion: targetRevision,
			Namespace:      item.GetNamespace(),
			ReleaseName:    item.GetName(),
			Source:         "argocd",
			RepoURL:        repoURL,
		})
	}

	return tools, nil
}
