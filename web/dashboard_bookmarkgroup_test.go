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

package web

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, dashboardv1alpha1.AddToScheme(scheme))
	return scheme
}

func TestCollectBookmarkGroups_UsesSpecNameAsDisplayName(t *testing.T) {
	scheme := newTestScheme(t)
	group := &dashboardv1alpha1.BookmarkGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-tools"},
		Spec:       dashboardv1alpha1.BookmarkGroupSpec{Name: "Kubernetes Tools"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(group).Build()

	groups := map[string][]DashboardLink{}
	groupDetails := map[string]DashboardLinkGroup{}
	require.NoError(t, collectBookmarkGroups(context.Background(), c, groups, groupDetails))

	details, ok := groupDetails["k8s-tools"]
	require.True(t, ok)
	assert.Equal(t, "Kubernetes Tools", details.DisplayName)
	assert.Equal(t, "k8s-tools", details.Name)
}

func TestCollectBookmarkGroups_DefaultsDisplayNameToMetadataName(t *testing.T) {
	scheme := newTestScheme(t)
	group := &dashboardv1alpha1.BookmarkGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-tools"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(group).Build()

	groups := map[string][]DashboardLink{}
	groupDetails := map[string]DashboardLinkGroup{}
	require.NoError(t, collectBookmarkGroups(context.Background(), c, groups, groupDetails))

	details, ok := groupDetails["k8s-tools"]
	require.True(t, ok)
	assert.Equal(t, "k8s-tools", details.DisplayName)
}

func TestCollectBookmarkGroups_BlankSpecNameDoesNotOverride(t *testing.T) {
	scheme := newTestScheme(t)
	group := &dashboardv1alpha1.BookmarkGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-tools"},
		Spec:       dashboardv1alpha1.BookmarkGroupSpec{Name: "   "},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(group).Build()

	groups := map[string][]DashboardLink{}
	groupDetails := map[string]DashboardLinkGroup{}
	require.NoError(t, collectBookmarkGroups(context.Background(), c, groups, groupDetails))

	details, ok := groupDetails["k8s-tools"]
	require.True(t, ok)
	assert.Equal(t, "k8s-tools", details.DisplayName)
}
