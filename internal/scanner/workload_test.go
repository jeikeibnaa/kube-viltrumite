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
				Image:         "jetstack/cert-manager-controller",
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
			{Name: "cert-manager", NamespaceHint: "cert-manager", Container: "cert-manager-controller", Image: "jetstack/cert-manager-controller", VersionFrom: "image_tag"},
			{Name: "cert-manager-webhook", NamespaceHint: "cert-manager", Container: "cert-manager-webhook", Image: "jetstack/cert-manager-webhook", VersionFrom: "image_tag"},
			{Name: "cert-manager-cainjector", NamespaceHint: "cert-manager", Container: "cert-manager-cainjector", Image: "jetstack/cert-manager-cainjector", VersionFrom: "image_tag"},
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

func makeTestStatefulSet(name, namespace, container, image string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: container, Image: image}}},
			},
		},
	}
}

func vaultDet() ToolDetection {
	return ToolDetection{
		ToolName: "vault",
		Deployments: []DeploymentMatch{
			{Name: "vault", Container: "vault", Image: "hashicorp/vault", VersionFrom: "image_tag"},
			{Image: "hashicorp/vault-enterprise", VersionFrom: "image_tag"},
		},
	}
}

func argoDet() ToolDetection {
	return ToolDetection{
		ToolName:    "argo-cd",
		Deployments: []DeploymentMatch{{Name: "argocd-server", Container: "argocd-server", Image: "argoproj/argocd", VersionFrom: "image_tag"}},
	}
}

func TestScanWorkloads_Detection(t *testing.T) {
	tests := []struct {
		name        string
		objs        []client.Object
		det         ToolDetection
		wantFound   bool
		wantVersion string
	}{
		{
			name:        "Vault server is a StatefulSet",
			objs:        []client.Object{makeTestStatefulSet("vault", "vault", "vault", "hashicorp/vault:1.16.1")},
			det:         vaultDet(),
			wantFound:   true,
			wantVersion: "1.16.1",
		},
		{
			name:        "enterprise image and tag suffix",
			objs:        []client.Object{makeTestStatefulSet("vault-ent", "vault", "vault", "registry.local:5000/hashicorp/vault-enterprise:1.15.2-ent")},
			det:         vaultDet(),
			wantFound:   true,
			wantVersion: "1.15.2",
		},
		{
			name:        "the injector's vault-k8s image is not Vault",
			objs:        []client.Object{makeTestDeployment("vault-agent-injector", "vault", "hashicorp/vault-k8s:1.4.1")},
			det:         vaultDet(),
			wantFound:   false,
			wantVersion: "",
		},
		{
			name:        "argocd-image-updater is not Argo CD",
			objs:        []client.Object{makeTestDeployment("image-updater", "argocd", "quay.io/argoproj/argocd-image-updater:v0.12.2")},
			det:         argoDet(),
			wantFound:   false,
			wantVersion: "",
		},
		{
			name:        "digest after the tag",
			objs:        []client.Object{makeTestDeployment("server", "argocd", "quay.io/argoproj/argocd:v2.8.0@sha256:0123456789abcdef")},
			det:         argoDet(),
			wantFound:   true,
			wantVersion: "v2.8.0",
		},
		{
			name:        "name-only match reads the named container",
			objs:        []client.Object{makeTestStatefulSet("vault", "vault", "vault", "registry.local/security/vault-fips:1.16.1")},
			det:         vaultDet(),
			wantFound:   true,
			wantVersion: "1.16.1",
		},
		{
			name: "a match with a version beats a name-only match without one",
			objs: []client.Object{
				makeTestDeployment("argocd-server", "argocd", "registry.local/custom/server:latest-build"),
				makeTestStatefulSet("argocd-application-controller", "argocd", "argocd-application-controller", "quay.io/argoproj/argocd:v2.12.0"),
			},
			det:         argoDet(),
			wantFound:   true,
			wantVersion: "v2.12.0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(workloadScheme()).WithObjects(tc.objs...).Build()
			tools, err := (&WorkloadScanner{Client: c}).ScanWorkloads(context.Background(), []string{"vault", "argocd"}, []ToolDetection{tc.det})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found := len(tools) == 1; found != tc.wantFound {
				t.Fatalf("got %+v, want found=%v", tools, tc.wantFound)
			}
			if tc.wantFound && tools[0].CurrentVersion != tc.wantVersion {
				t.Errorf("CurrentVersion = %q, want %q", tools[0].CurrentVersion, tc.wantVersion)
			}
		})
	}
}

