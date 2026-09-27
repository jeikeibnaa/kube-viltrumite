package planner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
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

// VersionEntry holds compatibility metadata for one release of a tool. An
// entry also covers the later patches on its major.minor line, up to the next
// entry: a 1.14.0 entry describes 1.14.3 too.
type VersionEntry struct {
	// AppVersion is the upstream application version: a raw install's image
	// tag, or the appVersion of the Helm chart. The matrix compares and looks
	// up entries by it.
	AppVersion string `yaml:"app_version"`
	// ChartVersion is the Helm chart version that ships AppVersion, written as
	// the chart repository publishes it. Empty when the tool has no Helm chart.
	ChartVersion string `yaml:"chart_version"`
	// Source is the URL of the release notes the entry is based on.
	Source string `yaml:"source"`
	// MinKubernetes is the earliest Kubernetes minor version this tool version supports.
	MinKubernetes string `yaml:"min_kubernetes"`
	// IncompatibleWith maps a tool name or alias to app versions of that tool
	// known to break with this version.
	IncompatibleWith map[string][]string `yaml:"incompatible_with"`
	// BreakingChanges lists every breaking change introduced in this version.
	BreakingChanges []BreakingChange `yaml:"breaking_changes"`
	// RiskLevel is low | medium | high | blocking: the risk of upgrading to this version.
	RiskLevel string `yaml:"risk_level"`
	// UpgradeNotes contains operator guidance for the upgrade.
	UpgradeNotes string `yaml:"upgrade_notes"`

	// version is AppVersion, parsed by Load.
	version *semver.Version
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
	// Tool is the canonical tool name.
	Tool string `yaml:"tool"`
	// Aliases are other names scanners report for the tool, such as Helm chart
	// names (kube-prometheus-stack for prometheus-stack).
	Aliases   []string       `yaml:"aliases"`
	Detection DetectionSpec  `yaml:"detection"`
	Versions  []VersionEntry `yaml:"versions"`
}

// CompatibilityEntry is the value returned by Resolve. It combines the target
// VersionEntry with the tool name and the source version being upgraded from.
type CompatibilityEntry struct {
	Tool        string
	FromVersion string
	VersionEntry
}

// Matrix holds the parsed compatibility data for all loaded tools. Every
// method that takes a tool accepts its canonical name or one of its aliases.
type Matrix struct {
	// tools maps a canonical tool name to its full compatibility document.
	tools map[string]*ToolCompatibility
	// names maps every canonical name and alias to the canonical name.
	names map[string]string
}

// riskOrder defines the numeric ordering of risk levels for comparison.
var riskOrder = map[ai.RiskLevel]int{
	ai.RiskLow:      0,
	ai.RiskMedium:   1,
	ai.RiskHigh:     2,
	ai.RiskBlocking: 3,
}

// kbRiskLevels maps the risk_level values a knowledge-base file may use to
// the operator's risk levels.
var kbRiskLevels = map[string]ai.RiskLevel{
	"low":      ai.RiskLow,
	"medium":   ai.RiskMedium,
	"high":     ai.RiskHigh,
	"blocking": ai.RiskBlocking,
}

// breakingChangeTypes is the set of allowed BreakingChange.Type values.
var breakingChangeTypes = map[string]bool{
	"api_removal":      true,
	"crd_migration":    true,
	"config_change":    true,
	"behaviour_change": true,
}

// toolNamePattern is what a tool name or alias must look like: a lowercase
// DNS label, like the Helm chart and Kubernetes object names scanners report.
var toolNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// RiskAtOrBelow reports whether risk is at or below ceiling in the ordering
// LOW < MEDIUM < HIGH < BLOCKING.
func RiskAtOrBelow(risk, ceiling ai.RiskLevel) bool {
	return riskOrder[risk] <= riskOrder[ceiling]
}

