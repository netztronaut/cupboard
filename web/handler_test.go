package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dashboardv1alpha1 "github.com/netztronaut/cupboard/api/dashboard/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type stubDiscovery map[string]*metav1.APIResourceList

func (s stubDiscovery) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	if list, ok := s[groupVersion]; ok {
		return list, nil
	}
	return &metav1.APIResourceList{}, nil
}

func TestAuthConfigDisabledReturnsExactPayload(t *testing.T) {
	t.Helper()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{Auth: AuthOptions{Enabled: false}})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/auth-config", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := strings.TrimSpace(rr.Body.String()); got != `{"enabled":false}` {
		t.Fatalf("unexpected body: got %q", got)
	}
}

func TestDashboardSkipsMissingHTTPRoute(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	bg := &dashboardv1alpha1.BookmarkGroup{}
	bg.SetGroupVersionKind(dashboardv1alpha1.GroupVersion.WithKind("BookmarkGroup"))
	bg.Name = "bookmarks"
	bg.Namespace = "default"
	bg.Spec.Name = "My Group"
	bg.Spec.Links = []dashboardv1alpha1.BookmarkLink{{
		Name: "Docs",
		URL:  "https://example.invalid",
	}}

	handler, err := NewHandler(
		fake.NewClientBuilder().WithScheme(s).WithObjects(bg).Build(),
		stubDiscovery{
			"gateway.networking.k8s.io/v1": {APIResources: nil},
			"cupboard.netztronaut.de/v1alpha1": {
				APIResources: []metav1.APIResource{{Kind: "BookmarkGroup"}},
			},
		},
		Options{Auth: AuthOptions{Enabled: false}},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload DashboardResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Groups) != 1 || payload.Groups[0].Name != "My Group" || len(payload.Groups[0].Links) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestOIDCDiscoveryProxyRewritesIssuer(t *testing.T) {
	t.Helper()

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":            "https://issuer.example",
			"userinfo_endpoint": "https://issuer.example/userinfo",
		})
	}))
	defer issuer.Close()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{
			Enabled:   true,
			IssuerURL: issuer.URL,
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if got, want := payload["issuer"], server.URL; got != want {
		t.Fatalf("issuer mismatch: got %q want %q", got, want)
	}
}

func TestAuthConfigUsesDirectIssuerWhenServerCannotReachIssuer(t *testing.T) {
	t.Helper()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{
			Enabled:   true,
			IssuerURL: "http://127.0.0.1:1",
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/public/auth-config", nil)
	req.Host = "cupboard.local"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}

	var payload authConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth-config: %v", err)
	}
	if got, want := payload.IssuerURL, "http://127.0.0.1:1"; got != want {
		t.Fatalf("issuerUrl mismatch: got %q want %q", got, want)
	}
	if got, want := payload.OpenIDConfigurationURL, "http://127.0.0.1:1/.well-known/openid-configuration"; got != want {
		t.Fatalf("openidConfigurationUrl mismatch: got %q want %q", got, want)
	}

	discoveryReq := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	discoveryRR := httptest.NewRecorder()
	handler.ServeHTTP(discoveryRR, discoveryReq)
	if discoveryRR.Code != http.StatusNotFound {
		t.Fatalf("unexpected discovery proxy status: got %d body=%s", discoveryRR.Code, discoveryRR.Body.String())
	}
}

