package scanner

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeploymentMatch holds the matching criteria for a single workload signature.
type DeploymentMatch struct {
	Name          string
	NamespaceHint string
	Container     string
	ImageContains string
	VersionFrom   string
}

// ToolDetection describes how to identify a tool from raw workload resources.
type ToolDetection struct {
	ToolName    string
	Deployments []DeploymentMatch
	Labels      map[string]string
}

// WorkloadScanner detects tools installed via raw kubectl apply (no Helm release).
type WorkloadScanner struct {
	Client client.Client
}

// ScanWorkloads lists Deployments and DaemonSets across namespaces and returns
// one InstalledTool per matched tool, deduplicated by ToolName.
func (s *WorkloadScanner) ScanWorkloads(ctx context.Context, namespaces []string, detections []ToolDetection) ([]InstalledTool, error) {
	seen := make(map[string]InstalledTool)

	for _, ns := range namespaces {
		if err := s.matchDeployments(ctx, ns, detections, seen); err != nil {
			return nil, err
		}
		if err := s.matchDaemonSets(ctx, ns, detections, seen); err != nil {
			return nil, err
		}
	}

	tools := make([]InstalledTool, 0, len(seen))
	for _, t := range seen {
		tools = append(tools, t)
	}
	return tools, nil
}

func (s *WorkloadScanner) matchDeployments(ctx context.Context, ns string, detections []ToolDetection, seen map[string]InstalledTool) error {
	list := &appsv1.DeploymentList{}
	if err := s.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("scanner: list Deployments in %s: %w", ns, err)
	}
	for i := range list.Items {
		d := &list.Items[i]
		applyDetections(d.Name, d.Namespace, d.Labels, d.Spec.Template.Spec.Containers, detections, seen)
	}
	return nil
}

func (s *WorkloadScanner) matchDaemonSets(ctx context.Context, ns string, detections []ToolDetection, seen map[string]InstalledTool) error {
	list := &appsv1.DaemonSetList{}
	if err := s.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("scanner: list DaemonSets in %s: %w", ns, err)
	}
	for i := range list.Items {
		d := &list.Items[i]
		applyDetections(d.Name, d.Namespace, d.Labels, d.Spec.Template.Spec.Containers, detections, seen)
	}
	return nil
}

// applyDetections checks a workload against every detection rule and records the
// first matching DeploymentMatch for each unmatched tool.
func applyDetections(name, namespace string, labels map[string]string, containers []corev1.Container, detections []ToolDetection, seen map[string]InstalledTool) {
	for _, det := range detections {
		if _, ok := seen[det.ToolName]; ok {
			continue
		}
		for _, dm := range det.Deployments {
			if !matchesWorkload(name, containers, dm) {
				continue
			}
			seen[det.ToolName] = InstalledTool{
				Name:           det.ToolName,
				CurrentVersion: extractVersion(labels, containers, dm),
				Namespace:      namespace,
				Source:         "raw",
			}
			break
		}
	}
}

// matchesWorkload returns true if the workload name equals dm.Name or any
// container image contains dm.ImageContains.
func matchesWorkload(name string, containers []corev1.Container, dm DeploymentMatch) bool {
	if name == dm.Name {
		return true
	}
	if dm.ImageContains != "" {
		for _, c := range containers {
			if strings.Contains(c.Image, dm.ImageContains) {
				return true
			}
		}
	}
	return false
}

// extractVersion pulls the version string from a workload based on dm.VersionFrom.
//
// image_tag: substring after the last ":" in the matched container's image.
// label:     value of the "app.kubernetes.io/version" label on the workload.
func extractVersion(labels map[string]string, containers []corev1.Container, dm DeploymentMatch) string {
	switch dm.VersionFrom {
	case "image_tag":
		for _, c := range containers {
			if dm.ImageContains == "" || strings.Contains(c.Image, dm.ImageContains) {
				if idx := strings.LastIndex(c.Image, ":"); idx != -1 {
					return c.Image[idx+1:]
				}
			}
		}
	case "label":
		return labels["app.kubernetes.io/version"]
	}
	return ""
}
