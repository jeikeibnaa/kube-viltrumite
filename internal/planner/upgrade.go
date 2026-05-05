package planner

// UpgradeStep describes a single helm release upgrade operation.
type UpgradeStep struct {
	ToolName    string
	ReleaseName string
	ChartRef    string // local path or "repo/chart"
	FromVersion string
	ToVersion   string
	Namespace   string // overrides executor namespace when non-empty
}
