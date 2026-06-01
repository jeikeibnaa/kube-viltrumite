package planner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

// BreakingChange describes a single breaking change introduced in a tool version.
type BreakingChange struct {
	// Description is a human-readable summary of the change.
	Description string `yaml:"description"`
	// Type categorises the change: api_removal | crd_migration | config_change | behaviour_change.
	Type string `yaml:"type"`
}

// VersionEntry holds compatibility metadata for one minor version of a tool.
type VersionEntry struct {
	// Version is the semver version string.
	Version string `yaml:"version"`
	// MinKubernetes is the earliest Kubernetes minor version this tool version supports.
	MinKubernetes string `yaml:"min_kubernetes"`
	// IncompatibleWith maps a tool name to a list of its known-bad version strings.
	IncompatibleWith map[string][]string `yaml:"incompatible_with"`
	// BreakingChanges lists every breaking change introduced in this version.
	BreakingChanges []BreakingChange `yaml:"breaking_changes"`
	// RiskLevel is low | medium | high and reflects upgrade risk from the previous minor.
	RiskLevel string `yaml:"risk_level"`
	// UpgradeNotes contains operator guidance for the upgrade.
	UpgradeNotes string `yaml:"upgrade_notes"`
}

// DeploymentMatchSpec holds workload-matching criteria parsed from a knowledge-base file.
type DeploymentMatchSpec struct {
	Name          string `yaml:"name"`
	NamespaceHint string `yaml:"namespace_hint"`
	Container     string `yaml:"container"`
	ImageContains string `yaml:"image_contains"`
	VersionFrom   string `yaml:"version_from"`
}

// DetectionSpec holds the workload-detection metadata for a tool.
type DetectionSpec struct {
	Deployments []DeploymentMatchSpec `yaml:"deployments"`
	Labels      map[string]string     `yaml:"labels"`
}

// ToolCompatibility is the top-level document parsed from a tool YAML file.
type ToolCompatibility struct {
	Tool      string        `yaml:"tool"`
	Detection DetectionSpec `yaml:"detection"`
	Versions  []VersionEntry `yaml:"versions"`
}

// CompatibilityEntry is the value returned by Resolve. It combines the target
// VersionEntry with the tool name and the source version being upgraded from.
type CompatibilityEntry struct {
	Tool        string
	FromVersion string
	VersionEntry
}

// Matrix holds the parsed compatibility data for all loaded tools.
type Matrix struct {
	// tools maps a tool name to its full compatibility document.
	tools map[string]*ToolCompatibility
}

// riskOrder defines the numeric ordering of risk levels for comparison.
var riskOrder = map[ai.RiskLevel]int{
	ai.RiskLow:      0,
	ai.RiskMedium:   1,
	ai.RiskHigh:     2,
	ai.RiskBlocking: 3,
}

// RiskAtOrBelow reports whether risk is at or below ceiling in the ordering
// LOW < MEDIUM < HIGH < BLOCKING.
func RiskAtOrBelow(risk, ceiling ai.RiskLevel) bool {
	return riskOrder[risk] <= riskOrder[ceiling]
}

// Load reads all *.yaml files from the directory at dir and returns a Matrix
// ready for queries. Every file must contain a valid ToolCompatibility document
// with a non-empty top-level tool field.
func Load(dir string) (*Matrix, error) {
	pattern := filepath.Join(dir, "*.yaml")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("matrix: glob %s: %w", pattern, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("matrix: no yaml files found in %s", dir)
	}

	m := &Matrix{tools: make(map[string]*ToolCompatibility)}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("matrix: read %s: %w", f, err)
		}

		var tc ToolCompatibility
		if err := yaml.Unmarshal(data, &tc); err != nil {
			return nil, fmt.Errorf("matrix: parse %s: %w", f, err)
		}
		if tc.Tool == "" {
			return nil, fmt.Errorf("matrix: %s: missing 'tool' field", f)
		}
		if _, dup := m.tools[tc.Tool]; dup {
			return nil, fmt.Errorf("matrix: duplicate tool %q declared in %s", tc.Tool, f)
		}

		m.tools[tc.Tool] = &tc
	}
	return m, nil
}