func TestDashboardIncludesStaticLinks(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{Enabled: false},
		LinkGroups: []LinkGroup{
			{Name: "pinned", DisplayName: "Pinned", Priority: 10, PriorityClass: "first"},
		},
		StaticLinks: []StaticLink{
			{
				LinkGroup: "pinned",
				Name:      "GitHub",
				URL:       "https://github.com",
				Target:    "_blank",
				Icon:      "fa-github",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload DashboardResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Groups) != 1 || payload.Groups[0].Name != "Pinned" || payload.Groups[0].LinkGroup != "pinned" || len(payload.Groups[0].Links) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Groups[0].Links[0].Source != "static" {
		t.Fatalf("expected static source, got %q", payload.Groups[0].Links[0].Source)
	}
	if len(payload.LinkGroups) != 1 || payload.LinkGroups[0].Name != "pinned" {
		t.Fatalf("expected linkGroups metadata, got %+v", payload.LinkGroups)
	}
}

func TestLinkGroupOrderingByClassPriorityAndDisplayName(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{Enabled: false},
		LinkGroups: []LinkGroup{
			{Name: "z-last", DisplayName: "Z Last", PriorityClass: "last"},
			{Name: "a-first", DisplayName: "A First", PriorityClass: "first"},
			{Name: "b-mid", DisplayName: "B Mid", Priority: 20},
			{Name: "a-mid", DisplayName: "A Mid", Priority: 10},
		},
		StaticLinks: []StaticLink{
			{LinkGroup: "z-last", Name: "one", URL: "https://example.com/1"},
			{LinkGroup: "a-first", Name: "two", URL: "https://example.com/2"},
			{LinkGroup: "b-mid", Name: "three", URL: "https://example.com/3"},
			{LinkGroup: "a-mid", Name: "four", URL: "https://example.com/4"},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload DashboardResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Groups) != 4 {
		t.Fatalf("expected 4 groups, got %+v", payload.Groups)
	}
	got := []string{payload.Groups[0].LinkGroup, payload.Groups[1].LinkGroup, payload.Groups[2].LinkGroup, payload.Groups[3].LinkGroup}
	want := []string{"a-first", "a-mid", "b-mid", "z-last"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("group order mismatch got=%v want=%v", got, want)
		}
	}
}

func TestRootPageUsesConfiguredTemplateOptions(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{Enabled: false},
		Page: PageOptions{
			TemplateSet:   "default",
			Title:         "My Cupboard",
			FaviconURL:    "/custom.ico",
			ContentLayout: "grid",
		},
		StaticLinks: []StaticLink{
			{Group: "Pinned", Name: "Docs", URL: "https://example.com", Icon: "fa-github"},
			{Group: "Pinned", Name: "Search", URL: "https://duckduckgo.com", Icon: "lucide:search"},
			{Group: "Pinned", Name: "Home", URL: "https://example.org", Icon: "hero:home"},
			{Group: "Pinned", Name: "Bell", URL: "https://example.net", Icon: "tabler:bell"},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<title>My Cupboard</title>") {
		t.Fatalf("expected configured title in page: %s", body)
	}
	if !strings.Contains(body, `rel="icon" href="/custom.ico"`) {
		t.Fatalf("expected configured favicon in page: %s", body)
	}
	if !strings.Contains(body, "content--grid") {
		t.Fatalf("expected grid layout class in page: %s", body)
	}
	if !strings.Contains(body, `id="dashboard-groups"`) {
		t.Fatalf("expected client-side dashboard container in page: %s", body)
	}
	if !strings.Contains(body, `fetch("/api/dashboard"`) {
		t.Fatalf("expected client-side dashboard fetch in page: %s", body)
	}
	if !strings.Contains(body, `/api/dashboard/updates`) {
		t.Fatalf("expected websocket updates endpoint in page: %s", body)
	}
	if strings.Contains(body, "cdnjs.cloudflare.com") {
		t.Fatalf("expected no external font-awesome CDN links in page: %s", body)
	}
	if !strings.Contains(body, "/static/fontawesome/css/fontawesome.min.css") {
		t.Fatalf("expected embedded font-awesome css links in page: %s", body)
	}
}

func TestRootPageForecastleTheme(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{Enabled: false},
		Page: PageOptions{
			TemplateSet: "forecastle",
			Title:       "Forecastle",
		},
		LinkGroups: []LinkGroup{{Name: "docs", DisplayName: "Docs"}},
		StaticLinks: []StaticLink{
			{LinkGroup: "docs", Name: "GitHub", URL: "https://github.com", Icon: "fa-github"},
			{LinkGroup: "docs", Name: "Search", URL: "https://duckduckgo.com", Icon: "lucide:search"},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "class=\"fc-header\"") || !strings.Contains(body, "class=\"container fc-groups\"") {
		t.Fatalf("expected forecastle theme classes in page: %s", body)
	}
	if !strings.Contains(body, `fetch("/api/dashboard"`) {
		t.Fatalf("expected client-side dashboard fetch in forecastle page: %s", body)
	}
	if !strings.Contains(body, `/api/dashboard/updates`) {
		t.Fatalf("expected websocket updates endpoint in forecastle page: %s", body)
	}
}
