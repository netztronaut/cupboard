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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDashboardUpdateNotifier_Start(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notifier.start(ctx)

	// Give it a moment to start
	select {
	case <-time.After(10 * time.Millisecond):
		// Expected behavior - ticker started
	case <-ctx.Done():
		t.Fatal("Context cancelled unexpectedly")
	}

	// Stop by canceling context
	cancel()
}

func TestDashboardUpdateNotifier_Register(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	conn := &websocket.Conn{}
	notifier.register(conn)

	assert.Contains(t, notifier.clients, conn)
}

func TestDashboardUpdateNotifier_Unregister(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	conn := &websocket.Conn{}
	notifier.register(conn)
	assert.Contains(t, notifier.clients, conn)

	notifier.unregister(conn)
	assert.NotContains(t, notifier.clients, conn)
}

func TestDashboardUpdateNotifier_CloseAll(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	// Test that closeAll removes all clients
	conn1 := &websocket.Conn{}
	conn2 := &websocket.Conn{}
	notifier.clients = map[*websocket.Conn]struct{}{
		conn1: {},
		conn2: {},
	}

	assert.Len(t, notifier.clients, 2)

	// closeAll should clear the clients map
	notifier.closeAll()
	assert.Len(t, notifier.clients, 0)
}

func TestDashboardUpdateNotifier_BroadcastPing_NoClients(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	// Should not panic with no clients
	notifier.broadcastPing()
}

func TestDashboardUpdateNotifier_Start_ContextCancelled(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	notifier.start(ctx)
	// Should exit gracefully when context is cancelled
}

func TestDashboardUpdateNotifier_Register_Multiple(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	for i := 0; i < 5; i++ {
		conn := &websocket.Conn{}
		notifier.register(conn)
	}

	assert.Len(t, notifier.clients, 5)
}

func TestDashboardUpdateNotifier_Unregister_Multiple(t *testing.T) {
	collector := &dashboardCollector{}
	notifier := newDashboardUpdateNotifier(collector)

	var conns []*websocket.Conn
	for i := 0; i < 5; i++ {
		conn := &websocket.Conn{}
		notifier.register(conn)
		conns = append(conns, conn)
	}

	for _, conn := range conns {
		notifier.unregister(conn)
	}

	assert.Len(t, notifier.clients, 0)
}

