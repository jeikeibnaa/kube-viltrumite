package planner

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Masterminds/semver/v3"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
	"github.com/jeikeibnaa/kube-viltrumite/knowledge"
)

// minorPattern is how the shipped files write min_kubernetes: a Kubernetes minor.
var minorPattern = regexp.MustCompile(`^1\.[0-9]+$`)

// loadEmbedded loads the knowledge base compiled into the binary — the same
// data the operator uses when --knowledge-base-path is not set.
func loadEmbedded(t *testing.T) *Matrix {
	t.Helper()
	m, err := Load(knowledge.Tools())
	if err != nil {
		t.Fatalf("Load(embedded): %v", err)
	}
	return m
}

func TestLoad(t *testing.T) {
	if m := loadEmbedded(t); len(m.ListTools()) == 0 {
		t.Fatal("embedded knowledge base has no tools")
	}
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{
			name: "empty knowledge base",
			fsys: fstest.MapFS{},
		},
		{
			name: "only non-yaml files",
			fsys: fstest.MapFS{
				".gitkeep":  {},
				"notes.txt": {Data: []byte("tool: not-yaml")},
			},
		},
		{
			name: "missing tool field",
			fsys: fstest.MapFS{"bad.yaml": {Data: []byte("versions: []")}},
		},
		{
			name: "duplicate tool name",
			fsys: fstest.MapFS{
				"a.yaml": {Data: toolYAML("duplicate-tool")},
				"b.yaml": {Data: toolYAML("duplicate-tool")},
			},
		},
		{
			name: "invalid yaml",
			fsys: fstest.MapFS{"broken.yaml": {Data: []byte("tool: [unclosed")}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(tc.fsys); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

// TestLoad_Validation proves that Load enforces each schema rule: every case
// breaks exactly one rule and must fail with a message that names it.
func TestLoad_Validation(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{
			name:    "empty file",
			fsys:    fstest.MapFS{"a.yaml": {Data: []byte("")}},
			wantErr: "missing 'tool' field",
		},
		{
			name:    "tool name not a lowercase DNS label",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("Cert-Manager", "", entry(nil))}},
			wantErr: `tool name "Cert-Manager"`,
		},
		{
			name:    "no versions",
			fsys:    fstest.MapFS{"a.yaml": {Data: []byte("tool: a\nversions: []\n")}},
			wantErr: "no versions",
		},
		{
			name:    "schema v1 version key",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"version": `"1.0.0"`}))}},
			wantErr: "field version not found",
		},
		{
			name: "second YAML document",
			fsys: fstest.MapFS{"a.yaml": {Data: append(toolYAML("a"),
				[]byte("---\ntool: b\nversions: []\n")...)}},
			wantErr: "more than one YAML document",
		},
		{
			name:    "misspelled top-level field",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "alias: [b]\n", entry(nil))}},
			wantErr: "field alias not found",
		},
		{
			name:    "missing app_version",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"app_version": ""}))}},
			wantErr: "missing app_version",
		},
		{
			name:    "app_version does not parse",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"app_version": `"one.two"`}))}},
			wantErr: `app_version: invalid version "one.two"`,
		},
		{
			name: "versions out of order",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "",
				entry(map[string]string{"app_version": `"1.1.0"`, "chart_version": `"1.0.0"`}),
				entry(map[string]string{"app_version": `"1.0.0"`, "chart_version": `"1.1.0"`}),
			)}},
			wantErr: "app_version must be above the previous entry's 1.1.0",
		},
		{
			name: "duplicate version written two ways",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "",
				entry(map[string]string{"app_version": `"1.0.0"`, "chart_version": `"1.0.0"`}),
				entry(map[string]string{"app_version": `"v1.0"`, "chart_version": `"1.1.0"`}),
			)}},
			wantErr: "app_version must be above the previous entry's 1.0.0",
		},
		{
			name: "chart versions out of order",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "",
				entry(map[string]string{"app_version": `"1.0.0"`, "chart_version": `"2.0.0"`}),
				entry(map[string]string{"app_version": `"1.1.0"`, "chart_version": `"1.9.0"`}),
			)}},
			wantErr: "chart_version 1.9.0 must be above the previous entry's 2.0.0",
		},
		{
			name:    "chart_version does not parse",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"chart_version": "latest"}))}},
			wantErr: `chart_version: invalid version "latest"`,
		},
		{
			name:    "missing source",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"source": ""}))}},
			wantErr: "source: missing release-notes URL",
		},
		{
			name:    "relative source",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"source": "RELEASE-NOTES.md"}))}},
			wantErr: "not an absolute http(s) URL",
		},
		{
			name:    "non-http source",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"source": "ftp://example.com/notes"}))}},
			wantErr: "not an absolute http(s) URL",
		},
		{
			name:    "missing min_kubernetes",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"min_kubernetes": ""}))}},
			wantErr: "missing min_kubernetes",
		},
		{
			name:    "risk_level outside the enum",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"risk_level": "severe"}))}},
			wantErr: `risk_level "severe"`,
		},
		{
			name:    "risk_level in upper case",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"risk_level": "LOW"}))}},
			wantErr: `risk_level "LOW"`,
		},
		{
			name:    "missing risk_level",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"risk_level": ""}))}},
			wantErr: `risk_level ""`,
		},
		{
			name:    "missing upgrade_notes",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{"upgrade_notes": ""}))}},
			wantErr: "missing upgrade_notes",
		},
		{
			name: "breaking change type outside the enum",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{
				"breaking_changes": `[{description: "flag renamed", type: api_change}]`,
			}))}},
			wantErr: `breaking_changes[0]: type "api_change"`,
		},
		{
			name: "breaking change without description",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{
				"breaking_changes": `[{type: config_change}]`,
			}))}},
			wantErr: "breaking_changes[0]: missing description",
		},
		{
			name: "incompatible_with names an unknown tool",
			fsys: fstest.MapFS{"a.yaml": {Data: doc("a", "", entry(map[string]string{
				"incompatible_with": `{ingress-nginx: ["1.9.0"]}`,
			}))}},
			wantErr: `incompatible_with names "ingress-nginx"`,
		},
		{
			name: "incompatible_with version does not parse",
			fsys: fstest.MapFS{
				"a.yaml": {Data: doc("a", "", entry(map[string]string{"incompatible_with": `{b: ["latest"]}`}))},
				"b.yaml": {Data: toolYAML("b")},
			},
			wantErr: `incompatible_with[b]: invalid version "latest"`,
		},
		{
			name:    "alias not a lowercase DNS label",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "aliases: [Kube Prometheus]\n", entry(nil))}},
			wantErr: `alias "Kube Prometheus"`,
		},
		{
			name:    "alias repeats the tool name",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "aliases: [a]\n", entry(nil))}},
			wantErr: `alias "a": name already used by tool "a"`,
		},
		{
			name: "alias is another tool's name",
			fsys: fstest.MapFS{
				"a.yaml": {Data: doc("a", "aliases: [b]\n", entry(nil))},
				"b.yaml": {Data: toolYAML("b")},
			},
			wantErr: `tool "b": name already used by tool "a"`,
		},
		{
			name:    "alias_origins for a name that is not an alias",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "aliases: [a-chart]\nalias_origins: {other: [a]}\n", entry(nil))}},
			wantErr: `alias_origins: "other" is not one of this tool's aliases`,
		},
		{
			name:    "alias_origins without words",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "aliases: [a-chart]\nalias_origins: {a-chart: []}\n", entry(nil))}},
			wantErr: "alias_origins[a-chart]: needs one or more non-empty words",
		},
		{
			name:    "detection rule without name or image",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "detection: {deployments: [{version_from: image_tag}]}\n", entry(nil))}},
			wantErr: "detection.deployments[0]: needs a name or an image",
		},
		{
			name:    "detection version_from outside the enum",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "detection: {deployments: [{image: a/a, version_from: annotation}]}\n", entry(nil))}},
			wantErr: `detection.deployments[0]: version_from "annotation"`,
		},
		{
			name:    "schema v2 image_contains detection key",
			fsys:    fstest.MapFS{"a.yaml": {Data: doc("a", "detection: {deployments: [{image_contains: a, version_from: image_tag}]}\n", entry(nil))}},
			wantErr: "field image_contains not found",
		},
		{
			name: "alias declared by two tools",
			fsys: fstest.MapFS{
				"a.yaml": {Data: doc("a", "aliases: [shared]\n", entry(nil))},
				"b.yaml": {Data: doc("b", "aliases: [shared]\n", entry(nil))},
			},
			wantErr: `alias "shared": name already used by tool "a"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.fsys)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestLoad_ReportsEveryProblem checks that a knowledge-base author sees all
// problems at once, each prefixed with its file, and that an invalid file
// still counts as a known tool for the other files' incompatible_with.
func TestLoad_ReportsEveryProblem(t *testing.T) {
	fsys := fstest.MapFS{
		"a.yaml": {Data: doc("a", "", entry(map[string]string{"source": "", "risk_level": "severe"}))},
		"b.yaml": {Data: doc("b", "", entry(map[string]string{
			"min_kubernetes":    "",
			"incompatible_with": `{a: ["1.0.0"]}`,
		}))},
	}
	_, err := Load(fsys)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{
		"matrix: a.yaml: versions[0] (1.0.0): source: missing release-notes URL",
		`matrix: a.yaml: versions[0] (1.0.0): risk_level "severe"`,
		"matrix: b.yaml: versions[0] (1.0.0): missing min_kubernetes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), `incompatible_with names "a"`) {
		t.Errorf("invalid a.yaml must still resolve as a tool name: %q", err)
	}
}

// TestLoad_IncompatibleWithAlias checks that incompatible_with may name a
// tool by one of its aliases, and that Resolve returns it with the entry.
func TestLoad_IncompatibleWithAlias(t *testing.T) {
	fsys := fstest.MapFS{
		"a.yaml": {Data: doc("a", "aliases: [a-chart]\n", entry(nil))},
		"b.yaml": {Data: doc("b", "", entry(map[string]string{"incompatible_with": `{a-chart: ["1.0.0"]}`}))},
	}
	m, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, err := m.Resolve("b", "0.9.0", "1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := e.IncompatibleWith["a-chart"]; !stringSliceEqual(got, []string{"1.0.0"}) {
		t.Errorf("IncompatibleWith[a-chart] = %v, want [1.0.0]", got)
	}
}

// TestLoad_IgnoresSubdirsAndOtherFiles checks that only top-level *.yaml files
// are read, so stray files next to the knowledge base cannot break startup.
func TestLoad_IgnoresSubdirsAndOtherFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"cert-manager.yaml": {Data: toolYAML("cert-manager")},
		".gitkeep":          {},
		"README.md":         {Data: []byte("# not a tool")},
		"drafts/istio.yaml": {Data: toolYAML("istio")},
	}
	m, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.ListTools(); !stringSliceEqual(got, []string{"cert-manager"}) {
		t.Errorf("ListTools() = %v, want [cert-manager]", got)
	}
}

// TestLoad_DirOverride covers --knowledge-base-path: a directory on disk
// replaces the embedded knowledge base entirely.
func TestLoad_DirOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "velero.yaml"), toolYAML("velero"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(os.DirFS(dir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.ListTools(); !stringSliceEqual(got, []string{"velero"}) {
		t.Errorf("ListTools() = %v, want [velero]", got)
	}
}

func TestLoad_DirNotFound(t *testing.T) {
	if _, err := Load(os.DirFS(filepath.Join(t.TempDir(), "missing"))); err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

// TestKnowledgeBase is the validation test over the embedded knowledge base.
// Load enforces the schema: every file parses with no unknown fields,
// required fields are present, risk_level is in its enum, versions are
// sorted and unique, incompatible_with keys are known tools or aliases, and
// every version has a source (TestLoad_Validation shows each rule firing).
// This test adds the conventions the shipped files keep on top of that.
func TestKnowledgeBase(t *testing.T) {
	m := loadEmbedded(t)

	files, err := fs.Glob(knowledge.Tools(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(m.tools) {
		t.Errorf("%d files but %d tools", len(files), len(m.tools))
	}

	for _, f := range files {
		name := strings.TrimSuffix(f, ".yaml")
		t.Run(name, func(t *testing.T) {
			tc, ok := m.tools[name]
			if !ok {
				t.Fatalf("%s must declare tool %q", f, name)
			}
			sources := map[string]string{}
			var prevMinK8s *semver.Version
			for _, v := range tc.Versions {
				if want := normalizeVersion(v.AppVersion); v.AppVersion != want {
					t.Errorf("app_version %q: write it as %q", v.AppVersion, want)
				}
				if v.ChartVersion == "" {
					t.Errorf("%s: missing chart_version (every shipped tool has a Helm chart)", v.AppVersion)
				}
				if !strings.HasPrefix(v.Source, "https://") {
					t.Errorf("%s: source %q is not https", v.AppVersion, v.Source)
				}
				if other, dup := sources[v.Source]; dup {
					t.Errorf("%s: source %q is already the source of %s", v.AppVersion, v.Source, other)
				}
				sources[v.Source] = v.AppVersion

				// min_kubernetes is a minor ("1.29"), and a newer release never
				// supports an older Kubernetes than the release before it.
				if !minorPattern.MatchString(v.MinKubernetes) {
					t.Errorf("%s: min_kubernetes %q: write it as a minor such as \"1.29\"", v.AppVersion, v.MinKubernetes)
				}
				if minK8s, err := parseVersion(v.MinKubernetes); err == nil {
					if prevMinK8s != nil && minK8s.LessThan(prevMinK8s) {
						t.Errorf("%s: min_kubernetes %s is below the previous entry's %s", v.AppVersion, minK8s, prevMinK8s)
					}
					prevMinK8s = minK8s
				}
			}
		})
	}
}

// TestAllToolsLoaded verifies that every expected tool YAML file loads correctly
// and meets minimum content requirements.
func TestAllToolsLoaded(t *testing.T) {
	m := loadEmbedded(t)

	expectedTools := []string{
		"cert-manager",
		"external-secrets",
		"argo-cd",
		"prometheus-stack",
		"istio",
		"vault",
	}

	for _, toolName := range expectedTools {
		t.Run(toolName, func(t *testing.T) {
			tc, ok := m.tools[toolName]
			if !ok {
				t.Fatalf("tool %q not found in matrix", toolName)
			}

			if len(tc.Versions) < 3 {
				t.Errorf("tool %q has %d versions, want at least 3", toolName, len(tc.Versions))
			}

			breakingTotal := 0
			for _, v := range tc.Versions {
				breakingTotal += len(v.BreakingChanges)
			}
			if breakingTotal == 0 {
				t.Errorf("tool %q has no breaking change entries across all versions", toolName)
			}
		})
	}
}

func TestCanonicalName(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{name: "prometheus-stack", want: "prometheus-stack", wantOK: true},
		{name: "kube-prometheus-stack", want: "prometheus-stack", wantOK: true},
		{name: "istiod", want: "istio", wantOK: true},
		{name: "base", want: "istio", wantOK: true},
		{name: "argocd", want: "argo-cd", wantOK: true},
		{name: "argo-cd", want: "argo-cd", wantOK: true},
		{name: "cert-manager", want: "cert-manager", wantOK: true},
		{name: "ingress-nginx"},
		{name: "Cert-Manager"},
		{name: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.CanonicalName(tc.name)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("CanonicalName(%q) = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestDetections checks that every shipped tool can be found as a raw
// install, by the image its main workload runs.
func TestDetections(t *testing.T) {
	m := loadEmbedded(t)

	want := map[string]string{
		"argo-cd":          "argoproj/argocd",
		"cert-manager":     "jetstack/cert-manager-controller",
		"external-secrets": "external-secrets/external-secrets",
		"istio":            "istio/pilot",
		"prometheus-stack": "prometheus-operator/prometheus-operator",
		"vault":            "hashicorp/vault",
	}
	detections := m.Detections()
	if len(detections) != len(want) {
		t.Errorf("Detections() has %d tools, want %d", len(detections), len(want))
	}
	for tool, image := range want {
		d := detections[tool]
		if len(d.Deployments) == 0 {
			t.Errorf("%s: no detection rule", tool)
			continue
		}
		if got := d.Deployments[0].Image; got != image {
			t.Errorf("%s: first rule's image = %q, want %q", tool, got, image)
		}
	}
}

func TestIdentifyChart(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name    string
		chart   string
		origins []string
		want    string
		wantOK  bool
	}{
		{name: "canonical chart name", chart: "cert-manager", want: "cert-manager", wantOK: true},
		{name: "plain alias", chart: "kube-prometheus-stack", want: "prometheus-stack", wantOK: true},
		{name: "istiod needs no origin", chart: "istiod", want: "istio", wantOK: true},
		{name: "base from Istio's chart home", chart: "base", origins: []string{"https://istio.io"}, want: "istio", wantOK: true},
		{
			name: "base from Istio's chart repository, any case", chart: "base",
			origins: []string{"https://ISTIO-release.storage.googleapis.com/charts"}, want: "istio", wantOK: true,
		},
		{name: "base without origin", chart: "base"},
		{name: "base from another project", chart: "base", origins: []string{"https://charts.example.com", "https://github.com/example/base"}},
		{name: "unknown chart", chart: "ingress-nginx", origins: []string{"https://istio.io"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.IdentifyChart(tc.chart, tc.origins)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("IdentifyChart(%q, %v) = (%q, %v), want (%q, %v)", tc.chart, tc.origins, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestAppVersionForChart(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name   string
		tool   string
		chart  string
		want   string
		wantOK bool
	}{
		{name: "argo-cd chart", tool: "argo-cd", chart: "5.43.0", want: "2.8.0", wantOK: true},
		{name: "alias and v prefix", tool: "kube-prometheus-stack", chart: "v58.0.0", want: "0.73.0", wantOK: true},
		{name: "chart published as v1.15.0", tool: "cert-manager", chart: "1.15.0", want: "1.15.0", wantOK: true},
		{name: "chart ships a patch release", tool: "vault", chart: "0.29.1", want: "1.18.1", wantOK: true},
		{name: "chart between entries is not guessed", tool: "argo-cd", chart: "5.46.8"},
		{name: "unparsable chart version", tool: "vault", chart: "latest"},
		{name: "unknown tool", tool: "velero", chart: "1.0.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := m.AppVersionForChart(tc.tool, tc.chart)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("AppVersionForChart(%q, %q) = (%q, %v), want (%q, %v)", tc.tool, tc.chart, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	for v, want := range map[string]bool{"v1.14.0": true, "1.14": true, "0.9.5-rc.1": true, "latest": false, "": false} {
		if got := ValidVersion(v); got != want {
			t.Errorf("ValidVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name              string
		tool              string
		fromVersion       string
		toVersion         string
		wantFrom          string
		wantAppVersion    string
		wantChartVersion  string
		wantMinK8s        string
		wantRisk          string
		wantBreakingCount int
	}{
		{
			name:              "1.11 to 1.12",
			tool:              "cert-manager",
			fromVersion:       "1.11",
			toVersion:         "1.12",
			wantFrom:          "1.11.0",
			wantAppVersion:    "1.12.0",
			wantChartVersion:  "v1.12.0",
			wantMinK8s:        "1.22",
			wantRisk:          "medium",
			wantBreakingCount: 2,
		},
		{
			// Upstream supports 1.21 → 1.27, but the chart requires 1.22.
			name:              "1.12 to 1.13",
			tool:              "cert-manager",
			fromVersion:       "1.12",
			toVersion:         "1.13",
			wantFrom:          "1.12.0",
			wantAppVersion:    "1.13.0",
			wantChartVersion:  "v1.13.0",
			wantMinK8s:        "1.22",
			wantRisk:          "medium",
			wantBreakingCount: 2,
		},
		{
			// The raw versions the cluster scanner reports must match the
			// knowledge-base entries. Upstream says to install 1.14.2, so the
			// 1.14 entry is that patch.
			name:              "v1.13.0 to v1.14.2",
			tool:              "cert-manager",
			fromVersion:       "v1.13.0",
			toVersion:         "v1.14.2",
			wantFrom:          "1.13.0",
			wantAppVersion:    "1.14.2",
			wantChartVersion:  "v1.14.2",
			wantMinK8s:        "1.24",
			wantRisk:          "medium",
			wantBreakingCount: 2,
		},
		{
			name:              "1.14 to 1.15",
			tool:              "cert-manager",
			fromVersion:       "1.14",
			toVersion:         "1.15",
			wantFrom:          "1.14.0",
			wantAppVersion:    "1.15.0",
			wantChartVersion:  "v1.15.0",
			wantMinK8s:        "1.25",
			wantRisk:          "medium",
			wantBreakingCount: 3,
		},
		{
			// rotationPolicy defaults to Always: high risk.
			name:              "1.17 to 1.18",
			tool:              "cert-manager",
			fromVersion:       "v1.17.4",
			toVersion:         "v1.18.2",
			wantFrom:          "1.17.4",
			wantAppVersion:    "1.18.0",
			wantChartVersion:  "v1.18.0",
			wantMinK8s:        "1.29",
			wantRisk:          "high",
			wantBreakingCount: 4,
		},
		{
			name:              "1.20 to 1.21",
			tool:              "cert-manager",
			fromVersion:       "1.20.4",
			toVersion:         "1.21.2",
			wantFrom:          "1.20.4",
			wantAppVersion:    "1.21.0",
			wantChartVersion:  "v1.21.0",
			wantMinK8s:        "1.33",
			wantRisk:          "medium",
			wantBreakingCount: 3,
		},
		{
			name:              "Argo CD major version",
			tool:              "argo-cd",
			fromVersion:       "v2.14.11",
			toVersion:         "v3.0.12",
			wantFrom:          "2.14.11",
			wantAppVersion:    "3.0.0",
			wantChartVersion:  "8.0.0",
			wantMinK8s:        "1.29",
			wantRisk:          "high",
			wantBreakingCount: 9,
		},
		{
			// Istio 1.31 charts come from blob.istio.io.
			name:              "Istio 1.30 to 1.31",
			tool:              "istio",
			fromVersion:       "1.30.5",
			toVersion:         "1.31.1",
			wantFrom:          "1.30.5",
			wantAppVersion:    "1.31.0",
			wantChartVersion:  "1.31.0",
			wantMinK8s:        "1.32",
			wantRisk:          "high",
			wantBreakingCount: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := m.Resolve(tc.tool, tc.fromVersion, tc.toVersion)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if entry.Tool != tc.tool {
				t.Errorf("Tool: got %q, want %q", entry.Tool, tc.tool)
			}
			if entry.FromVersion != tc.wantFrom {
				t.Errorf("FromVersion: got %q, want %q", entry.FromVersion, tc.wantFrom)
			}
			if entry.AppVersion != tc.wantAppVersion {
				t.Errorf("AppVersion: got %q, want %q", entry.AppVersion, tc.wantAppVersion)
			}
			if entry.ChartVersion != tc.wantChartVersion {
				t.Errorf("ChartVersion: got %q, want %q", entry.ChartVersion, tc.wantChartVersion)
			}
			if entry.MinKubernetes != tc.wantMinK8s {
				t.Errorf("MinKubernetes: got %q, want %q", entry.MinKubernetes, tc.wantMinK8s)
			}
			if entry.RiskLevel != tc.wantRisk {
				t.Errorf("RiskLevel: got %q, want %q", entry.RiskLevel, tc.wantRisk)
			}
			if entry.Source == "" {
				t.Error("Source: must not be empty")
			}
			if entry.UpgradeNotes == "" {
				t.Error("UpgradeNotes: must not be empty")
			}

			if len(entry.BreakingChanges) != tc.wantBreakingCount {
				t.Errorf("BreakingChanges count: got %d, want %d", len(entry.BreakingChanges), tc.wantBreakingCount)
			}
		})
	}
}

// TestResolve_Covering checks which entry a release resolves to: its own
// entry, or the latest entry below it on the same major.minor line.
func TestResolve_Covering(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name      string
		tool      string
		from      string
		to        string
		wantTool  string
		wantApp   string
		wantChart string
		wantRisk  string
		wantFrom  string
	}{
		{
			// cert-manager's 1.14 entry is 1.14.2, the patch upstream says to install.
			name: "patch release uses its minor's entry", tool: "cert-manager", from: "1.13.2", to: "v1.14.3",
			wantTool: "cert-manager", wantApp: "1.14.2", wantChart: "v1.14.2", wantRisk: "medium", wantFrom: "1.13.2",
		},
		{
			// Audit finding #6: 0.9.0 and 0.9.5 are separate entries, so the
			// patch must resolve to its own entry, not to 0.9.0.
			name: "patch entry is not collapsed into its minor", tool: "external-secrets", from: "0.9.0", to: "0.9.5",
			wantTool: "external-secrets", wantApp: "0.9.5", wantChart: "0.9.5", wantRisk: "low", wantFrom: "0.9.0",
		},
		{
			name: "release after a patch entry uses the patch entry", tool: "external-secrets", from: "0.9.5", to: "0.9.7",
			wantTool: "external-secrets", wantApp: "0.9.5", wantChart: "0.9.5", wantRisk: "low", wantFrom: "0.9.5",
		},
		{
			name: "release before a patch entry uses the earlier entry", tool: "external-secrets", from: "0.10.0", to: "0.10.3",
			wantTool: "external-secrets", wantApp: "0.10.0", wantChart: "0.10.0", wantRisk: "medium", wantFrom: "0.10.0",
		},
		{
			name: "two entries on one minor", tool: "vault", from: "1.20.1", to: "1.20.6",
			wantTool: "vault", wantApp: "1.20.4", wantChart: "0.31.0", wantRisk: "medium", wantFrom: "1.20.1",
		},
		{
			name: "three entries on one minor", tool: "vault", from: "2.0.2", to: "2.0.3",
			wantTool: "vault", wantApp: "2.0.3", wantChart: "0.34.0", wantRisk: "low", wantFrom: "2.0.2",
		},
		{
			name: "alias resolves to the canonical tool", tool: "kube-prometheus-stack", from: "v0.63.0", to: "v0.66.0",
			wantTool: "prometheus-stack", wantApp: "0.66.0", wantChart: "47.0.0", wantRisk: "medium", wantFrom: "0.63.0",
		},
		{
			name: "unparsable from version is kept as is", tool: "cert-manager", from: "unknown", to: "1.15",
			wantTool: "cert-manager", wantApp: "1.15.0", wantChart: "v1.15.0", wantRisk: "medium", wantFrom: "unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := m.Resolve(tc.tool, tc.from, tc.to)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if entry.Tool != tc.wantTool || entry.AppVersion != tc.wantApp || entry.ChartVersion != tc.wantChart ||
				entry.RiskLevel != tc.wantRisk || entry.FromVersion != tc.wantFrom {
				t.Errorf("Resolve(%q, %q, %q) = {Tool:%q App:%q Chart:%q Risk:%q From:%q}, want {Tool:%q App:%q Chart:%q Risk:%q From:%q}",
					tc.tool, tc.from, tc.to,
					entry.Tool, entry.AppVersion, entry.ChartVersion, entry.RiskLevel, entry.FromVersion,
					tc.wantTool, tc.wantApp, tc.wantChart, tc.wantRisk, tc.wantFrom)
			}
		})
	}
}

func TestResolve_Errors(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name string
		tool string
		to   string
	}{
		{name: "unknown tool", tool: "velero", to: "1.1"},
		{name: "version not in the knowledge base", tool: "cert-manager", to: "1.99"},
		{name: "minor above the last entry", tool: "cert-manager", to: "1.22.0"},
		{name: "minor below the first entry", tool: "cert-manager", to: "1.11.5"},
		{name: "pre-release before its entry", tool: "cert-manager", to: "1.15.0-rc.1"},
		// Upstream says not to install 1.14.0 or 1.19.0: their minors' entries
		// are 1.14.2 and 1.19.1, so the knowledge base covers neither.
		{name: "release below its minor's entry", tool: "cert-manager", to: "1.14.0"},
		{name: "release upstream says to skip", tool: "cert-manager", to: "v1.19.0"},
		{name: "unparsable target", tool: "cert-manager", to: "latest"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if entry, err := m.Resolve(tc.tool, "1.0.0", tc.to); err == nil {
				t.Fatalf("expected an error, got entry %s", entry.AppVersion)
			}
		})
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.14.0", "1.14.0"},
		{"1.14.2", "1.14.2"},
		{"1.14", "1.14.0"},
		{"v1", "1.0.0"},
		{"V0.9.5", "0.9.5"},
		{" 1.2.3 ", "1.2.3"},
		{"1.14.0-rc.1", "1.14.0-rc.1"},
		{"v1.14.0+build.5", "1.14.0+build.5"},
		{"garbage", "garbage"},
		{"latest", "latest"},
		{"1.2.3.4", "1.2.3.4"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeVersion(tc.input)
			if got != tc.want {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.13", "1.14", -1},
		{"1.14", "1.14", 0},
		{"1.15", "1.14", 1},
		{"0.9.0", "0.9.5", -1}, // audit finding #6: patches count
		{"0.9.5", "0.9.0", 1},
		{"v1.14.0", "1.14", 0},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.14.0-rc.1", "1.14.0", -1},
		{"1.14.0-alpha", "1.14.0-beta", -1},
		{"1.14.0+a", "1.14.0+b", 0},
	}
	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			got, err := CompareVersions(tc.a, tc.b)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q): %v", tc.a, tc.b, err)
			}
			if got != tc.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCompareVersions_Errors(t *testing.T) {
	for _, pair := range [][2]string{{"latest", "1.0.0"}, {"1.0.0", ""}, {"1.2.3.4", "1.2.3"}} {
		t.Run(pair[0]+"_vs_"+pair[1], func(t *testing.T) {
			if _, err := CompareVersions(pair[0], pair[1]); err == nil {
				t.Errorf("CompareVersions(%q, %q): expected an error", pair[0], pair[1])
			}
		})
	}
}

func TestListTools(t *testing.T) {
	m := loadEmbedded(t)

	tools := m.ListTools()
	found := false
	for _, n := range tools {
		if n == "cert-manager" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListTools: expected cert-manager to be present")
	}
	if !sort.StringsAreSorted(tools) {
		t.Errorf("ListTools: result not sorted: %v", tools)
	}
	for _, alias := range []string{"kube-prometheus-stack", "istiod"} {
		for _, n := range tools {
			if n == alias {
				t.Errorf("ListTools: alias %q listed as a tool", alias)
			}
		}
	}
}

func TestLatestSafeVersion(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		name        string
		tool        string
		current     string
		tolerance   ai.RiskLevel
		wantVersion string
		wantRisk    ai.RiskLevel
		wantFound   bool
	}{
		{
			// Argo CD 2.14.1 is low; everything above it is medium or high.
			name:        "a riskier newest version is skipped for one within tolerance",
			tool:        "argo-cd",
			current:     "v2.8.4",
			tolerance:   ai.RiskLow,
			wantVersion: "2.14.1",
			wantRisk:    ai.RiskLow,
			wantFound:   true,
		},
		{
			// Only the target entry's risk counts: 2.8.4 → 3.5.0 is reported
			// medium although 3.0.0 on the way is high. Multi-hop paths (S31)
			// are to take the entries in between into account.
			name:        "entries between the installed and the target version do not count",
			tool:        "argo-cd",
			current:     "v2.8.4",
			tolerance:   ai.RiskMedium,
			wantVersion: "3.5.0",
			wantRisk:    ai.RiskMedium,
			wantFound:   true,
		},
		{
			// 1.21.2 is past the 1.21.0 entry, the last one.
			name:      "patch above an entry is not below it",
			tool:      "cert-manager",
			current:   "v1.21.2",
			tolerance: ai.RiskHigh,
			wantFound: false,
		},
		{
			// No cert-manager entry is low risk.
			name:      "nothing within tolerance",
			tool:      "cert-manager",
			current:   "1.13",
			tolerance: ai.RiskLow,
			wantFound: false,
		},
		{
			// Audit finding #6: the old major.minor comparison saw every 2.0.x
			// as equal and never recommended a patch entry.
			name:        "patch entry above the installed version",
			tool:        "vault",
			current:     "v2.0.2",
			tolerance:   ai.RiskLow,
			wantVersion: "2.0.4",
			wantRisk:    ai.RiskLow,
			wantFound:   true,
		},
		{
			name:        "installed patch entry itself is not recommended",
			tool:        "vault",
			current:     "2.0.3",
			tolerance:   ai.RiskLow,
			wantVersion: "2.0.4",
			wantRisk:    ai.RiskLow,
			wantFound:   true,
		},
		{
			name:        "alias with an app version",
			tool:        "kube-prometheus-stack",
			current:     "v0.66.0",
			tolerance:   ai.RiskMedium,
			wantVersion: "0.94.0",
			wantRisk:    ai.RiskMedium,
			wantFound:   true,
		},
		{
			name:      "unparsable installed version recommends nothing",
			tool:      "cert-manager",
			current:   "latest",
			tolerance: ai.RiskHigh,
			wantFound: false,
		},
		{
			name:      "unknown tool",
			tool:      "velero",
			current:   "1.0.0",
			tolerance: ai.RiskHigh,
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, risk, found := m.LatestSafeVersion(tc.tool, tc.current, tc.tolerance)
			if found != tc.wantFound {
				t.Fatalf("found=%v (rec=%q), want %v", found, rec, tc.wantFound)
			}
			if !found {
				return
			}
			if rec != tc.wantVersion {
				t.Errorf("recommended=%q, want %q", rec, tc.wantVersion)
			}
			if risk != tc.wantRisk {
				t.Errorf("risk=%q, want %q", risk, tc.wantRisk)
			}
		})
	}
}

func TestLatestVersion(t *testing.T) {
	m := loadEmbedded(t)

	tests := []struct {
		tool   string
		want   string
		wantOK bool
	}{
		{tool: "cert-manager", want: "1.21.0", wantOK: true},
		{tool: "kube-prometheus-stack", want: "0.94.0", wantOK: true},
		{tool: "vault", want: "2.0.4", wantOK: true},
		{tool: "velero"},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			got, ok := m.LatestVersion(tc.tool)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("LatestVersion(%q) = (%q, %v), want (%q, %v)", tc.tool, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestRiskAtOrBelow(t *testing.T) {
	tests := []struct {
		risk    ai.RiskLevel
		ceiling ai.RiskLevel
		want    bool
	}{
		{ai.RiskLow, ai.RiskMedium, true},
		{ai.RiskHigh, ai.RiskLow, false},
		{ai.RiskLow, ai.RiskLow, true},
		{ai.RiskMedium, ai.RiskMedium, true},
		{ai.RiskBlocking, ai.RiskHigh, false},
	}
	for _, tc := range tests {
		name := string(tc.risk) + "_leq_" + string(tc.ceiling)
		t.Run(name, func(t *testing.T) {
			got := RiskAtOrBelow(tc.risk, tc.ceiling)
			if got != tc.want {
				t.Errorf("RiskAtOrBelow(%q, %q) = %v, want %v", tc.risk, tc.ceiling, got, tc.want)
			}
		})
	}
}

// stringSliceEqual compares two string slices treating nil and empty as equal.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// toolYAML returns a minimal valid knowledge-base document for tool.
func toolYAML(tool string) []byte {
	return doc(tool, "", entry(nil))
}

// doc renders a knowledge-base document for tool. header holds extra
// top-level YAML (such as "aliases: [x]\n") placed before versions.
func doc(tool, header string, entries ...string) []byte {
	return []byte("tool: " + tool + "\n" + header + "versions:\n" + strings.Join(entries, ""))
}

// entry renders one version entry that passes every rule, with overrides
// applied: a key maps to its raw YAML value, and an empty value drops the
// field. Keys outside the defaults are appended in sorted order.
func entry(overrides map[string]string) string {
	keys := []string{"app_version", "chart_version", "source", "min_kubernetes", "risk_level", "upgrade_notes"}
	values := map[string]string{
		"app_version":    `"1.0.0"`,
		"chart_version":  `"1.0.0"`,
		"source":         `"https://example.com/releases/1.0.0"`,
		"min_kubernetes": `"1.28"`,
		"risk_level":     "low",
		"upgrade_notes":  `"Read the release notes."`,
	}
	var extra []string
	for k, v := range overrides {
		if _, isDefault := values[k]; !isDefault {
			extra = append(extra, k)
		}
		values[k] = v
	}
	sort.Strings(extra)

	var lines []string
	for _, k := range append(keys, extra...) {
		if values[k] != "" {
			lines = append(lines, k+": "+values[k])
		}
	}
	return "  - " + strings.Join(lines, "\n    ") + "\n"
}
