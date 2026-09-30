package controller

import (
	"slices"
	"testing"

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
