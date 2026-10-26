/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ScaleGuardSpec defines the desired state of ScaleGuard
type ScaleGuardSpec struct {
	// TargetHPA is the name of the HorizontalPodAutoscaler to protect.
	// +optional
	TargetHPA string `json:"targetHPA,omitempty"`

	// Mode is either "observe" or "enforce". Defaults to "observe".
	// +optional
	// +kubebuilder:default="observe"
	Mode string `json:"mode,omitempty"`

	// TTL is the duration for which a cap is applied before expiring.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// Floor is the minimum replica count to never cap below.
	// +optional
	Floor *int32 `json:"floor,omitempty"`

	// WarmupSeconds is the cooldown period after scale-up before evaluating metrics.
	// +optional
	WarmupSeconds *int `json:"warmupSeconds,omitempty"`

	// MinSamples is the minimum number of pre and post samples required.
	// +optional
	MinSamples *int `json:"minSamples,omitempty"`

	// Step is the metric sampling interval.
	// +optional
	Step *metav1.Duration `json:"step,omitempty"`

	// Dependencies lists downstream dependencies to evaluate for contention.
	// +optional
	Dependencies []string `json:"dependencies,omitempty"`

	// HarmMargin is the margin threshold for harm detection (e.g. "0.15").
	// +optional
	HarmMargin string `json:"harmMargin,omitempty"`

	// SaturationPct is the saturation threshold percentage (e.g. "90.0").
	// +optional
	SaturationPct string `json:"saturationPct,omitempty"`

	// MinConfidence is the minimum confidence score required (e.g. "0.6").
	// +optional
	MinConfidence string `json:"minConfidence,omitempty"`

	// MinThroughputGain is the minimum throughput gain expected from scaling (e.g. "0.05").
	// +optional
	MinThroughputGain string `json:"minThroughputGain,omitempty"`
}

// ScaleGuardStatus defines the observed state of ScaleGuard.
type ScaleGuardStatus struct {
	// conditions represent the current state of the ScaleGuard resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastObservedReplicas is the replica count last recorded from the HPA.
	// +optional
	LastObservedReplicas int32 `json:"lastObservedReplicas,omitempty"`

	// ScaleUpFrom is the replica count before the most recent scale-up.
	// +optional
	ScaleUpFrom int32 `json:"scaleUpFrom,omitempty"`

	// ScaleUpTo is the replica count after the most recent scale-up.
	// +optional
	ScaleUpTo int32 `json:"scaleUpTo,omitempty"`

	// LastScaleUpTime is the timestamp when the most recent scale-up occurred.
	// +optional
	LastScaleUpTime *metav1.Time `json:"lastScaleUpTime,omitempty"`

	// Capped indicates whether the target HPA is currently capped.
	// +optional
	Capped bool `json:"capped,omitempty"`

	// LastVerdict is the verdict from the most recent evaluation.
	// +optional
	LastVerdict string `json:"lastVerdict,omitempty"`

	// LastAction is the action decided from the most recent evaluation.
	// +optional
	LastAction string `json:"lastAction,omitempty"`
}

func (s *ScaleGuard) GetTargetHPAName() string {
	if s.Spec.TargetHPA != "" {
		return s.Spec.TargetHPA
	}
	return s.Name
}

func (s *ScaleGuard) GetMode() string {
	if s.Spec.Mode == "" {
		return "observe"
	}
	return s.Spec.Mode
}

func (s *ScaleGuard) GetTTL() time.Duration {
	if s.Spec.TTL != nil && s.Spec.TTL.Duration > 0 {
		return s.Spec.TTL.Duration
	}
	return 10 * time.Minute
}

func (s *ScaleGuard) GetFloor() int32 {
	if s.Spec.Floor != nil && *s.Spec.Floor > 0 {
		return *s.Spec.Floor
	}
	return 1
}

func (s *ScaleGuard) GetWarmupSeconds() int {
	if s.Spec.WarmupSeconds != nil {
		return *s.Spec.WarmupSeconds
	}
	return 30
}

func (s *ScaleGuard) GetMinSamples() int {
	if s.Spec.MinSamples != nil && *s.Spec.MinSamples > 0 {
		return *s.Spec.MinSamples
	}
	return 5
}

func (s *ScaleGuard) GetStep() time.Duration {
	if s.Spec.Step != nil && s.Spec.Step.Duration > 0 {
		return s.Spec.Step.Duration
	}
	return time.Minute
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ScaleGuard is the Schema for the scaleguards API
type ScaleGuard struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ScaleGuard
	// +required
	Spec ScaleGuardSpec `json:"spec"`

	// status defines the observed state of ScaleGuard
	// +optional
	Status ScaleGuardStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ScaleGuardList contains a list of ScaleGuard
type ScaleGuardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ScaleGuard `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ScaleGuard{}, &ScaleGuardList{})
		return nil
	})
}