// ListTools returns all tool names that have a knowledge-base entry, sorted alphabetically.
func (m *Matrix) ListTools() []string {
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LatestSafeVersion finds the highest version of tool above currentVersion whose risk
// level is at or below tolerance. Returns ("", "", false) when no qualifying version exists.
func (m *Matrix) LatestSafeVersion(tool, currentVersion string, tolerance ai.RiskLevel) (string, ai.RiskLevel, bool) {
	tc, ok := m.tools[tool]
	if !ok {
		return "", "", false
	}

	var bestVersion string
	var bestRisk ai.RiskLevel

	for _, v := range tc.Versions {
		if CompareMinor(v.Version, currentVersion) <= 0 {
			continue
		}
		entryRisk := ai.RiskLevel(strings.ToUpper(v.RiskLevel))
		if !RiskAtOrBelow(entryRisk, tolerance) {
			continue
		}
		if bestVersion == "" || CompareMinor(v.Version, bestVersion) > 0 {
			bestVersion = v.Version
			bestRisk = entryRisk
		}
	}

	if bestVersion == "" {
		return "", "", false
	}
	return bestVersion, bestRisk, true
}

// LatestVersion returns the last version string in the matrix for the named tool.
// Returns ("", false) when the tool is unknown or has no versions.
func (m *Matrix) LatestVersion(toolName string) (string, bool) {
	tc, ok := m.tools[toolName]
	if !ok || len(tc.Versions) == 0 {
		return "", false
	}
	return tc.Versions[len(tc.Versions)-1].Version, true
}

// Detections returns the detection spec for each tool, keyed by tool name.
func (m *Matrix) Detections() map[string]DetectionSpec {
	result := make(map[string]DetectionSpec, len(m.tools))
	for name, tc := range m.tools {
		result[name] = tc.Detection
	}
	return result
}

// normalizeVersion converts a raw version string to major.minor form.
// "v1.14.0" → "1.14", "1.14.2" → "1.14", "1.14" → "1.14".
// Returns raw unchanged when it cannot be parsed.
func normalizeVersion(raw string) string {
	s := raw
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return raw
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return raw
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return raw
	}
	return parts[0] + "." + parts[1]
}

// CompareMinor compares two version strings as major.minor pairs.
// Returns -1 if a < b, 0 if equal, +1 if a > b.
func CompareMinor(a, b string) int {
	aN := normalizeVersion(a)
	bN := normalizeVersion(b)

	aParts := strings.SplitN(aN, ".", 2)
	bParts := strings.SplitN(bN, ".", 2)

	if len(aParts) < 2 || len(bParts) < 2 {
		if aN < bN {
			return -1
		}
		if aN > bN {
			return 1
		}
		return 0
	}

	aMaj, _ := strconv.Atoi(aParts[0])
	aMin, _ := strconv.Atoi(aParts[1])
	bMaj, _ := strconv.Atoi(bParts[0])
	bMin, _ := strconv.Atoi(bParts[1])

	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin != bMin {
		if aMin < bMin {
			return -1
		}
		return 1
	}
	return 0
}

// Resolve returns the CompatibilityEntry for upgrading tool from fromVersion to
// toVersion. It returns an error when the tool is unknown or toVersion is not
// present in the matrix. Both arguments and knowledge-base versions are
// normalised to major.minor before comparison so "v1.14.0" matches "1.14".
func (m *Matrix) Resolve(tool, fromVersion, toVersion string) (*CompatibilityEntry, error) {
	tc, ok := m.tools[tool]
	if !ok {
		return nil, fmt.Errorf("matrix: unknown tool %q", tool)
	}

	normFrom := normalizeVersion(fromVersion)
	normTo := normalizeVersion(toVersion)

	for _, v := range tc.Versions {
		if normalizeVersion(v.Version) == normTo {
			return &CompatibilityEntry{
				Tool:         tool,
				FromVersion:  normFrom,
				VersionEntry: v,
			}, nil
		}
	}

	return nil, fmt.Errorf("matrix: tool %q has no entry for version %q", tool, toVersion)
}
