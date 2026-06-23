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

// InfoTileSpec defines the desired state of InfoTile
type InfoTileSpec struct {
	// Name is the display name of this tile on the dashboard.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Group is the dashboard group this tile belongs to.
	// When omitted, the tile name is used as its own group.
	// +optional
	Group string `json:"group,omitempty"`

	// Icon identifies an icon for this tile.
	// It can be a Font Awesome key (fa-*), an icon-set key (lucide:*, tabler:*, hero:*), or a URL.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Icon string `json:"icon,omitempty"`

	// URL is an optional target URL opened when the user clicks the tile.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url,omitempty"`

	// Target controls where the URL is opened. Defaults to "_self".
	// +optional
	Target string `json:"target,omitempty"`

	// Source is the raw HTML injected into the source badge area.
	// When set it replaces the hardcoded source badge.
	// The value is treated as safe HTML — only supply trusted content.
	// +optional
	Source string `json:"source,omitempty"`

	// Interval controls how often the controller re-fetches httpRequest and re-renders the tile.
	// Defaults to 60s.
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// Replicate controls whether this tile is included in the synchronisation API.
	// +optional
	Replicate bool `json:"replicate,omitempty"`

	// HTTPRequest describes how the controller should query a backend endpoint.
	// When omitted, no HTTP fetch is performed and Processor may still be evaluated
	// with an empty Response.
	// +optional
	HTTPRequest *InfoTileHTTPRequest `json:"httpRequest,omitempty"`

	// Processor is a Helm-compatible (Sprig) template string evaluated after every fetch.
	// The output is stored in status.content and rendered as raw HTML on the dashboard.
	// Context variables:
	//   .Response.Code  — HTTP status code (int)
	//   .Response.Body  — raw response body (string)
	//   .Response.Data  — decoded JSON value when Content-Type is application/json, else nil
	// +optional
	Processor string `json:"processor,omitempty"`
}

// InfoTileHTTPRequest describes a single HTTP request made by the InfoTile controller.
type InfoTileHTTPRequest struct {
	// Method is the HTTP method. Defaults to GET.
	// +optional
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE;HEAD
	Method string `json:"method,omitempty"`

	// URL is the endpoint to call.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// QueryParams are appended to the URL as query parameters.
	// +optional
	QueryParams []InfoTileQueryParam `json:"queryParams,omitempty"`

	// Headers are added to the HTTP request.
	// +optional
	Headers []InfoTileHeader `json:"headers,omitempty"`

	// Body is the request body sent for POST/PUT/PATCH requests.
	// +optional
	Body string `json:"body,omitempty"`
}

// InfoTileQueryParam is a single query-string parameter.
type InfoTileQueryParam struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +optional
	Value string `json:"value,omitempty"`
}

// InfoTileHeader is a single HTTP request header.
type InfoTileHeader struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +optional
	Value string `json:"value,omitempty"`
}

// InfoTileStatus defines the observed state of InfoTile
type InfoTileStatus struct {
	// Content is the rendered HTML output of the Processor template.
	// It is injected verbatim into the dashboard tile body.
	// +optional
	Content string `json:"content,omitempty"`

	// LastFetchedAt is the timestamp of the most recent successful fetch-and-render cycle.
	// +optional
	LastFetchedAt *metav1.Time `json:"lastFetchedAt,omitempty"`

	// LastError describes the most recent reconciliation error, if any.
	// Cleared on the next successful cycle.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// InfoTile is the Schema for the infotiles API
type InfoTile struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of InfoTile
	// +required
	Spec InfoTileSpec `json:"spec"`

	// status defines the observed state of InfoTile
	// +optional
	Status InfoTileStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// InfoTileList contains a list of InfoTile
type InfoTileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []InfoTile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InfoTile{}, &InfoTileList{})
}
