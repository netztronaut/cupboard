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

// BookmarkSpec defines the desired state of Bookmark
type BookmarkSpec struct {
	// Group is the name of the group this bookmark belongs to.
	// The controller will dynamically create groups based on this field.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`

	// Name is the display name of this bookmark.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// URL is the direct target URL for this bookmark.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url,omitempty"`

	// URLFrom resolves URL dynamically from Kubernetes resources.
	// Supports Forecastle-compatible source fields.
	// +optional
	URLFrom *URLSource `json:"urlFrom,omitempty"`

	// Target controls where the URL is opened.
	// Defaults to "_self".
	// +optional
	Target BookmarkLinkTarget `json:"target,omitempty"`

	// Icon identifies an icon for this bookmark.
	// It can be an absolute URL (http/https/data), an icon-set key (e.g. fa-home),
	// a relative URL, or a filename resolved by an asset service.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Icon string `json:"icon,omitempty"`

	// NetworkRestricted indicates this bookmark is only reachable in private networks.
	// Adapted from Forecastle's API for backward-compatibility.
	// +optional
	NetworkRestricted bool `json:"networkRestricted,omitempty"`

	// Replicate controls whether this bookmark's group is included in the
	// synchronization API response served to peer cupboard instances.
	// When true, the controller sets Replicate=true on the managed BookmarkGroup.
	// When false (the default) the group is only visible on the local dashboard.
	// +optional
	Replicate bool `json:"replicate,omitempty"`

	// Properties allows free-form metadata, compatible with Forecastle.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// BookmarkStatus defines the observed state of Bookmark.
type BookmarkStatus struct {
	// LastSyncedAt indicates when the controller last reconciled this object.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// Conditions represent the current state of the Bookmark resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Bookmark is the Schema for the bookmarks API
type Bookmark struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Bookmark
	// +required
	Spec BookmarkSpec `json:"spec"`

	// status defines the observed state of Bookmark
	// +optional
	Status BookmarkStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BookmarkList contains a list of Bookmark
type BookmarkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Bookmark `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bookmark{}, &BookmarkList{})
	SchemeBuilder.Register(&BookmarkGroup{}, &BookmarkGroupList{})
}
