package scanner

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InstalledTool describes a Helm-managed tool found in the cluster.
type InstalledTool struct {
	Name           string
	ChartName      string
	CurrentVersion string
	Namespace      string
	ReleaseName    string
	Source         string // helm | fluxcd
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

// ScanHelmReleases lists FluxCD HelmRelease objects across the given namespaces.
// If the HelmRelease CRD is not installed the function returns an empty list without error.
func (s *ClusterScanner) ScanHelmReleases(ctx context.Context, namespaces []string) ([]InstalledTool, error) {
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
			tools = append(tools, InstalledTool{
				Name:           item.GetName(),
				ChartName:      chartName,
				CurrentVersion: version,
				Namespace:      item.GetNamespace(),
				ReleaseName:    item.GetName(),
				Source:         "fluxcd",
			})
		}
	}

	return tools, nil
}