func TestSplitImage(t *testing.T) {
	tests := []struct{ image, repo, tag string }{
		{"quay.io/jetstack/cert-manager-controller:v1.14.0", "quay.io/jetstack/cert-manager-controller", "v1.14.0"},
		{"istio/pilot:1.22.0-distroless", "istio/pilot", "1.22.0-distroless"},
		{"registry.local:5000/hashicorp/vault", "registry.local:5000/hashicorp/vault", ""},
		{"registry.local:5000/hashicorp/vault:1.16.1", "registry.local:5000/hashicorp/vault", "1.16.1"},
		{"quay.io/argoproj/argocd:v2.8.0@sha256:abc", "quay.io/argoproj/argocd", "v2.8.0"},
		{"quay.io/argoproj/argocd@sha256:abc", "quay.io/argoproj/argocd", ""},
	}
	for _, tc := range tests {
		t.Run(tc.image, func(t *testing.T) {
			if repo, tag := splitImage(tc.image); repo != tc.repo || tag != tc.tag {
				t.Errorf("splitImage(%q) = (%q, %q), want (%q, %q)", tc.image, repo, tag, tc.repo, tc.tag)
			}
		})
	}
}

func TestImageMatches(t *testing.T) {
	tests := []struct {
		image, want string
		match       bool
	}{
		{"docker.io/istio/pilot:1.22.0", "istio/pilot", true},
		{"istio/pilot:1.22.0", "istio/pilot", true},
		{"registry.local/mirror/docker.io/istio/pilot:1.22.0", "istio/pilot", true},
		{"docker.io/istio/proxyv2:1.22.0", "istio/pilot", false},
		{"quay.io/argoproj/argocd-image-updater:v0.12.2", "argoproj/argocd", false},
		{"example.com/notistio/pilot:1.0.0", "istio/pilot", false},
		{"istio/pilot:1.22.0", "", false},
	}
	for _, tc := range tests {
		if got := imageMatches(tc.image, tc.want); got != tc.match {
			t.Errorf("imageMatches(%q, %q) = %v, want %v", tc.image, tc.want, got, tc.match)
		}
	}
}

func TestCleanVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.14.0", "v1.14.0"},
		{"1.14.0-debian-12-r3", "1.14.0"},
		{"1.16.1-ent", "1.16.1"},
		{"1.22.0-distroless", "1.22.0"},
		{"v0.63.0", "v0.63.0"},
		{"v1.15.0-rc.1", "v1.15.0-rc.1"},
		{"v2.9.0-beta1-alpine", "v2.9.0-beta1"},
		{"1.14", "1.14"},
		{" 1.2.3 ", "1.2.3"},
		{"latest", "latest"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := CleanVersion(tc.in); got != tc.want {
			t.Errorf("CleanVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVersionFromImages(t *testing.T) {
	images := []string{"redis:7.0.11-alpine", "ghcr.io/dexidp/dex:v2.38.0", "quay.io/argoproj/argocd:v2.12.0"}
	if got := VersionFromImages(images, argoDet()); got != "v2.12.0" {
		t.Errorf("VersionFromImages = %q, want v2.12.0", got)
	}
	if got := VersionFromImages(images, vaultDet()); got != "" {
		t.Errorf("VersionFromImages with no matching image = %q, want empty", got)
	}
	if got := VersionFromImages(nil, argoDet()); got != "" {
		t.Errorf("VersionFromImages(nil) = %q, want empty", got)
	}
}
