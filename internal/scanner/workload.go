package scanner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeploymentMatch holds the matching criteria for a single workload signature.
// A workload matches when its name is Name or one of its containers runs Image.
type DeploymentMatch struct {
	Name          string
	NamespaceHint string
	// Container is the container to read the version from when the workload
	// matched by name but runs no container with Image.
	Container string
	// Image is an image repository matched as a whole path suffix (see
	// imageMatches).
	Image       string
	VersionFrom string
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

// ScanWorkloads lists Deployments, StatefulSets and DaemonSets across
// namespaces and returns one InstalledTool per matched tool, deduplicated by
// ToolName.
func (s *WorkloadScanner) ScanWorkloads(ctx context.Context, namespaces []string, detections []ToolDetection) ([]InstalledTool, error) {
	seen := make(map[string]InstalledTool)

	for _, ns := range namespaces {
		if err := s.matchDeployments(ctx, ns, detections, seen); err != nil {
			return nil, err
		}
		if err := s.matchStatefulSets(ctx, ns, detections, seen); err != nil {
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

// matchStatefulSets covers tools whose server runs as a StatefulSet, such as
// Vault and the Argo CD application controller.
func (s *WorkloadScanner) matchStatefulSets(ctx context.Context, ns string, detections []ToolDetection, seen map[string]InstalledTool) error {
	list := &appsv1.StatefulSetList{}
	if err := s.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("scanner: list StatefulSets in %s: %w", ns, err)
	}
	for i := range list.Items {
		st := &list.Items[i]
		applyDetections(st.Name, st.Namespace, st.Labels, st.Spec.Template.Spec.Containers, detections, seen)
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

// applyDetections checks a workload against every detection rule. A tool
// keeps its first match whose version looks like one; a match without (no tag,
// or a tag such as "latest") is kept until a later workload yields a version.
func applyDetections(name, namespace string, labels map[string]string, containers []corev1.Container, detections []ToolDetection, seen map[string]InstalledTool) {
	for _, det := range detections {
		if prev, ok := seen[det.ToolName]; ok && versionPrefix.MatchString(prev.CurrentVersion) {
			continue
		}
		for _, dm := range det.Deployments {
			if !matchesWorkload(name, containers, dm) {
				continue
			}
			version := extractVersion(labels, containers, dm)
			if _, ok := seen[det.ToolName]; ok && !versionPrefix.MatchString(version) {
				break
			}
			seen[det.ToolName] = InstalledTool{
				Name:           det.ToolName,
				CurrentVersion: version,
				Namespace:      namespace,
				Source:         "raw",
			}
			break
		}
	}
}

// matchesWorkload returns true if the workload name equals dm.Name or any
// container runs dm.Image.
func matchesWorkload(name string, containers []corev1.Container, dm DeploymentMatch) bool {
	if dm.Name != "" && name == dm.Name {
		return true
	}
	for _, c := range containers {
		if imageMatches(c.Image, dm.Image) {
			return true
		}
	}
	return false
}

// extractVersion pulls the version string from a workload based on dm.VersionFrom.
//
// image_tag: the tag of the first container running dm.Image or, failing
// that, of the container named dm.Container, cleaned with CleanVersion.
// label:     value of the "app.kubernetes.io/version" label on the workload.
func extractVersion(labels map[string]string, containers []corev1.Container, dm DeploymentMatch) string {
	switch dm.VersionFrom {
	case "image_tag":
		for _, c := range containers {
			if imageMatches(c.Image, dm.Image) {
				return imageVersion(c.Image)
			}
		}
		for _, c := range containers {
			if dm.Container != "" && c.Name == dm.Container {
				return imageVersion(c.Image)
			}
		}
	case "label":
		return CleanVersion(labels["app.kubernetes.io/version"])
	}
	return ""
}

// VersionFromImages returns the version of the first image one of det's rules
// matches, or "" when none does. It reads images a GitOps controller reports
// for a tool (Argo CD's status.summary.images).
func VersionFromImages(images []string, det ToolDetection) string {
	for _, dm := range det.Deployments {
		for _, image := range images {
			if imageMatches(image, dm.Image) {
				if v := imageVersion(image); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// splitImage returns an image reference's repository and tag. A digest
// ("@sha256:…") is dropped, and a colon starts the tag only after the last
// "/", so a registry port ("registry:5000/vault") is not taken for one.
func splitImage(image string) (repository, tag string) {
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
		return image[:colon], image[colon+1:]
	}
	return image, ""
}

// imageMatches reports whether image runs repository want: its repository is
// want or ends with "/"+want. So "istio/pilot" matches docker.io/istio/pilot
// and a mirror such as registry.local/docker.io/istio/pilot, but
// argoproj/argocd does not match argoproj/argocd-image-updater.
func imageMatches(image, want string) bool {
	if want == "" {
		return false
	}
	repository, _ := splitImage(image)
	return repository == want || strings.HasSuffix(repository, "/"+want)
}

// imageVersion returns the cleaned tag of image, or "" when it has none.
func imageVersion(image string) string {
	_, tag := splitImage(image)
	return CleanVersion(tag)
}

// versionPrefix matches the version at the start of a tag or app version: an
// optional "v", one to three numeric parts and a semver pre-release named
// alpha, beta or rc.
var versionPrefix = regexp.MustCompile(`^[vV]?\d+(\.\d+){0,2}(-(alpha|beta|rc)(\.?\d+)*)?`)

// CleanVersion drops the build and distribution suffix from a version, so it
// compares as the release it is: "1.14.0-debian-12-r3" → "1.14.0",
// "1.16.1-ent" → "1.16.1", "1.22.0-distroless" → "1.22.0", while
// "v1.15.0-rc.1" keeps its pre-release. A value that does not start with a
// version ("latest") is returned unchanged.
func CleanVersion(v string) string {
	v = strings.TrimSpace(v)
	if prefix := versionPrefix.FindString(v); prefix != "" {
		return prefix
	}
	return v
}
