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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBookmarkSpecDefaults(t *testing.T) {
	spec := BookmarkSpec{
		Group: "test-group",
		Name:  "test-bookmark",
		URL:   "https://example.com",
	}

	if spec.Group != "test-group" {
		t.Errorf("expected group 'test-group', got %q", spec.Group)
	}
	if spec.Name != "test-bookmark" {
		t.Errorf("expected name 'test-bookmark', got %q", spec.Name)
	}
	if spec.URL != "https://example.com" {
		t.Errorf("expected URL 'https://example.com', got %q", spec.URL)
	}
}

func TestBookmarkSpecWithOptionalFields(t *testing.T) {
	target := BookmarkLinkTargetBlank
	spec := BookmarkSpec{
		Group:             "test-group",
		Name:              "test-bookmark",
		URL:               "https://example.com",
		Target:            target,
		Icon:              "fa-external-link",
		NetworkRestricted: true,
		Properties: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	if spec.Target != BookmarkLinkTargetBlank {
		t.Errorf("expected target '_blank', got %q", spec.Target)
	}
	if spec.Icon != "fa-external-link" {
		t.Errorf("expected icon 'fa-external-link', got %q", spec.Icon)
	}
	if !spec.NetworkRestricted {
		t.Errorf("expected NetworkRestricted to be true")
	}
	if len(spec.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(spec.Properties))
	}
}

func TestBookmarkStatus(t *testing.T) {
	now := metav1.Now()
	status := BookmarkStatus{
		LastSyncedAt: &now,
		Conditions: []metav1.Condition{
			{
				Type:   "Ready",
				Status: "True",
			},
		},
	}

	if status.LastSyncedAt.IsZero() {
		t.Error("expected LastSyncedAt to be set")
	}
	if len(status.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(status.Conditions))
	}
}

func TestBookmarkLinkTargetConstants(t *testing.T) {
	tests := []struct {
		name     string
		expected BookmarkLinkTarget
	}{
		{"BookmarkLinkTargetSelf", BookmarkLinkTargetSelf},
		{"BookmarkLinkTargetBlank", BookmarkLinkTargetBlank},
		{"BookmarkLinkTargetParent", BookmarkLinkTargetParent},
		{"BookmarkLinkTargetTop", BookmarkLinkTargetTop},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expected == "" {
				t.Errorf("expected %s to be non-empty", tt.name)
			}
		})
	}
}

func TestBookmarkGroupSpec(t *testing.T) {
	spec := BookmarkGroupSpec{
		Properties: map[string]string{
			"team": "platform",
			"env":  "prod",
		},
	}

	if len(spec.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(spec.Properties))
	}
}

func TestBookmarkGroupStatus(t *testing.T) {
	now := metav1.Now()
	status := BookmarkGroupStatus{
		BookmarkCount: 3,
		LastSyncedAt:  &now,
	}

	if status.BookmarkCount != 3 {
		t.Errorf("expected BookmarkCount 3, got %d", status.BookmarkCount)
	}
	if status.LastSyncedAt.IsZero() {
		t.Error("expected LastSyncedAt to be set")
	}
}

func TestBookmarkGroupSpecNilProperties(t *testing.T) {
	spec := BookmarkGroupSpec{}

	if spec.Properties != nil {
		t.Errorf("expected nil properties, got %v", spec.Properties)
	}
}

func TestBookmarkURLSourceWithIngressRef(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress"},
	}

	if source.IngressRef == nil {
		t.Error("expected IngressRef to be set")
	}
	if source.IngressRef.Name != "my-ingress" {
		t.Errorf("expected IngressRef name 'my-ingress', got %q", source.IngressRef.Name)
	}
}

func TestBookmarkURLSourceWithMultipleRefs(t *testing.T) {
	source := &URLSource{
		IngressRef:      &LocalObjectReference{Name: "ingress"},
		RouteRef:        &LocalObjectReference{Name: "route"},
		IngressRouteRef: &LocalObjectReference{Name: "ingressroute"},
		HTTPRouteRef:    &LocalObjectReference{Name: "httproute"},
		ServiceRef:      &LocalObjectReference{Name: "service"},
	}

	if source.IngressRef == nil || source.RouteRef == nil ||
		source.IngressRouteRef == nil || source.HTTPRouteRef == nil || source.ServiceRef == nil {
		t.Error("expected all refs to be set")
	}
}

func TestBookmarkStatusURLReachable(t *testing.T) {
	reachable := true
	now := metav1.Now()
	status := BookmarkStatus{
		URLReachable:   &reachable,
		LastURLCheckAt: &now,
		URLCheckError:  "",
	}

	if status.URLReachable == nil || !*status.URLReachable {
		t.Error("expected URLReachable to be true")
	}
	if status.LastURLCheckAt == nil || status.LastURLCheckAt.IsZero() {
		t.Error("expected LastURLCheckAt to be set")
	}
}

func TestBookmarkStatusURLNotReachable(t *testing.T) {
	reachable := false
	status := BookmarkStatus{
		URLReachable:  &reachable,
		URLCheckError: "connection refused",
	}

	if status.URLReachable == nil || *status.URLReachable {
		t.Error("expected URLReachable to be false")
	}
	if status.URLCheckError == "" {
		t.Error("expected URLCheckError to be set")
	}
}

func TestBookmarkStatusURLReachableNil(t *testing.T) {
	status := BookmarkStatus{}
	if status.URLReachable != nil {
		t.Error("expected URLReachable to be nil (not yet checked)")
	}
}

func TestBookmarkSpecGroups(t *testing.T) {
	spec := BookmarkSpec{
		Group:  "primary-group",
		Name:   "My Bookmark",
		URL:    "https://example.com",
		Groups: []string{"admin", "devops"},
	}

	if len(spec.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(spec.Groups))
	}
	if spec.Group != "primary-group" {
		t.Errorf("expected group 'primary-group', got %q", spec.Group)
	}
}

func TestBookmarkSpecGroupsNil(t *testing.T) {
	spec := BookmarkSpec{
		Group: "test",
		Name:  "My Bookmark",
		URL:   "https://example.com",
	}

	if spec.Groups != nil {
		t.Errorf("expected nil groups, got %v", spec.Groups)
	}
}
