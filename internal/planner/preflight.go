package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Severity is how a pre-flight finding affects a plan.
type Severity string

const (
	// SeverityBlock stops the plan: the knowledge base records the
	// combination as broken.
	SeverityBlock Severity = "Block"
	// SeverityWarn lets the plan go on and tells the person approving it.
	SeverityWarn Severity = "Warn"
)

// MinKubernetesSeverity is what a cluster below a tool's min_kubernetes
// counts as. min_kubernetes is the oldest Kubernetes upstream supports or
// tests the release on (S26), which is stricter than what the chart
// installs on (its kubeVersion), so it warns: blocking would stop upgrades
// that work. Change it here to make the floor a hard gate.
const MinKubernetesSeverity = SeverityWarn

// PlannedTool is one tool of a plan, as pre-flight sees it.
type PlannedTool struct {
	Name          string
	TargetVersion string
}

// Finding is one result of a pre-flight check.
type Finding struct {
	Severity Severity
	// Tool is the canonical name of the tool the finding is about.
	Tool    string
	Message string
}

// Blocked reports whether any finding is a block.
func Blocked(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityBlock {
			return true
		}
	}
	return false
}

// Preflight checks a plan against the knowledge base before it runs.
//
// kubernetesVersion is the cluster's version (any form Kubernetes reports,
// "v1.32.2" or "v1.29.4+k3s1"); empty skips the min_kubernetes check. installed
// maps tools (a name or alias) to the app version running now; a tool the plan
// upgrades is checked at its target version instead, and a tool with no known
// version is not checked. Findings come out in a stable order.
//
// Two checks run:
//   - min_kubernetes: each target's floor against the cluster version.
//   - incompatible_with: the stack as it will be after the plan. Each target's
//     entry is checked against the other tools' versions, and the entries of
//     installed tools the plan leaves alone are checked against each target,
//     since either side may be the one that records the conflict.
func (m *Matrix) Preflight(tools []PlannedTool, kubernetesVersion string, installed map[string]string) []Finding {
	// The stack after the plan, by canonical name.
	after := make(map[string]string)
	for name, v := range installed {
		if c, ok := m.CanonicalName(name); ok && v != "" {
			after[c] = v
		}
	}
	targets := make(map[string]*CompatibilityEntry)
	for _, t := range tools {
		c, ok := m.CanonicalName(t.Name)
		if !ok {
			continue
		}
		after[c] = t.TargetVersion
		if e, err := m.Resolve(c, "", t.TargetVersion); err == nil {
			targets[c] = e
		}
	}

	var out []Finding
	seen := make(map[string]bool)
	add := func(f Finding) {
		if key := string(f.Severity) + "|" + f.Tool + "|" + f.Message; !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}

	names := make([]string, 0, len(targets))
	for c := range targets {
		names = append(names, c)
	}
	sort.Strings(names)

	cluster, clusterErr := parseKubernetesMinor(kubernetesVersion)
	for _, c := range names {
		e := targets[c]
		if kubernetesVersion != "" && clusterErr == nil && e.MinKubernetes != "" {
			if floor, err := parseKubernetesMinor(e.MinKubernetes); err == nil && cluster.LessThan(floor) {
				add(Finding{MinKubernetesSeverity, c, fmt.Sprintf("%s %s supports Kubernetes %s and later; the cluster runs %s", c, e.AppVersion, e.MinKubernetes, strings.TrimPrefix(kubernetesVersion, "v"))})
			}
		}
		// This target's entry against the rest of the stack.
		for _, f := range m.conflicts(e, c, after) {
			add(f)
		}
	}

	// Installed tools the plan leaves alone: their own entries may name a
	// target.
	var rest []string
	for c := range after {
		if _, planned := targets[c]; !planned {
			rest = append(rest, c)
		}
	}
	sort.Strings(rest)
	for _, c := range rest {
		e, err := m.Resolve(c, "", after[c])
		if err != nil {
			continue
		}
		for _, f := range m.conflicts(e, c, after) {
			// Only a conflict with a tool the plan changes is the plan's doing.
			if _, planned := targets[f.Tool]; planned {
				add(f)
			}
		}
	}
	return out
}

// conflicts lists the incompatible_with entries of e (tool's entry, at its
// version in after) that the stack after the plan runs into. The finding is
// about the other tool: it is the one whose version is listed.
func (m *Matrix) conflicts(e *CompatibilityEntry, tool string, after map[string]string) []Finding {
	var out []Finding
	for _, other := range sortedKeys(e.IncompatibleWith) {
		oc, ok := m.CanonicalName(other)
		if !ok {
			continue
		}
		have, ok := after[oc]
		if !ok {
			continue
		}
		for _, listed := range e.IncompatibleWith[other] {
			if matchesListed(have, listed) {
				out = append(out, Finding{SeverityBlock, oc, fmt.Sprintf("%s %s is incompatible with %s %s (listed as %s)", tool, e.AppVersion, oc, have, listed)})
				break
			}
		}
	}
	return out
}

// matchesListed reports whether version is covered by listed, a version from
// an incompatible_with list. A partial version names a line: "1" is every 1.x,
// "1.14" every 1.14.x, and a full "1.14.2" that release alone (pre-releases
// of it are separate). Neither side parsing means no match.
func matchesListed(version, listed string) bool {
	have, err := parseVersion(version)
	if err != nil {
		return false
	}
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(listed), "v"), "V")
	want, err := parseVersion(s)
	if err != nil {
		return false
	}
	switch strings.Count(s, ".") {
	case 0:
		return have.Major() == want.Major()
	case 1:
		return have.Major() == want.Major() && have.Minor() == want.Minor()
	default:
		return have.Equal(want)
	}
}

// parseKubernetesMinor reduces a Kubernetes version ("v1.29.4+k3s1", "1.25")
// to its major.minor, since min_kubernetes names minors.
func parseKubernetesMinor(raw string) (*semver.Version, error) {
	v, err := parseVersion(raw)
	if err != nil {
		return nil, err
	}
	return semver.New(v.Major(), v.Minor(), 0, "", ""), nil
}
