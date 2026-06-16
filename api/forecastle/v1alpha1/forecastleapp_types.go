/*
Copyright 2026 steigr <me@stei.gr>.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ForecastleAppSpec defines the desired state of ForecastleApp.
type ForecastleAppSpec struct {
	Name     string `json:"name"`
	Instance string `json:"instance,omitempty"`
	Group    string `json:"group"`
	Icon     string `json:"icon"`
	URL      string `json:"url,omitempty"`
	// +optional
	URLFrom *URLSource `json:"urlFrom,omitempty"`
	// +optional
	NetworkRestricted bool `json:"networkRestricted,omitempty"`
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// URLSource represents the set of resources to fetch the URL from.
type URLSource struct {
	// +optional
	IngressRef *LocalObjectReference `json:"ingressRef,omitempty"`
	// +optional
	RouteRef *LocalObjectReference `json:"routeRef,omitempty"`
	// +optional
	IngressRouteRef *LocalObjectReference `json:"ingressRouteRef,omitempty"`
	// +optional
	HTTPRouteRef *LocalObjectReference `json:"httpRouteRef,omitempty"`
}

// LocalObjectReference contains enough information to let you locate
// the referenced object inside the same namespace.
type LocalObjectReference struct {
	Name string `json:"name"`
}

// ForecastleAppStatus defines the observed state of ForecastleApp.
type ForecastleAppStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ForecastleApp is the Schema for the forecastleapps API.
type ForecastleApp struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec   ForecastleAppSpec   `json:"spec,omitempty"`
	Status ForecastleAppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ForecastleAppList contains a list of ForecastleApp.
type ForecastleAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ForecastleApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ForecastleApp{}, &ForecastleAppList{})
}
