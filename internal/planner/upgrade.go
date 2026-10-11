package planner

// UpgradeStep describes a single helm release upgrade operation.
type UpgradeStep struct {
	ToolName    string
	ReleaseName string
	// ChartRef is the chart: a name looked up in RepoURL, a full oci://
	// reference, or a local path.
	ChartRef string
	// RepoURL is the chart repository ChartRef is looked up in. Empty when
	// ChartRef is a full reference or a path.
	RepoURL     string
	FromVersion string // installed app version
	ToVersion   string // chart version to install
	Namespace   string // overrides executor namespace when non-empty
}
