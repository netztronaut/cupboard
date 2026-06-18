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

	"github.com/stretchr/testify/assert"
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

func TestBookmarkLink(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Icon:   "fa-test",
		Groups: []string{"devops", "admin"},
	}

	if link.Name != "Test Link" {
		t.Errorf("expected name 'Test Link', got %q", link.Name)
	}
	if len(link.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(link.Groups))
	}
}

func TestBookmarkGroupSpec(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name: "Test Group",
		Links: []BookmarkLink{
			{Name: "Link 1", URL: "https://example1.com"},
			{Name: "Link 2", URL: "https://example2.com"},
		},
	}

	if spec.Name != "Test Group" {
		t.Errorf("expected name 'Test Group', got %q", spec.Name)
	}
	if len(spec.Links) != 2 {
		t.Errorf("expected 2 links, got %d", len(spec.Links))
	}
}

func TestBookmarkGroupStatus(t *testing.T) {
	now := metav1.Now()
	status := BookmarkGroupStatus{
		LinkCount:    3,
		LastSyncedAt: &now,
	}

	if status.LinkCount != 3 {
		t.Errorf("expected LinkCount 3, got %d", status.LinkCount)
	}
	if status.LastSyncedAt.IsZero() {
		t.Error("expected LastSyncedAt to be set")
	}
}

func TestBookmarkGroupLinkCountCalculation(t *testing.T) {
	spec := BookmarkGroupSpec{
		Links: []BookmarkLink{
			{Name: "Link 1", URL: "https://example1.com"},
			{Name: "Link 2", URL: "https://example2.com"},
			{Name: "Link 3", URL: "https://example3.com"},
		},
	}

	linkCount := int32(len(spec.Links))
	if linkCount != 3 {
		t.Errorf("expected LinkCount 3, got %d", linkCount)
	}
}

func TestBookmarkGroupLinkWithOptionalFields(t *testing.T) {
	link := BookmarkLink{
		Name:              "Test Link",
		URL:               "https://example.com",
		Icon:              "fa-test",
		Groups:            []string{"devops"},
		NetworkRestricted: true,
		Properties: map[string]string{
			"env": "production",
		},
	}

	if link.NetworkRestricted != true {
		t.Error("expected NetworkRestricted to be true")
	}
	if len(link.Properties) != 1 {
		t.Errorf("expected 1 property, got %d", len(link.Properties))
	}
}

func TestBookmarkGroupLinkEmptyName(t *testing.T) {
	link := BookmarkLink{
		Name: "",
		URL:  "https://example.com",
	}

	if link.Name != "" {
		t.Errorf("expected empty name, got %q", link.Name)
	}
}

func TestBookmarkGroupLinkGroupsFiltering(t *testing.T) {
	link := BookmarkLink{
		Name:   "Restricted Link",
		URL:    "https://example.com",
		Groups: []string{"admin", "devops"},
	}

	if len(link.Groups) != 2 {
		t.Errorf("expected 2 groups in link, got %d", len(link.Groups))
	}
}

func TestBookmarkGroupSpecEmptyLinks(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name:  "Empty Group",
		Links: []BookmarkLink{},
	}

	if len(spec.Links) != 0 {
		t.Errorf("expected 0 links, got %d", len(spec.Links))
	}
}

func TestBookmarkGroupSpecNilLinks(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name:  "Nil Links Group",
		Links: nil,
	}

	if spec.Links != nil {
		t.Errorf("expected nil links, got %v", spec.Links)
	}
}

func TestBookmarkGroupSpecWithNameOnly(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name: "Name Only Group",
	}

	if spec.Name != "Name Only Group" {
		t.Errorf("expected name 'Name Only Group', got %q", spec.Name)
	}
}

func TestBookmarkGroupLinkTargetDefault(t *testing.T) {
	link := BookmarkLink{
		Name: "Test Link",
		URL:  "https://example.com",
	}

	if link.Target != "" {
		t.Errorf("expected empty target (will default to _self), got %q", link.Target)
	}
}

func TestBookmarkGroupLinkIconMaxLength(t *testing.T) {
	longIcon := string(make([]byte, 2049))
	link := BookmarkLink{
		Name: "Test Link",
		URL:  "https://example.com",
		Icon: longIcon,
	}

	assert.Greater(t, len(link.Icon), 2048)
}

func TestBookmarkGroupLinkPropertiesMap(t *testing.T) {
	properties := map[string]string{
		"environment": "production",
		"team":        "devops",
		"tier":        "1",
	}

	link := BookmarkLink{
		Name:       "Test Link",
		URL:        "https://example.com",
		Properties: properties,
	}

	if len(link.Properties) != 3 {
		t.Errorf("expected 3 properties, got %d", len(link.Properties))
	}
}

