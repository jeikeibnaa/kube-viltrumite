package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

// UpgradePhase represents the current phase of an upgrade.
type UpgradePhase string

const (
	UpgradePhasePending    UpgradePhase = "Pending"
	UpgradePhaseApproved   UpgradePhase = "Approved"
	UpgradePhaseUpgrading  UpgradePhase = "Upgrading"
	UpgradePhaseSucceeded  UpgradePhase = "Succeeded"
	UpgradePhaseFailed     UpgradePhase = "Failed"
	UpgradePhaseRolledBack UpgradePhase = "RolledBack"
)

// GitRepoRef references a git repository with optional auth.
type GitRepoRef struct {
	URL       string                    `json:"url"`
	Branch    string                    `json:"branch"`
	Path      string                    `json:"path"`
	SecretRef *corev1.SecretReference   `json:"secretRef,omitempty"`
}

// ToolUpgradeSpec describes a single tool upgrade.
type ToolUpgradeSpec struct {
	Name           string      `json:"name"`
	CurrentVersion string      `json:"currentVersion"`
	TargetVersion  string      `json:"targetVersion"`
	Risk           ai.RiskLevel `json:"risk"`
}

// StackUpgradeSpec defines the desired state of StackUpgrade.
type StackUpgradeSpec struct {
	Tools           []ToolUpgradeSpec `json:"tools,omitempty"`
	ApprovalRequired bool             `json:"approvalRequired,omitempty"`
	GitRepo         *GitRepoRef       `json:"gitRepo,omitempty"`
}

// StackUpgradeStatus defines the observed state of StackUpgrade.
type StackUpgradeStatus struct {
	Phase         UpgradePhase       `json:"phase,omitempty"`
	StartedAt     *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt   *metav1.Time       `json:"completedAt,omitempty"`
	FailureReason string             `json:"failureReason,omitempty"`
	Conditions    []metav1.Condition `json:"conditions,omitempty"`
	CurrentStep   int                `json:"currentStep,omitempty"`
	TotalSteps    int                `json:"totalSteps,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Risk",type="string",JSONPath=".spec.tools[0].risk"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// StackUpgrade is the Schema for the stackupgrades API.
type StackUpgrade struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StackUpgradeSpec   `json:"spec,omitempty"`
	Status StackUpgradeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StackUpgradeList contains a list of StackUpgrade.
type StackUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StackUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StackUpgrade{}, &StackUpgradeList{})
}
