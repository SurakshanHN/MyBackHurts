package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:validation:Enum=observe;enforce
type Mode string

const (
	ModeObserve Mode = "observe"
	ModeEnforce Mode = "enforce"
)

type Dependency struct {
	Name             string `json:"name"`
	SaturationQuery  string `json:"saturationQuery"`
	ThresholdPercent int    `json:"thresholdPercent"` // default 90
}

type QuerySet struct {
	RPS       string `json:"rps"`
	P99Millis string `json:"p99Millis"`
	ErrorRate string `json:"errorRate"` // 0-1
}

type ScaleGuardSpec struct {
	HPAName           string          `json:"hpaName"`
	Mode              Mode            `json:"mode"`
	PrometheusURL     string          `json:"prometheusURL"`
	Queries           QuerySet        `json:"queries"`
	Dependencies      []Dependency    `json:"dependencies,omitempty"`
	WarmupWindow      metav1.Duration `json:"warmupWindow"`      // default 2m
	MaxFreezeDuration metav1.Duration `json:"maxFreezeDuration"` // default 30m
	MinReplicasFloor  int32           `json:"minReplicasFloor"`
}

type CapStatus struct {
	OriginalMaxReplicas int32       `json:"originalMaxReplicas"`
	CappedAt            int32       `json:"cappedAt"`
	Since               metav1.Time `json:"since"`
	Reason              string      `json:"reason"`
}

type ScaleGuardStatus struct {
	Phase          string             `json:"phase,omitempty"`
	LastVerdict    string             `json:"lastVerdict,omitempty"`
	LastConfidence string             `json:"lastConfidence,omitempty"`
	LastMessage    string             `json:"lastMessage,omitempty"`
	Cap            *CapStatus         `json:"cap,omitempty"`
	TrustScore     int                `json:"trustScore,omitempty"`
	Conditions     []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ScaleGuard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ScaleGuardSpec   `json:"spec,omitempty"`
	Status            ScaleGuardStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ScaleGuardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScaleGuard `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ScaleGuard{}, &ScaleGuardList{})
		metav1.AddToGroupVersion(s, GroupVersion)
		return nil
	})
}
