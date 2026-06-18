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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dashboardv1alpha1 "github.com/netztronaut/cupboard/api/dashboard/v1alpha1"
)

func TestResolveURLFromSource(t *testing.T) {
	tests := []struct {
		name          string
		source        *dashboardv1alpha1.URLSource
		wantErr       bool
		expectedError string
	}{
		{
			name:    "nil source",
			source:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := ResolveURLFromSource(context.Background(), client, "default", tt.source)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveIngressURL(t *testing.T) {
	tests := []struct {
		name          string
		ingressName   string
		wantErr       bool
		expectedError string
	}{
		{
			name:        "ingress without host",
			ingressName: "test-ingress-no-host",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := resolveIngressURL(context.Background(), client, "default", tt.ingressName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveServiceURL(t *testing.T) {
	tests := []struct {
		name          string
		serviceName   string
		wantErr       bool
		expectedError string
	}{
		{
			name:        "service not found",
			serviceName: "test-service-notfound",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := resolveServiceURL(context.Background(), client, "default", tt.serviceName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveHTTPRouteURL(t *testing.T) {
	tests := []struct {
		name          string
		routeName     string
		wantErr       bool
		expectedError string
	}{
		{
			name:      "HTTPRoute not found",
			routeName: "test-route-notfound",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := resolveHTTPRouteURL(context.Background(), client, "default", tt.routeName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveOpenShiftRouteURL(t *testing.T) {
	tests := []struct {
		name          string
		routeName     string
		wantErr       bool
		expectedError string
	}{
		{
			name:      "Route not found",
			routeName: "test-route-notfound",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := resolveOpenShiftRouteURL(context.Background(), client, "default", tt.routeName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveTraefikIngressRouteURL(t *testing.T) {
	tests := []struct {
		name          string
		routeName     string
		wantErr       bool
		expectedError string
	}{
		{
			name:      "IngressRoute not found",
			routeName: "test-route-notfound",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			_, err := resolveTraefikIngressRouteURL(context.Background(), client, "default", tt.routeName)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