// Load reads every *.yaml file at the root of fsys and returns a Matrix ready
// for queries. Pass knowledge.Tools() for the embedded knowledge base or
// os.DirFS(dir) for a directory on disk, which replaces the embedded one
// entirely.
//
// Load rejects a knowledge base that breaks the schema described at the top
// of every knowledge/tools file: unknown fields, missing required fields, a
// risk_level or breaking-change type outside its enum, versions that do not
// parse or are not ascending and unique, a tool name or alias declared twice,
// and incompatible_with keys that name no known tool or alias. It reports
// every problem it finds, not just the first.
func Load(fsys fs.FS) (*Matrix, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("matrix: read knowledge base: %w", err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".yaml" {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) == 0 {
		return nil, errors.New("matrix: no yaml files found in knowledge base")
	}

	m := &Matrix{
		tools: make(map[string]*ToolCompatibility),
		names: make(map[string]string),
	}
	var errs []error
	for _, f := range files {
		tc, fileErrs := parseTool(fsys, f)
		if tc != nil && tc.Tool != "" {
			// Register a document that parsed even when it failed validation,
			// so its names still resolve in the incompatible_with check below
			// and one broken file does not add false errors to the others.
			fileErrs = append(fileErrs, m.add(tc)...)
		}
		for _, e := range fileErrs {
			errs = append(errs, fmt.Errorf("matrix: %s: %w", f, e))
		}
	}
	// incompatible_with keys can only be checked once every name is known.
	errs = append(errs, m.checkReferences()...)

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return m, nil
}

// parseTool decodes and validates one knowledge-base file. It returns a nil
// document only when the file cannot be read or parsed. Unknown fields and a
// second YAML document are errors, so a typo, a schema v1 "version" key or a
// stray "---" never drops data silently.
func parseTool(fsys fs.FS, name string) (*ToolCompatibility, []error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, []error{fmt.Errorf("read: %w", err)}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var tc ToolCompatibility
	// io.EOF means an empty file; validateTool reports the missing fields.
	if err := dec.Decode(&tc); err != nil && !errors.Is(err, io.EOF) {
		return nil, []error{fmt.Errorf("parse: %w", err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, []error{errors.New("parse: more than one YAML document; use one file per tool")}
	}
	return &tc, validateTool(&tc)
}

// validateTool checks the rules one document can be checked against on its
// own, and stores each entry's parsed app version for later comparisons.
func validateTool(tc *ToolCompatibility) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	switch {
	case tc.Tool == "":
		fail("missing 'tool' field")
	case !toolNamePattern.MatchString(tc.Tool):
		fail("tool name %q must be lowercase letters, digits and '-'", tc.Tool)
	}
	for _, a := range tc.Aliases {
		if !toolNamePattern.MatchString(a) {
			fail("alias %q must be lowercase letters, digits and '-'", a)
		}
	}
	if len(tc.Versions) == 0 {
		fail("no versions")
	}

	var prevApp, prevChart *semver.Version
	for i := range tc.Versions {
		v := &tc.Versions[i]
		at := fmt.Sprintf("versions[%d] (%s)", i, v.AppVersion)

		if v.AppVersion == "" {
			fail("%s: missing app_version", at)
		} else if parsed, err := parseVersion(v.AppVersion); err != nil {
			fail("%s: app_version: %w", at, err)
		} else {
			if prevApp != nil && !parsed.GreaterThan(prevApp) {
				fail("%s: app_version must be above the previous entry's %s (versions ascending, no duplicates)", at, prevApp)
			}
			v.version, prevApp = parsed, parsed
		}

		if v.ChartVersion != "" {
			if parsed, err := parseVersion(v.ChartVersion); err != nil {
				fail("%s: chart_version: %w", at, err)
			} else {
				if prevChart != nil && !parsed.GreaterThan(prevChart) {
					fail("%s: chart_version %s must be above the previous entry's %s", at, v.ChartVersion, prevChart)
				}
				prevChart = parsed
			}
		}

		if err := validateSource(v.Source); err != nil {
			fail("%s: source: %w", at, err)
		}
		if v.MinKubernetes == "" {
			fail("%s: missing min_kubernetes", at)
		} else if _, err := parseVersion(v.MinKubernetes); err != nil {
			fail("%s: min_kubernetes: %w", at, err)
		}
		if _, ok := kbRiskLevels[v.RiskLevel]; !ok {
			fail("%s: risk_level %q is not one of low, medium, high, blocking", at, v.RiskLevel)
		}
		if strings.TrimSpace(v.UpgradeNotes) == "" {
			fail("%s: missing upgrade_notes", at)
		}
		for j, bc := range v.BreakingChanges {
			if strings.TrimSpace(bc.Description) == "" {
				fail("%s: breaking_changes[%d]: missing description", at, j)
			}
			if !breakingChangeTypes[bc.Type] {
				fail("%s: breaking_changes[%d]: type %q is not one of api_removal, crd_migration, config_change, behaviour_change", at, j, bc.Type)
			}
		}
		for _, other := range sortedKeys(v.IncompatibleWith) {
			for _, bad := range v.IncompatibleWith[other] {
				if _, err := parseVersion(bad); err != nil {
					fail("%s: incompatible_with[%s]: %w", at, other, err)
				}
			}
		}
	}
	return errs
}

// validateSource requires an absolute http(s) URL.
func validateSource(raw string) error {
	if raw == "" {
		return errors.New("missing release-notes URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an absolute http(s) URL", raw)
	}
	return nil
}

// add registers tc under its canonical name and aliases. Each name may
// belong to one tool only, so an alias always resolves to a single tool.
func (m *Matrix) add(tc *ToolCompatibility) []error {
	if owner, taken := m.names[tc.Tool]; taken {
		return []error{fmt.Errorf("tool %q: name already used by tool %q", tc.Tool, owner)}
	}
	var errs []error
	names := map[string]string{tc.Tool: tc.Tool}
	for _, a := range tc.Aliases {
		owner, taken := m.names[a]
		if !taken {
			owner, taken = names[a]
		}
		if taken {
			errs = append(errs, fmt.Errorf("alias %q: name already used by tool %q", a, owner))
			continue
		}
		names[a] = tc.Tool
	}
	if len(errs) > 0 {
		return errs
	}

	for n, canonical := range names {
		m.names[n] = canonical
	}
	m.tools[tc.Tool] = tc
	return nil
}

// checkReferences reports incompatible_with keys that name no tool or alias
// in the knowledge base: a typo there would silently disable the check.
func (m *Matrix) checkReferences() []error {
	var errs []error
	for _, name := range m.ListTools() {
		tc := m.tools[name]
		for _, v := range tc.Versions {
			for _, other := range sortedKeys(v.IncompatibleWith) {
				if _, ok := m.names[other]; !ok {
					errs = append(errs, fmt.Errorf("matrix: %s %s: incompatible_with names %q, which is not a tool or alias in the knowledge base", tc.Tool, v.AppVersion, other))
				}
			}
		}
	}
	return errs
}

// CanonicalName maps name, a tool name or one of its aliases (for example the
// Helm chart name kube-prometheus-stack), to the tool's canonical name. ok is
// false when the knowledge base does not know the name.
func (m *Matrix) CanonicalName(name string) (string, bool) {
	canonical, ok := m.names[name]
	return canonical, ok
}

// tool returns the compatibility document for a tool name or alias.
func (m *Matrix) tool(name string) (*ToolCompatibility, bool) {
	canonical, ok := m.names[name]
	if !ok {
		return nil, false
	}
	return m.tools[canonical], true
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

// LatestSafeVersion finds the highest app version of tool above currentVersion
// whose risk level is at or below tolerance. Versions compare by full semver,
// so a patch entry counts: 0.10.5 is above 0.10.0. Returns ("", "", false)
// when the tool is unknown, currentVersion does not parse, or no version
// qualifies.
func (m *Matrix) LatestSafeVersion(tool, currentVersion string, tolerance ai.RiskLevel) (string, ai.RiskLevel, bool) {
	tc, ok := m.tool(tool)
	if !ok {
		return "", "", false
	}
	current, err := parseVersion(currentVersion)
	if err != nil {
		// An unknown installed version gives no safe basis for a recommendation.
		return "", "", false
	}

	var best *VersionEntry
	for i := range tc.Versions {
		v := &tc.Versions[i]
		if !v.version.GreaterThan(current) {
			continue
		}
		if !RiskAtOrBelow(kbRiskLevels[v.RiskLevel], tolerance) {
			continue
		}
		if best == nil || v.version.GreaterThan(best.version) {
			best = v
		}
	}

	if best == nil {
		return "", "", false
	}
	return best.AppVersion, kbRiskLevels[best.RiskLevel], true
}

// LatestVersion returns the highest app version in the matrix for the named tool.
// Returns ("", false) when the tool is unknown or has no versions.
func (m *Matrix) LatestVersion(toolName string) (string, bool) {
	tc, ok := m.tool(toolName)
	if !ok || len(tc.Versions) == 0 {
		return "", false
	}
	// Load guarantees ascending versions.
	return tc.Versions[len(tc.Versions)-1].AppVersion, true
}

// Detections returns the detection spec for each tool, keyed by tool name.
func (m *Matrix) Detections() map[string]DetectionSpec {
	result := make(map[string]DetectionSpec, len(m.tools))
	for name, tc := range m.tools {
		result[name] = tc.Detection
	}
	return result
}

// parseVersion parses a version leniently: surrounding space and a "v" or "V"
// prefix are dropped, and a partial version is completed with zeros ("1.14"
// is 1.14.0). Pre-release and build parts follow semver.
func parseVersion(raw string) (*semver.Version, error) {
	s := strings.TrimSpace(raw)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	v, err := semver.NewVersion(s)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", raw, err)
	}
	return v, nil
}

// normalizeVersion returns raw as a full semver string without a "v" prefix:
// "v1.14.0" → "1.14.0", "1.14" → "1.14.0", "1.14.2" → "1.14.2".
// Returns raw unchanged when it cannot be parsed.
func normalizeVersion(raw string) string {
	v, err := parseVersion(raw)
	if err != nil {
		return raw
	}
	return v.String()
}

// CompareVersions compares two versions by semver precedence and returns -1
// if a < b, 0 if they are equal and +1 if a > b. Patch releases count
// (0.9.0 < 0.9.5); a "v" prefix and partial versions ("1.14" is 1.14.0) are
// accepted. It returns an error when either version does not parse.
func CompareVersions(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// Resolve returns the CompatibilityEntry for upgrading tool from fromVersion
// to toVersion. The entry is the one for toVersion itself or, when the
// knowledge base has no entry for that exact release, the latest entry below
// it on the same major.minor line: "v1.14.3" resolves to the 1.14.0 entry,
// and external-secrets 0.9.7 to 0.9.5, not 0.9.0. It returns an error when
// the tool is unknown, toVersion does not parse, or no entry covers it.
// FromVersion is normalised the same way (see normalizeVersion).
func (m *Matrix) Resolve(tool, fromVersion, toVersion string) (*CompatibilityEntry, error) {
	tc, ok := m.tool(tool)
	if !ok {
		return nil, fmt.Errorf("matrix: unknown tool %q", tool)
	}
	to, err := parseVersion(toVersion)
	if err != nil {
		return nil, fmt.Errorf("matrix: tool %q: target: %w", tool, err)
	}

	var best *VersionEntry
	for i := range tc.Versions {
		v := &tc.Versions[i]
		if v.version.Major() != to.Major() || v.version.Minor() != to.Minor() || v.version.GreaterThan(to) {
			continue
		}
		if best == nil || v.version.GreaterThan(best.version) {
			best = v
		}
	}
	if best == nil {
		return nil, fmt.Errorf("matrix: tool %q has no entry for version %q", tool, toVersion)
	}

	return &CompatibilityEntry{
		Tool:         tc.Tool,
		FromVersion:  normalizeVersion(fromVersion),
		VersionEntry: *best,
	}, nil
}

// sortedKeys returns the keys of m in ascending order, for stable error output.
func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
