package scanner

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func makeHelmRelease(name, namespace, chartName, version string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "helm.toolkit.fluxcd.io",
		Version: "v2beta1",
		Kind:    "HelmRelease",
	})
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = unstructured.SetNestedField(obj.Object, chartName, "spec", "chart", "spec", "chart")
	_ = unstructured.SetNestedField(obj.Object, version, "spec", "chart", "spec", "version")
	return obj
}

func TestScanHelmReleases_ReturnsTwoTools(t *testing.T) {
	hr1 := makeHelmRelease("cert-manager", "default", "cert-manager", "1.12.0")
	hr2 := makeHelmRelease("prometheus", "monitoring", "kube-prometheus-stack", "45.0.0")

	c := fake.NewClientBuilder().
		WithScheme(runtime.NewScheme()).
		WithObjects(hr1, hr2).
		Build()

	s := &ClusterScanner{Client: c}
	tools, err := s.ScanHelmReleases(context.Background(), []string{"default", "monitoring"})
	if err != nil {
		t.Fatalf("ScanHelmReleases: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(tools), tools)
	}

	byName := make(map[string]InstalledTool)
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	cm, ok := byName["cert-manager"]
	if !ok {
		t.Fatal("expected cert-manager in results")
	}
	if cm.ChartName != "cert-manager" {
		t.Errorf("ChartName: got %q, want cert-manager", cm.ChartName)
	}
	if cm.CurrentVersion != "1.12.0" {
		t.Errorf("CurrentVersion: got %q, want 1.12.0", cm.CurrentVersion)
	}
	if cm.Namespace != "default" {
		t.Errorf("Namespace: got %q, want default", cm.Namespace)
	}
	if cm.Source != "fluxcd" {
		t.Errorf("Source: got %q, want fluxcd", cm.Source)
	}
	if cm.ReleaseName != "cert-manager" {
		t.Errorf("ReleaseName: got %q, want cert-manager", cm.ReleaseName)
	}

	prom, ok := byName["prometheus"]
	if !ok {
		t.Fatal("expected prometheus in results")
	}
	if prom.ChartName != "kube-prometheus-stack" {
		t.Errorf("ChartName: got %q, want kube-prometheus-stack", prom.ChartName)
	}
	if prom.CurrentVersion != "45.0.0" {
		t.Errorf("CurrentVersion: got %q, want 45.0.0", prom.CurrentVersion)
	}
	if prom.Namespace != "monitoring" {
		t.Errorf("Namespace: got %q, want monitoring", prom.Namespace)
	}
	if prom.Source != "fluxcd" {
		t.Errorf("Source: got %q, want fluxcd", prom.Source)
	}
}
