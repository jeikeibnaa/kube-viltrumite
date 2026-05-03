package planner

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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

// ToolCompatibility is the top-level document parsed from a tool YAML file.
type ToolCompatibility struct {
	Tool     string         `yaml:"tool"`
	Versions []VersionEntry `yaml:"versions"`
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

// LatestVersion returns the last version string in the matrix for the named tool.
// Returns ("", false) when the tool is unknown or has no versions.
func (m *Matrix) LatestVersion(toolName string) (string, bool) {
	tc, ok := m.tools[toolName]
	if !ok || len(tc.Versions) == 0 {
		return "", false
	}
	return tc.Versions[len(tc.Versions)-1].Version, true
}

// Resolve returns the CompatibilityEntry for upgrading tool from fromVersion to
// toVersion. It returns an error when the tool is unknown or toVersion is not
// present in the matrix.
func (m *Matrix) Resolve(tool, fromVersion, toVersion string) (*CompatibilityEntry, error) {
	tc, ok := m.tools[tool]
	if !ok {
		return nil, fmt.Errorf("matrix: unknown tool %q", tool)
	}

	for _, v := range tc.Versions {
		if v.Version == toVersion {
			return &CompatibilityEntry{
				Tool:         tool,
				FromVersion:  fromVersion,
				VersionEntry: v,
			}, nil
		}
	}

	return nil, fmt.Errorf("matrix: tool %q has no entry for version %q", tool, toVersion)
}
