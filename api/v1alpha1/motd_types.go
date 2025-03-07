/*
Copyright 2025 TwistedSolutions.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MOTDSpec defines the desired configuration for MOTD.
type MotdSpec struct {
	// Components is the list of live data sources to include in the MOTD.
	Components []Component `json:"components,omitempty"`
	// RefreshInterval specifies how often (in seconds) this component should be updated.
	// +kubebuilder:default=60
	RefreshInterval int `json:"refreshInterval,omitempty"`
}

type StyleType string

const (
	UnderlineStyle StyleType = "underline"
	BoldStyle      StyleType = "bold"
	ItalicStyle    StyleType = "italic"
)

// Component defines a single live data source for the MOTD.
type Component struct {
	// Type is the type of component.
	// Allowed values: weather, nodeStatus, freeMemory.
	// +kubebuilder:validation:Enum=text;divider;weather;nodeStatus;clusterOperatorStatus
	Type string `json:"type"`

	// Style is the style to use for this component.
	// Allowed values: underline, bold, italic. Default is no style.
	Style []StyleType `json:"style,omitempty"`

	// Text is the text to display in the MOTD when type is "text".
	Text string `json:"text,omitempty"`
	// City specifies the city to use for weather data.
	// This field is required if type is "weather".
	City string `json:"city,omitempty"`

	// WeatherAPIKeySecretRef is a reference to a secret that contains the API key for weather.
	// This field is required if type is "weather".
	WeatherAPIKeySecretRef *corev1.SecretKeySelector `json:"weatherAPIKeySecretRef,omitempty"`
}

// MotdStatus defines the observed state of Motd
type MotdStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Motd is the Schema for the motds API
type Motd struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MotdSpec   `json:"spec,omitempty"`
	Status MotdStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MotdList contains a list of Motd
type MotdList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Motd `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Motd{}, &MotdList{})
}
