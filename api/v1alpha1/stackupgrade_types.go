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
	URL       string                  `json:"url"`
	Branch    string                  `json:"branch"`
	Path      string                  `json:"path"`
	SecretRef *corev1.SecretReference `json:"secretRef,omitempty"`
}

// ChartRef is the Helm chart an upgrade installs.
type ChartRef struct {
	// Name is the chart's name, which may differ from the tool's
	// (kube-prometheus-stack for prometheus-stack, istiod for istio).
	Name string `json:"name"`
	// Version is the chart version that ships the target version, from the
	// knowledge base.
	Version string `json:"version"`
	// Repository is the chart repository URL (https:// or oci://): the one
	// the install records (an Argo CD Application's repoURL, a Flux
	// HelmRepository's url), or else the knowledge base's, because a plain
	// Helm release does not record where its chart came from.
	// +optional
	Repository string `json:"repository,omitempty"`
}

// ManagedByRef names the GitOps object that owns a tool's release. Its
// controller reverts a change made with Helm directly.
type ManagedByRef struct {
	// Kind is HelmRelease (Flux, helm.toolkit.fluxcd.io) or Application
	// (Argo CD, argoproj.io).
	// +kubebuilder:validation:Enum=HelmRelease;Application
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ToolUpgradeSpec describes the upgrade of one tool and where the tool is
// installed, as the scan that planned it found it.
type ToolUpgradeSpec struct {
	// Name is the tool's knowledge-base name.
	Name string `json:"name"`
	// CurrentVersion is the installed app version.
	CurrentVersion string `json:"currentVersion"`
	// TargetVersion is the app version to upgrade to.
	TargetVersion string `json:"targetVersion"`
	// Risk is the knowledge base's risk of upgrading to the target version.
	// +kubebuilder:validation:Enum=LOW;MEDIUM;HIGH;BLOCKING
	Risk ai.RiskLevel `json:"risk"`
	// Source is how the tool is installed: helm (a plain Helm release),
	// fluxcd (a Flux HelmRelease), argocd (an Argo CD Application) or raw
	// (kubectl apply, no Helm release).
	// +kubebuilder:validation:Enum=helm;fluxcd;argocd;raw
	Source string `json:"source"`
	// Namespace is the namespace the tool runs in: its Helm release's
	// namespace, an Argo CD Application's destination or, for a raw
	// install, its workloads' namespace.
	Namespace string `json:"namespace"`
	// ReleaseName is the tool's Helm release. Empty for a raw install.
	// +optional
	ReleaseName string `json:"releaseName,omitempty"`
	// Chart is the chart to upgrade to. Unset for a raw install.
	// +optional
	Chart *ChartRef `json:"chart,omitempty"`
	// ManagedBy is the GitOps object that owns the release, for the fluxcd
	// and argocd sources.
	// +optional
	ManagedBy *ManagedByRef `json:"managedBy,omitempty"`
}

// StackUpgradeSpec defines the desired state of StackUpgrade.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.approved) || !oldSelf.approved || self == oldSelf",message="an approved plan cannot change"
type StackUpgradeSpec struct {
	// +kubebuilder:validation:MinItems=1
	Tools []ToolUpgradeSpec `json:"tools"`
	// Approved lets the operator carry out the plan; until then it waits in
	// the Pending phase. An approved plan cannot change, approval included.
	// +kubebuilder:default=false
	// +optional
	Approved bool        `json:"approved,omitempty"`
	GitRepo  *GitRepoRef `json:"gitRepo,omitempty"`
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
// +kubebuilder:printcolumn:name="Approved",type="boolean",JSONPath=".spec.approved"
// +kubebuilder:printcolumn:name="Risk",type="string",JSONPath=".spec.tools[0].risk"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// StackUpgrade is the Schema for the stackupgrades API.
type StackUpgrade struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StackUpgradeSpec   `json:"spec"`
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
