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

// BookmarkGroupSpec defines the desired state of BookmarkGroup
type BookmarkGroupSpec struct {
	// Name is the display name for this group in the dashboard.
	// When omitted, metadata.name is used.
	// +optional
	Name string `json:"name,omitempty"`

	// Properties allows free-form metadata for this group, compatible with Forecastle.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`

	// Replicate controls whether this group and its bookmarks are included in the
	// synchronization API response served to peer cupboard instances.
	// When false (the default) the group is only visible on the local dashboard.
	// +optional
	Replicate bool `json:"replicate,omitempty"`
}

// BookmarkGroupStatus defines the observed state of BookmarkGroup.
type BookmarkGroupStatus struct {
	// BookmarkCount is the number of Bookmark resources belonging to this group.
	// +optional
	BookmarkCount int32 `json:"bookmarkCount,omitempty"`

	// LastSyncedAt indicates when the controller last reconciled this object.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// Conditions represent the current state of the BookmarkGroup resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// BookmarkGroup is the Schema for the bookmarkgroups API
type BookmarkGroup struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of BookmarkGroup
	// +required
	Spec BookmarkGroupSpec `json:"spec"`

	// status defines the observed state of BookmarkGroup
	// +optional
	Status BookmarkGroupStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BookmarkGroupList contains a list of BookmarkGroup
type BookmarkGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BookmarkGroup `json:"items"`
}
