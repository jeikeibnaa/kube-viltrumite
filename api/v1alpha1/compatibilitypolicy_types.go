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

// AIProviderConfig configures the AI backend used for analysis.
type AIProviderConfig struct {
	// +kubebuilder:validation:Enum=anthropic;ollama;openai;none
	Provider  string                   `json:"provider"`
	Endpoint  string                   `json:"endpoint,omitempty"`
	Model     string                   `json:"model,omitempty"`
	SecretRef *corev1.SecretReference  `json:"secretRef,omitempty"`
	Timeout   metav1.Duration          `json:"timeout,omitempty"`
}

// CompatibilityPolicySpec defines the desired state of CompatibilityPolicy.
type CompatibilityPolicySpec struct {
	WatchNamespaces []string           `json:"watchNamespaces,omitempty"`
	RiskTolerance   ai.RiskLevel       `json:"riskTolerance,omitempty"`
	AutoApprove     AutoApproveConfig  `json:"autoApprove,omitempty"`
	ScanInterval    metav1.Duration    `json:"scanInterval,omitempty"`
	AI              AIProviderConfig   `json:"ai,omitempty"`
	GitRepo         *GitRepoRef        `json:"gitRepo,omitempty"`
}

// CompatibilityPolicyStatus defines the observed state of CompatibilityPolicy.
type CompatibilityPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make generate" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Risk Tolerance",type="string",JSONPath=".spec.riskTolerance"
// +kubebuilder:printcolumn:name="Interval",type="string",JSONPath=".spec.scanInterval"

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
