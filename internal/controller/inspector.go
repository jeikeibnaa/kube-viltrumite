package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
	"github.com/jeikeibnaa/kube-viltrumite/internal/scanner"
)

// ServerVersioner is the part of the discovery client that reports the
// cluster's version.
type ServerVersioner interface {
	ServerVersion() (*version.Info, error)
}

// StackInspector is the ClusterInspector for a live cluster: the version
// comes from the API server, the installed stack from the same scanners and
// mapping the CompatibilityPolicy reconciler uses, over every namespace.
type StackInspector struct {
	Discovery       ServerVersioner
	Scanner         *scanner.ClusterScanner
	WorkloadScanner *scanner.WorkloadScanner
	Matrix          *planner.Matrix
	Detections      []scanner.ToolDetection
}

// KubernetesVersion returns the API server's git version, such as "v1.32.2".
func (s *StackInspector) KubernetesVersion(_ context.Context) (string, error) {
	info, err := s.Discovery.ServerVersion()
	if err != nil {
		return "", fmt.Errorf("server version: %w", err)
	}
	return info.GitVersion, nil
}

// InstalledVersions maps each installed knowledge-base tool to its app
// version. A tool found with no version is left out.
func (s *StackInspector) InstalledVersions(ctx context.Context) (map[string]string, error) {
	found, err := s.Scanner.ScanAll(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cluster scan: %w", err)
	}
	if s.WorkloadScanner != nil {
		raw, rawErr := s.WorkloadScanner.ScanWorkloads(ctx, nil, s.Detections)
		if rawErr != nil {
			// As in the policy reconciler: Helm, Flux and Argo CD finds still count.
			log.FromContext(ctx).Error(rawErr, "workload scan failed, continuing without raw results")
		} else {
			found = append(found, raw...)
		}
	}
	installed, _ := resolveInstalled(s.Matrix, s.Detections, found)
	out := make(map[string]string, len(installed))
	for name, t := range installed {
		if t.CurrentVersion != "" {
			out[name] = t.CurrentVersion
		}
	}
	return out, nil
}
