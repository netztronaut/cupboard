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
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// dialTestNotifier starts a test HTTP server that upgrades connections and registers
// them with the notifier. Returns the server and a client-side WS connection.
func dialTestNotifier(t *testing.T, notifier *DashboardNotifier) *websocket.Conn {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		notifier.register(conn)
		go func() {
			defer notifier.unregister(conn)
			for {
				if _, _, readErr := conn.ReadMessage(); readErr != nil {
					return
				}
			}
		}()
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestDashboardNotifier_Start(t *testing.T) {
	notifier := NewDashboardNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = notifier.Start(ctx)
	}()

	select {
	case <-time.After(10 * time.Millisecond):
		// running as expected
	case <-done:
		t.Fatal("Start returned before context was cancelled")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestDashboardNotifier_Notify(t *testing.T) {
	notifier := NewDashboardNotifier()

	// Multiple Notify calls must not block.
	for range 10 {
		notifier.Notify()
	}
}

func TestDashboardNotifier_Register(t *testing.T) {
	notifier := NewDashboardNotifier()
	_ = dialTestNotifier(t, notifier)

	// Give the goroutine time to register.
	time.Sleep(20 * time.Millisecond)
	notifier.mu.Lock()
	count := len(notifier.clients)
	notifier.mu.Unlock()
	assert.Equal(t, 1, count)
}

func TestDashboardNotifier_Unregister(t *testing.T) {
	notifier := NewDashboardNotifier()
	clientConn := dialTestNotifier(t, notifier)

	time.Sleep(20 * time.Millisecond)
	notifier.mu.Lock()
	assert.Equal(t, 1, len(notifier.clients))
	notifier.mu.Unlock()

	_ = clientConn.Close()
	time.Sleep(20 * time.Millisecond)
	notifier.mu.Lock()
	assert.Equal(t, 0, len(notifier.clients))
	notifier.mu.Unlock()
}

func TestDashboardNotifier_CloseAll(t *testing.T) {
	notifier := NewDashboardNotifier()
	_ = dialTestNotifier(t, notifier)
	_ = dialTestNotifier(t, notifier)

	time.Sleep(20 * time.Millisecond)
	notifier.mu.Lock()
	assert.Equal(t, 2, len(notifier.clients))
	notifier.mu.Unlock()

	notifier.closeAll()
	notifier.mu.Lock()
	assert.Equal(t, 0, len(notifier.clients))
	notifier.mu.Unlock()
}

func TestDashboardNotifier_BroadcastPing_NoClients(t *testing.T) {
	notifier := NewDashboardNotifier()
	// Must not panic with no clients.
	notifier.broadcastPing()
}

func TestDashboardNotifier_Start_ContextCancelled(t *testing.T) {
	notifier := NewDashboardNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = notifier.Start(ctx)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after pre-cancelled context")
	}
}

func TestDashboardNotifier_Register_Multiple(t *testing.T) {
	notifier := NewDashboardNotifier()
	for range 5 {
		_ = dialTestNotifier(t, notifier)
	}
	time.Sleep(50 * time.Millisecond)
	notifier.mu.Lock()
	assert.Equal(t, 5, len(notifier.clients))
	notifier.mu.Unlock()
}

func TestDashboardNotifier_NotifyBroadcast(t *testing.T) {
	notifier := NewDashboardNotifier()
	go func() { _ = notifier.Start(t.Context()) }()

	clientConn := dialTestNotifier(t, notifier)
	time.Sleep(20 * time.Millisecond)

	notifier.Notify()

	_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := clientConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "ping", string(msg))
}

