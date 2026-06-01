package scanner

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func workloadScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = appsv1.AddToScheme(s)
	return s
}

func makeTestDeployment(name, namespace, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: image}}},
			},
		},
	}
}

// certManagerDet matches only the controller workload (single DeploymentMatch).
func certManagerDet() ToolDetection {
	return ToolDetection{
		ToolName: "cert-manager",
		Deployments: []DeploymentMatch{
			{
				Name:          "cert-manager",
				NamespaceHint: "cert-manager",
				Container:     "cert-manager-controller",
				ImageContains: "cert-manager-controller",
				VersionFrom:   "image_tag",
			},
		},
		Labels: map[string]string{"app.kubernetes.io/name": "cert-manager"},
	}
}

// certManagerDetFull matches all three cert-manager workloads so the dedup test
// has three real matches to collapse into one InstalledTool.
func certManagerDetFull() ToolDetection {
	return ToolDetection{
		ToolName: "cert-manager",
		Deployments: []DeploymentMatch{
			{Name: "cert-manager", NamespaceHint: "cert-manager", Container: "cert-manager-controller", ImageContains: "cert-manager-controller", VersionFrom: "image_tag"},
			{Name: "cert-manager-webhook", NamespaceHint: "cert-manager", Container: "cert-manager-webhook", ImageContains: "cert-manager-webhook", VersionFrom: "image_tag"},
			{Name: "cert-manager-cainjector", NamespaceHint: "cert-manager", Container: "cert-manager-cainjector", ImageContains: "cert-manager-cainjector", VersionFrom: "image_tag"},
		},
		Labels: map[string]string{"app.kubernetes.io/name": "cert-manager"},
	}
}

func TestScanWorkloads(t *testing.T) {
	// Test 1: one cert-manager Deployment -> 1 InstalledTool with correct fields.
	t.Run("single cert-manager Deployment", func(t *testing.T) {
		dep := makeTestDeployment("cert-manager", "cert-manager",
			"quay.io/jetstack/cert-manager-controller:v1.14.0")
		c := fake.NewClientBuilder().
			WithScheme(workloadScheme()).
			WithObjects(dep).
			Build()

		s := &WorkloadScanner{Client: c}
		tools, err := s.ScanWorkloads(context.Background(), []string{"cert-manager"}, []ToolDetection{certManagerDet()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("got %d tools, want 1: %v", len(tools), tools)
		}
		got := tools[0]
		if got.Name != "cert-manager" {
			t.Errorf("Name = %q, want cert-manager", got.Name)
		}
		if got.CurrentVersion != "v1.14.0" {
			t.Errorf("CurrentVersion = %q, want v1.14.0", got.CurrentVersion)
		}
		if got.Source != "raw" {
			t.Errorf("Source = %q, want raw", got.Source)
		}
		if got.Namespace != "cert-manager" {
			t.Errorf("Namespace = %q, want cert-manager", got.Namespace)
		}
		if got.ReleaseName != "" {
			t.Errorf("ReleaseName = %q, want empty", got.ReleaseName)
		}
	})

	// Test 2: controller + webhook + cainjector all match -> exactly 1 InstalledTool.
	t.Run("three cert-manager workloads deduplicate to one", func(t *testing.T) {
		objs := []client.Object{
			makeTestDeployment("cert-manager", "cert-manager",
				"quay.io/jetstack/cert-manager-controller:v1.14.0"),
			makeTestDeployment("cert-manager-webhook", "cert-manager",
				"quay.io/jetstack/cert-manager-webhook:v1.14.0"),
			makeTestDeployment("cert-manager-cainjector", "cert-manager",
				"quay.io/jetstack/cert-manager-cainjector:v1.14.0"),
		}
		c := fake.NewClientBuilder().
			WithScheme(workloadScheme()).
			WithObjects(objs...).
			Build()

		s := &WorkloadScanner{Client: c}
		tools, err := s.ScanWorkloads(context.Background(), []string{"cert-manager"}, []ToolDetection{certManagerDetFull()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("got %d tools, want 1 (dedup): %v", len(tools), tools)
		}
		if tools[0].Name != "cert-manager" {
			t.Errorf("Name = %q, want cert-manager", tools[0].Name)
		}
		if tools[0].Source != "raw" {
			t.Errorf("Source = %q, want raw", tools[0].Source)
		}
	})

	// Test 3: unrelated nginx deployment -> detection rules don't match -> empty result.
	t.Run("unrelated nginx Deployment returns empty", func(t *testing.T) {
		dep := makeTestDeployment("nginx", "default", "nginx:1.25.0")
		c := fake.NewClientBuilder().
			WithScheme(workloadScheme()).
			WithObjects(dep).
			Build()

		s := &WorkloadScanner{Client: c}
		tools, err := s.ScanWorkloads(context.Background(), []string{"default"}, []ToolDetection{certManagerDet()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tools) != 0 {
			t.Errorf("got %d tools, want 0: %v", len(tools), tools)
		}
	})

	// Test 4: empty namespace slice -> no List calls, no panic, empty result.
	t.Run("empty namespaces returns empty without panic", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(workloadScheme()).
			Build()

		s := &WorkloadScanner{Client: c}
		tools, err := s.ScanWorkloads(context.Background(), []string{}, []ToolDetection{certManagerDet()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tools) != 0 {
			t.Errorf("got %d tools, want 0: %v", len(tools), tools)
		}
	})
}
