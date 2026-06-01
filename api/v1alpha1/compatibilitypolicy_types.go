package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

// AutoApproveConfig controls which upgrade classes are auto-approved.
type AutoApproveConfig struct {
	PatchVersions bool `json:"patchVersions,omitempty"`
	MinorVersions bool `json:"minorVersions,omitempty"`
}

// AutoplanConfig controls automatic StackUpgrade creation by the CompatibilityPolicy reconciler.
type AutoplanConfig struct {
	Enabled bool `json:"enabled"`
	// +kubebuilder:validation:Enum=LOW;MEDIUM;HIGH;BLOCKING
	// +kubebuilder:default=LOW
	MaxRisk ai.RiskLevel `json:"maxRisk,omitempty"`
	// AutoApprove creates generated StackUpgrades already in the Approved phase (non-prod use).
	AutoApprove bool `json:"autoApprove,omitempty"`
}

// TrackedToolStatus reports the upgrade status of a single tracked tool.
type TrackedToolStatus struct {
	Name               string       `json:"name"`
	InstalledVersion   string       `json:"installedVersion,omitempty"`
	Source             string       `json:"source,omitempty"`
	Namespace          string       `json:"namespace,omitempty"`
	RecommendedVersion string       `json:"recommendedVersion,omitempty"`
	Installed          bool         `json:"installed"`
	UpgradeAvailable   bool         `json:"upgradeAvailable,omitempty"`
	Risk               ai.RiskLevel `json:"risk,omitempty"`
	Message            string       `json:"message,omitempty"`
}

// AIProviderConfig configures the AI backend used for analysis.
type AIProviderConfig struct {
	// +kubebuilder:validation:Enum=anthropic;ollama;openai;none
	Provider  string                  `json:"provider"`
	Endpoint  string                  `json:"endpoint,omitempty"`
	Model     string                  `json:"model,omitempty"`
	SecretRef *corev1.SecretReference `json:"secretRef,omitempty"`
	Timeout   metav1.Duration         `json:"timeout,omitempty"`
}

// CompatibilityPolicySpec defines the desired state of CompatibilityPolicy.
type CompatibilityPolicySpec struct {
	WatchNamespaces []string          `json:"watchNamespaces,omitempty"`
	RiskTolerance   ai.RiskLevel      `json:"riskTolerance,omitempty"`
	AutoApprove     AutoApproveConfig `json:"autoApprove,omitempty"`
	ScanInterval    metav1.Duration   `json:"scanInterval,omitempty"`
	AI              AIProviderConfig  `json:"ai,omitempty"`
	GitRepo         *GitRepoRef       `json:"gitRepo,omitempty"`
	// TrackedTools restricts scanning to the listed tool names.
	// Empty means discovery mode: track all known tools.
	// +optional
	TrackedTools []string `json:"trackedTools,omitempty"`
	// Autoplan enables automatic StackUpgrade creation for qualifying upgrades.
	// +optional
	Autoplan *AutoplanConfig `json:"autoplan,omitempty"`
}

// CompatibilityPolicyStatus defines the observed state of CompatibilityPolicy.
type CompatibilityPolicyStatus struct {
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`
	// Mode is "discovery" when TrackedTools is empty, "focused" otherwise.
	Mode string `json:"mode,omitempty"`
	// Tools lists the upgrade status of every evaluated tool.
	Tools []TrackedToolStatus `json:"tools,omitempty"`
	// UntrackableTools lists names from TrackedTools that have no KB entry.
	UntrackableTools []string `json:"untrackableTools,omitempty"`
	// UnknownInstalled lists tools found in the cluster that have no KB entry.
	UnknownInstalled []string `json:"unknownInstalled,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Risk Tolerance",type="string",JSONPath=".spec.riskTolerance"
// +kubebuilder:printcolumn:name="Interval",type="string",JSONPath=".spec.scanInterval"
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".status.mode"
// +kubebuilder:printcolumn:name="Last Scan",type="date",JSONPath=".status.lastScanTime"

// CompatibilityPolicy is the Schema for the compatibilitypolicies API.
type CompatibilityPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CompatibilityPolicySpec   `json:"spec,omitempty"`
	Status CompatibilityPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CompatibilityPolicyList contains a list of CompatibilityPolicy.
type CompatibilityPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CompatibilityPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CompatibilityPolicy{}, &CompatibilityPolicyList{})
}