func TestNewHandler_Success(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_EmptyOptions(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithAuth(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		Auth: AuthOptions{Enabled: true, CookieSecret: "test-secret"},
	}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithLinkGroups(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		LinkGroups: []LinkGroup{{Name: "group1", Priority: 10}},
	}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithStaticLinks(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		StaticLinks: []StaticLink{{Name: "link1", URL: "https://example.com"}},
	}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithPageOptions(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		Page: PageOptions{Title: "Test Title", FaviconURL: "/favicon.ico", ContentLayout: "grid"},
	}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_WithForecastleOptions(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		Forecastle: ForecastleOptions{Instance: "test-instance"},
	}, NewDashboardNotifier(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestNewHandler_PageTemplateError(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	handler, err := NewHandler(c, nil, Options{
		Page: PageOptions{TemplateSet: "nonexistent"},
	}, NewDashboardNotifier(), nil)
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
	result, err := injectAuthConfig(html, authConfigResponse{Enabled: true})
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestInjectAuthConfig_MissingHeadTag(t *testing.T) {
	html := []byte(`<html><body>Test</body></html>`)
	result, err := injectAuthConfig(html, authConfigResponse{Enabled: true})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestInjectAuthConfig_InvalidConfig(t *testing.T) {
	type invalidConfig struct {
		Func func() `json:"func"`
	}
	_, err := json.Marshal(invalidConfig{Func: func() {}}) //nolint:staticcheck
	assert.Error(t, err)
}

func TestInjectAuthConfig_EmptyHTML(t *testing.T) {
	_, err := injectAuthConfig([]byte(``), authConfigResponse{Enabled: true})
	assert.Error(t, err)
}

func TestInjectAuthConfig_NoHeadCloseTag(t *testing.T) {
	result, err := injectAuthConfig([]byte(`<html><head>`), authConfigResponse{Enabled: true})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestInjectAuthConfig_MultipleHeadTags(t *testing.T) {
	html := []byte(`<html><head></head><body>Test</body><head></head></html>`)
	_, err := injectAuthConfig(html, authConfigResponse{Enabled: true})
	assert.NoError(t, err)
}

func TestInjectAuthConfig_WithScript(t *testing.T) {
	html := []byte(`<html><head><script>existing</script></head><body>Test</body></html>`)
	result, err := injectAuthConfig(html, authConfigResponse{Enabled: false})
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestInjectAuthConfig_UnicodeCharacters(t *testing.T) {
	html := []byte(`<html><head></head><body>日本語</body></html>`)
	result, err := injectAuthConfig(html, authConfigResponse{Enabled: true})
	assert.NoError(t, err)
	assert.Contains(t, string(result), "日本語")
}

func TestInjectAuthConfig_LongHTML(t *testing.T) {
	html := []byte(`<html><head></head><body>`)
	for range 1000 {
		html = append(html, []byte("Test content ")...)
	}
	html = append(html, []byte("</body></html>")...)
	result, err := injectAuthConfig(html, authConfigResponse{Enabled: true})
	assert.NoError(t, err)
	assert.Contains(t, string(result), "window.config")
}

func TestRequireAuthentication_Enabled(t *testing.T) {
	auth := newAuthService(AuthOptions{Enabled: true, CookieSecret: "test-secret"})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	requireAuthentication(auth, handler).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuthentication_Disabled(t *testing.T) {
	auth := newAuthService(AuthOptions{Enabled: false})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	requireAuthentication(auth, handler).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOpenAPISpec(t *testing.T) {
	spec := openAPISpec()
	assert.Equal(t, "3.0.3", spec["openapi"])
	assert.Equal(t, "cupboard API", spec["info"].(map[string]any)["title"])
	securitySchemes := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)
	assert.Equal(t, "http", securitySchemes["bearerAuth"].(map[string]any)["type"])
	assert.Equal(t, "bearer", securitySchemes["bearerAuth"].(map[string]any)["scheme"])
	paths := spec["paths"].(map[string]any)
	assert.Contains(t, paths, "/api/session")
	assert.Contains(t, paths, "/api/dashboard")
}

func TestOpenAPISpec_SessionEndpoint(t *testing.T) {
	spec := openAPISpec()
	sessionPath := spec["paths"].(map[string]any)["/api/session"].(map[string]any)
	assert.Contains(t, sessionPath, "get")
	assert.Contains(t, sessionPath, "post")
	assert.Contains(t, sessionPath, "delete")
	getOp := sessionPath["get"].(map[string]any)
	assert.Equal(t, "Get session userinfo from cookie", getOp["summary"])
}

func TestOpenAPISpec_DashboardEndpoint(t *testing.T) {
	spec := openAPISpec()
	dashboardPath := spec["paths"].(map[string]any)["/api/dashboard"].(map[string]any)
	assert.Contains(t, dashboardPath, "get")
	getOp := dashboardPath["get"].(map[string]any)
	assert.Equal(t, "Get grouped dashboard links", getOp["summary"])
}

func TestOpenAPISpec_SecurityScheme(t *testing.T) {
	spec := openAPISpec()
	securitySchemes := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)
	assert.Contains(t, securitySchemes, "bearerAuth")
	bearerAuth := securitySchemes["bearerAuth"].(map[string]any)
	assert.Equal(t, "http", bearerAuth["type"])
	assert.Equal(t, "bearer", bearerAuth["scheme"])
}

func TestOpenAPISpec_PathStructure(t *testing.T) {
	spec := openAPISpec()
	assert.IsType(t, map[string]any{}, spec)
	assert.Contains(t, spec, "openapi")
	assert.Contains(t, spec, "info")
	assert.Contains(t, spec, "components")
	assert.Contains(t, spec, "paths")
}

func TestOpenAPISpec_InfoStructure(t *testing.T) {
	spec := openAPISpec()
	info := spec["info"].(map[string]any)
	assert.Equal(t, "cupboard API", info["title"])
	assert.Equal(t, "v1", info["version"])
}

func TestOpenAPISpec_SecuritySchemesStructure(t *testing.T) {
	spec := openAPISpec()
	securitySchemes := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)
	assert.IsType(t, map[string]any{}, securitySchemes)
}

func TestOpenAPISpec_DashboardSecurity(t *testing.T) {
	spec := openAPISpec()
	dashboardPath := spec["paths"].(map[string]any)["/api/dashboard"].(map[string]any)
	getOp := dashboardPath["get"].(map[string]any)
	security := getOp["security"].([]map[string]any)
	assert.Len(t, security, 1)
	assert.Contains(t, security[0], "bearerAuth")
}

func TestOpenAPISpec_SessionSecurity(t *testing.T) {
	spec := openAPISpec()
	sessionPath := spec["paths"].(map[string]any)["/api/session"].(map[string]any)
	postOp := sessionPath["post"].(map[string]any)
	security := postOp["security"].([]map[string]any)
	assert.Len(t, security, 1)
	assert.Contains(t, security[0], "bearerAuth")
}
