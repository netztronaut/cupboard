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

	// Links are the dashboard entries shown under this group.
	// +kubebuilder:validation:MinItems=1
	Links []BookmarkLink `json:"links"`
}

// BookmarkGroupStatus defines the observed state of BookmarkGroup.
type BookmarkGroupStatus struct {
	// LinkCount is the number of links present in spec.links.
	// +optional
	LinkCount int32 `json:"linkCount,omitempty"`

	// LastSyncedAt indicates when the controller last reconciled this object.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// Conditions represent the current state of the BookmarkGroup resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// BookmarkLinkTarget defines where links should open.
// +kubebuilder:validation:Enum=_self;_blank;_parent;_top
type BookmarkLinkTarget string

const (
	BookmarkLinkTargetSelf   BookmarkLinkTarget = "_self"
	BookmarkLinkTargetBlank  BookmarkLinkTarget = "_blank"
	BookmarkLinkTargetParent BookmarkLinkTarget = "_parent"
	BookmarkLinkTargetTop    BookmarkLinkTarget = "_top"
)

// BookmarkLink describes a link entry rendered in the dashboard.
type BookmarkLink struct {
	// Name is the display name of this link.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// URL is the direct target URL for this link.
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

	// Icon identifies an icon for this link.
	// It can be an absolute URL (http/https/data), an icon-set key (e.g. fa-home),
	// a relative URL, or a filename resolved by an asset service.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Icon string `json:"icon,omitempty"`

	// NetworkRestricted indicates this link is only reachable in private networks.
	// Adapted from Forecastle's API for backward-compatibility.
	// +optional
	NetworkRestricted bool `json:"networkRestricted,omitempty"`

	// Properties allows free-form metadata, compatible with Forecastle.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// URLSource represents the set of resources to fetch the URL from.
// It is compatible with Forecastle URL source fields and extended with serviceRef.
type URLSource struct {
	// +optional
	IngressRef *LocalObjectReference `json:"ingressRef,omitempty"`
	// +optional
	RouteRef *LocalObjectReference `json:"routeRef,omitempty"`
	// +optional
	IngressRouteRef *LocalObjectReference `json:"ingressRouteRef,omitempty"`
	// +optional
	HTTPRouteRef *LocalObjectReference `json:"httpRouteRef,omitempty"`
	// +optional
	ServiceRef *LocalObjectReference `json:"serviceRef,omitempty"`
}

// LocalObjectReference contains enough information to locate an object in the same namespace.
type LocalObjectReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
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

func init() {
	SchemeBuilder.Register(&BookmarkGroup{}, &BookmarkGroupList{})
}