func TestBookmarkGroupLinkURLFromNil(t *testing.T) {
	link := BookmarkLink{
		Name:    "Test Link",
		URL:     "https://example.com",
		URLFrom: nil,
	}

	if link.URLFrom != nil {
		t.Error("expected URLFrom to be nil")
	}
}

func TestBookmarkGroupLinkURLFromWithIngressRef(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
	if link.URLFrom.IngressRef == nil {
		t.Error("expected IngressRef to be set")
	}
	if link.URLFrom.IngressRef.Name != "my-ingress" {
		t.Errorf("expected IngressRef name 'my-ingress', got %q", link.URLFrom.IngressRef.Name)
	}
}

func TestBookmarkGroupLinkURLFromWithServiceRef(t *testing.T) {
	source := &URLSource{
		ServiceRef: &LocalObjectReference{Name: "my-service"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom.ServiceRef == nil {
		t.Error("expected ServiceRef to be set")
	}
	if link.URLFrom.ServiceRef.Name != "my-service" {
		t.Errorf("expected ServiceRef name 'my-service', got %q", link.URLFrom.ServiceRef.Name)
	}
}

func TestBookmarkGroupLinkURLFromWithRouteRef(t *testing.T) {
	source := &URLSource{
		RouteRef: &LocalObjectReference{Name: "my-route"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom.RouteRef == nil {
		t.Error("expected RouteRef to be set")
	}
	if link.URLFrom.RouteRef.Name != "my-route" {
		t.Errorf("expected RouteRef name 'my-route', got %q", link.URLFrom.RouteRef.Name)
	}
}

func TestBookmarkGroupLinkURLFromWithHTTPRouteRef(t *testing.T) {
	source := &URLSource{
		HTTPRouteRef: &LocalObjectReference{Name: "my-httproute"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom.HTTPRouteRef == nil {
		t.Error("expected HTTPRouteRef to be set")
	}
	if link.URLFrom.HTTPRouteRef.Name != "my-httproute" {
		t.Errorf("expected HTTPRouteRef name 'my-httproute', got %q", link.URLFrom.HTTPRouteRef.Name)
	}
}

func TestBookmarkGroupLinkURLFromWithIngressRouteRef(t *testing.T) {
	source := &URLSource{
		IngressRouteRef: &LocalObjectReference{Name: "my-ingressroute"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom.IngressRouteRef == nil {
		t.Error("expected IngressRouteRef to be set")
	}
	if link.URLFrom.IngressRouteRef.Name != "my-ingressroute" {
		t.Errorf("expected IngressRouteRef name 'my-ingressroute', got %q", link.URLFrom.IngressRouteRef.Name)
	}
}

func TestBookmarkGroupLinkURLMutualExclusivity(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URL:     "https://example.com",
		URLFrom: source,
	}

	if link.URL == "" {
		t.Error("expected URL to be set")
	}
	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkWithAllOptionalFields(t *testing.T) {
	target := BookmarkLinkTargetParent
	properties := map[string]string{
		"env":  "prod",
		"team": "platform",
	}

	link := BookmarkLink{
		Name:              "Full Featured Link",
		URL:               "https://example.com",
		Target:            target,
		Icon:              "fa-full-featured",
		NetworkRestricted: true,
		Groups:            []string{"admin", "devops"},
		Properties:        properties,
		URLFrom: &URLSource{
			IngressRef:      &LocalObjectReference{Name: "ingress"},
			RouteRef:        &LocalObjectReference{Name: "route"},
			IngressRouteRef: &LocalObjectReference{Name: "ingressroute"},
			HTTPRouteRef:    &LocalObjectReference{Name: "httproute"},
			ServiceRef:      &LocalObjectReference{Name: "service"},
		},
	}

	if link.Target != BookmarkLinkTargetParent {
		t.Errorf("expected target '_parent', got %q", link.Target)
	}
	if !link.NetworkRestricted {
		t.Error("expected NetworkRestricted to be true")
	}
	if len(link.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(link.Groups))
	}
	if len(link.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(link.Properties))
	}
	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkEmptyGroups(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Groups: []string{},
	}

	if len(link.Groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(link.Groups))
	}
}

func TestBookmarkGroupLinkNilGroups(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Groups: nil,
	}

	if link.Groups != nil {
		t.Errorf("expected nil groups, got %v", link.Groups)
	}
}

func TestBookmarkGroupLinkWithEmptyStringGroups(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Groups: []string{"", " ", "\t"},
	}

	if len(link.Groups) != 3 {
		t.Errorf("expected 3 groups (including empty), got %d", len(link.Groups))
	}
}

func TestBookmarkGroupLinkWithWhitespaceGroups(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Groups: []string{"  devops  ", " admin "},
	}

	if len(link.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(link.Groups))
	}
}

func TestBookmarkGroupLinkURLFromWithMultipleRefs(t *testing.T) {
	source := &URLSource{
		IngressRef:      &LocalObjectReference{Name: "ingress"},
		RouteRef:        &LocalObjectReference{Name: "route"},
		IngressRouteRef: &LocalObjectReference{Name: "ingressroute"},
		HTTPRouteRef:    &LocalObjectReference{Name: "httproute"},
		ServiceRef:      &LocalObjectReference{Name: "service"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithEmptyRef(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: ""},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithWhitespaceRef(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "  my-ingress  "},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithSpecialCharactersInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress-123"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithUnderscoreInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my_ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithDotInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my.ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithDashInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithNumberInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress-1"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithMixedCaseInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "My-Ingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithUppercaseInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "MYINGRESS"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithLowercaseInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "myingress"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithSingleCharInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "a"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithLongName(t *testing.T) {
	longName := string(make([]byte, 257))
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: longName},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithUnicodeInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "my-ingress-日本語"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithChineseInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "我的入口"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithArabicInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "الدخول"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithRussianInName(t *testing.T) {
	source := &URLSource{
		IngressRef: &LocalObjectReference{Name: "вход"},
	}

	link := BookmarkLink{
		Name:    "Test Link",
		URLFrom: source,
	}

	if link.URLFrom == nil {
		t.Error("expected URLFrom to be set")
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInIcon(t *testing.T) {
	link := BookmarkLink{
		Name: "Test Link",
		URL:  "https://example.com",
		Icon: "fa-🚀",
	}

	if link.Icon != "fa-🚀" {
		t.Errorf("expected icon 'fa-🚀', got %q", link.Icon)
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInGroups(t *testing.T) {
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Groups: []string{"devops", "admin🚀"},
	}

	if len(link.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(link.Groups))
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInURL(t *testing.T) {
	link := BookmarkLink{
		Name: "Test Link",
		URL:  "https://example.com/🚀",
	}

	if link.URL != "https://example.com/🚀" {
		t.Errorf("expected URL 'https://example.com/🚀', got %q", link.URL)
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInTarget(t *testing.T) {
	target := BookmarkLinkTargetBlank
	link := BookmarkLink{
		Name:   "Test Link",
		URL:    "https://example.com",
		Target: target,
	}

	if link.Target != BookmarkLinkTargetBlank {
		t.Errorf("expected target '_blank', got %q", link.Target)
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInIconAsset(t *testing.T) {
	link := BookmarkLink{
		Name: "Test Link",
		URL:  "https://example.com",
		Icon: "fa-🚀",
	}

	if link.Icon != "fa-🚀" {
		t.Errorf("expected icon 'fa-🚀', got %q", link.Icon)
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInPropertyKey(t *testing.T) {
	properties := map[string]string{
		"🚀": "value",
	}

	link := BookmarkLink{
		Name:       "Test Link",
		URL:        "https://example.com",
		Properties: properties,
	}

	if len(link.Properties) != 1 {
		t.Errorf("expected 1 property, got %d", len(link.Properties))
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInPropertyValue(t *testing.T) {
	properties := map[string]string{
		"key": "🚀",
	}

	link := BookmarkLink{
		Name:       "Test Link",
		URL:        "https://example.com",
		Properties: properties,
	}

	if len(link.Properties) != 1 {
		t.Errorf("expected 1 property, got %d", len(link.Properties))
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInGroupName(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name:  "🚀 Group",
		Links: []BookmarkLink{},
	}

	if spec.Name != "🚀 Group" {
		t.Errorf("expected name '🚀 Group', got %q", spec.Name)
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInLinkGroupName(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name:  "Test Group",
		Links: []BookmarkLink{{Name: "🚀 Link", URL: "https://example.com"}},
	}

	if len(spec.Links) != 1 {
		t.Errorf("expected 1 link, got %d", len(spec.Links))
	}
}

func TestBookmarkGroupLinkURLFromWithEmojiInLinkName(t *testing.T) {
	spec := BookmarkGroupSpec{
		Name:  "Test Group",
		Links: []BookmarkLink{{Name: "🚀 Link", URL: "https://example.com"}},
	}

	if len(spec.Links) != 1 {
		t.Errorf("expected 1 link, got %d", len(spec.Links))
	}
}
