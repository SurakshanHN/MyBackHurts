package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type ScaleDecisionSpec struct {
	GuardName    string      `json:"guardName"`
	Time         metav1.Time `json:"time"`
	Verdict      string      `json:"verdict"`
	Confidence   string      `json:"confidence"`
	FromReplicas int32       `json:"fromReplicas"`
	ToReplicas   int32       `json:"toReplicas"`
	Culprit      string      `json:"culprit,omitempty"`
	Reasons      []string    `json:"reasons,omitempty"`
	ActionTaken  string      `json:"actionTaken"`
}

type ScaleDecisionStatus struct {
	Outcome               string `json:"outcome,omitempty"`
	EstimatedMinutesSaved int    `json:"estimatedMinutesSaved,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ScaleDecision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ScaleDecisionSpec   `json:"spec,omitempty"`
	Status            ScaleDecisionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ScaleDecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScaleDecision `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ScaleDecision{}, &ScaleDecisionList{})
		metav1.AddToGroupVersion(s, GroupVersion)
		return nil
	})
}