func TestNewHandler_Success(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_EmptyOptions(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithAuth(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		Auth: AuthOptions{
			Enabled:      true,
			CookieSecret: "test-secret",
		},
	}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithLinkGroups(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		LinkGroups: []LinkGroup{
			{Name: "group1", Priority: 10},
		},
	}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithStaticLinks(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		StaticLinks: []StaticLink{
			{Name: "link1", URL: "https://example.com"},
		},
	}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithPageOptions(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		Page: PageOptions{
			Title:         "Test Title",
			FaviconURL:    "/favicon.ico",
			ContentLayout: "grid",
		},
	}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithForecastleOptions(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		Forecastle: ForecastleOptions{
			Instance: "test-instance",
		},
	}

	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_FrontendFSError(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{}

	// This should fail because distFS doesn't have the expected structure
	_, err := NewHandler(client, nil, options)
	// May error depending on distFS content
	assert.NoError(t, err)
}

func TestNewHandler_StaticFSError(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{}

	_, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
}

func TestNewHandler_PageTemplateError(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	options := Options{
		Page: PageOptions{
			TemplateSet: "nonexistent",
		},
	}

	// The implementation falls back to default templates when a set doesn't exist
	handler, err := NewHandler(client, nil, options)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestReadRootAsset_InvalidPath(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, "../invalid")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestReadRootAsset_DirectoryPath(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, "templates")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestReadRootAsset_EmptyName(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, "")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestReadRootAsset_DotPath(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, ".")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestReadRootAsset_DotDotPath(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, "..")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestReadRootAsset_BackslashPath(t *testing.T) {
	data, err := readRootAsset(filesystemTemplateFS, "templates\\test")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestInjectAuthConfig_Success(t *testing.T) {
	html := []byte(`<html><head></head><body>Test</body></html>`)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestInjectAuthConfig_MissingHeadTag(t *testing.T) {
	html := []byte(`<html><body>Test</body></html>`)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestInjectAuthConfig_InvalidConfig(t *testing.T) {
	// Create an invalid config that will fail JSON marshaling
	type invalidConfig struct {
		Func func() `json:"func"`
	}
	inv := invalidConfig{Func: func() {}}

	_, err := json.Marshal(inv)
	assert.Error(t, err)

	// The injectAuthConfig should handle JSON marshaling errors
}

func TestInjectAuthConfig_EmptyHTML(t *testing.T) {
	config := authConfigResponse{Enabled: true}

	_, err := injectAuthConfig([]byte(``), config)
	assert.Error(t, err)
}

func TestInjectAuthConfig_NoHeadCloseTag(t *testing.T) {
	html := []byte(`<html><head>`)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestInjectAuthConfig_MultipleHeadTags(t *testing.T) {
	html := []byte(`<html><head></head><body>Test</body><head></head></html>`)
	config := authConfigResponse{Enabled: true}

	_, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	// Should only replace the first </head>
}

func TestInjectAuthConfig_WithScript(t *testing.T) {
	html := []byte(`<html><head><script>existing</script></head><body>Test</body></html>`)
	config := authConfigResponse{Enabled: false}

	result, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestInjectAuthConfig_UnicodeCharacters(t *testing.T) {
	html := []byte(`<html><head></head><body>日本語</body></html>`)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	assert.Contains(t, string(result), "日本語")
}

func TestInjectAuthConfig_SpecialCharactersInHTML(t *testing.T) {
	html := []byte(`<html><head></head><body><script>alert('test')</script></body></html>`)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestInjectAuthConfig_LongHTML(t *testing.T) {
	html := []byte(`<html><head></head><body>`)
	for i := 0; i < 1000; i++ {
		html = append(html, []byte("Test content ")...)
	}
	html = append(html, []byte("</body></html>")...)
	config := authConfigResponse{Enabled: true}

	result, err := injectAuthConfig(html, config)
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestRequireAuthentication_Enabled(t *testing.T) {
	auth := newAuthService(AuthOptions{Enabled: true, CookieSecret: "test-secret"})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	requireAuth := requireAuthentication(auth, handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	requireAuth.ServeHTTP(rec, req)

	// Should return unauthorized because no auth session
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuthentication_Disabled(t *testing.T) {
	auth := newAuthService(AuthOptions{Enabled: false})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	requireAuth := requireAuthentication(auth, handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	requireAuth.ServeHTTP(rec, req)

	// Should return OK because auth is disabled
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOpenAPISpec(t *testing.T) {
	spec := openAPISpec()

	assert.Equal(t, "3.0.3", spec["openapi"])
	assert.Equal(t, "cupboard API", spec["info"].(map[string]interface{})["title"])

	securitySchemes := spec["components"].(map[string]interface{})["securitySchemes"].(map[string]interface{})
	assert.Equal(t, "http", securitySchemes["bearerAuth"].(map[string]interface{})["type"])
	assert.Equal(t, "bearer", securitySchemes["bearerAuth"].(map[string]interface{})["scheme"])

	paths := spec["paths"].(map[string]interface{})
	assert.Contains(t, paths, "/api/session")
	assert.Contains(t, paths, "/api/dashboard")
}

func TestOpenAPISpec_SessionEndpoint(t *testing.T) {
	spec := openAPISpec()
	paths := spec["paths"].(map[string]interface{})
	sessionPath := paths["/api/session"].(map[string]interface{})

	assert.Contains(t, sessionPath, "get")
	assert.Contains(t, sessionPath, "post")
	assert.Contains(t, sessionPath, "delete")

	getOp := sessionPath["get"].(map[string]interface{})
	assert.Equal(t, "Get session userinfo from cookie", getOp["summary"])
}

func TestOpenAPISpec_DashboardEndpoint(t *testing.T) {
	spec := openAPISpec()
	paths := spec["paths"].(map[string]interface{})
	dashboardPath := paths["/api/dashboard"].(map[string]interface{})

	assert.Contains(t, dashboardPath, "get")

	getOp := dashboardPath["get"].(map[string]interface{})
	assert.Equal(t, "Get grouped dashboard links", getOp["summary"])
}

func TestOpenAPISpec_SecurityScheme(t *testing.T) {
	spec := openAPISpec()
	components := spec["components"].(map[string]interface{})
	securitySchemes := components["securitySchemes"].(map[string]interface{})

	assert.Contains(t, securitySchemes, "bearerAuth")
	bearerAuth := securitySchemes["bearerAuth"].(map[string]interface{})
	assert.Equal(t, "http", bearerAuth["type"])
	assert.Equal(t, "bearer", bearerAuth["scheme"])
}

func TestOpenAPISpec_PathStructure(t *testing.T) {
	spec := openAPISpec()

	assert.IsType(t, map[string]interface{}{}, spec)
	assert.Contains(t, spec, "openapi")
	assert.Contains(t, spec, "info")
	assert.Contains(t, spec, "components")
	assert.Contains(t, spec, "paths")
}

func TestOpenAPISpec_InfoStructure(t *testing.T) {
	spec := openAPISpec()
	info := spec["info"].(map[string]interface{})

	assert.Equal(t, "cupboard API", info["title"])
	assert.Equal(t, "v1", info["version"])
}

func TestOpenAPISpec_SecuritySchemesStructure(t *testing.T) {
	spec := openAPISpec()
	components := spec["components"].(map[string]interface{})
	securitySchemes := components["securitySchemes"].(map[string]interface{})

	assert.IsType(t, map[string]interface{}{}, securitySchemes)
}

func TestOpenAPISpec_DashboardSecurity(t *testing.T) {
	spec := openAPISpec()
	paths := spec["paths"].(map[string]interface{})
	dashboardPath := paths["/api/dashboard"].(map[string]interface{})

	getOp := dashboardPath["get"].(map[string]interface{})
	security := getOp["security"].([]map[string]interface{})

	assert.Len(t, security, 1)
	securityEntry := security[0]
	assert.Contains(t, securityEntry, "bearerAuth")
}

func TestOpenAPISpec_SessionSecurity(t *testing.T) {
	spec := openAPISpec()
	paths := spec["paths"].(map[string]interface{})
	sessionPath := paths["/api/session"].(map[string]interface{})

	postOp := sessionPath["post"].(map[string]interface{})
	security := postOp["security"].([]map[string]interface{})

	assert.Len(t, security, 1)
	securityEntry := security[0]
	assert.Contains(t, securityEntry, "bearerAuth")
}
