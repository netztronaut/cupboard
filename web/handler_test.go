package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dashboardv1alpha1 "github.com/netztronaut/cupboard/api/dashboard/v1alpha1"
	forecastlev1alpha1 "github.com/netztronaut/cupboard/api/forecastle/v1alpha1"
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

func TestIndexDocumentInjectsDisabledAuthConfig(t *testing.T) {
	t.Helper()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{Auth: AuthOptions{Enabled: false}})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `<script>window.config = {"enabled":false};</script>`) {
		t.Fatalf("index document does not contain disabled auth config: %s", rr.Body.String())
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

func TestRootDocumentAuthConfigUsesDirectIssuerWhenServerCannotReachIssuer(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "cupboard.local"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}

	payload, err := extractInjectedAuthConfig(rr.Body.String())
	if err != nil {
		t.Fatalf("decode injected auth config: %v", err)
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

func TestInjectAuthConfigRejectsIndexWithoutHead(t *testing.T) {
	t.Helper()

	if _, err := injectAuthConfig([]byte("<html></html>"), authConfigResponse{Enabled: true}); err == nil {
		t.Fatal("expected error for index without </head>")
	}
}

func extractInjectedAuthConfig(document string) (authConfigResponse, error) {
	const prefix = `<script>window.config = `
	start := strings.Index(document, prefix)
	if start == -1 {
		return authConfigResponse{}, errors.New("window.config script not found")
	}
	start += len(prefix)
	end := strings.Index(document[start:], `;</script>`)
	if end == -1 {
		return authConfigResponse{}, errors.New("window.config script terminator not found")
	}
	var payload authConfigResponse
	if err := json.Unmarshal([]byte(document[start:start+end]), &payload); err != nil {
		return authConfigResponse{}, err
	}
	return payload, nil
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

func TestSessionCookieStoresUserGroups(t *testing.T) {
	t.Helper()

	auth := newAuthService(AuthOptions{Enabled: true, CookieSecret: "test-secret"})
	rr := httptest.NewRecorder()
	auth.setSessionCookie(rr, newAuthSession(map[string]interface{}{
		"sub":    "user-1",
		"groups": []interface{}{"devops", "admins", "devops", ""},
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, cookie := range rr.Result().Cookies() {
		req.AddCookie(cookie)
	}

	session, err := auth.userInfoFromCookie(req)
	if err != nil {
		t.Fatalf("userInfoFromCookie() error = %v", err)
	}
	if got, want := session.Groups, []string{"devops", "admins"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("groups mismatch got=%v want=%v", got, want)
	}
}

func TestDashboardFiltersRestrictedLinksBySessionGroups(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}

	bg := &dashboardv1alpha1.BookmarkGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "bookmarks", Namespace: "default"},
		Spec: dashboardv1alpha1.BookmarkGroupSpec{
			Name: "Pinned",
			Links: []dashboardv1alpha1.BookmarkLink{
				{Name: "Allowed CRD", URL: "https://crd.example.com", Groups: []string{"devops"}},
				{Name: "Blocked CRD", URL: "https://blocked-crd.example.com", Groups: []string{"finance"}},
			},
		},
	}
	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).WithObjects(bg).Build(), stubDiscovery{}, Options{
		Auth:       AuthOptions{Enabled: true, CookieSecret: "test-secret"},
		LinkGroups: []LinkGroup{{Name: "Pinned", DisplayName: "Pinned"}},
		StaticLinks: []StaticLink{
			{Group: "Pinned", Name: "Allowed Static", URL: "https://static.example.com", Groups: []string{"devops"}},
			{Group: "Pinned", Name: "Blocked Static", URL: "https://blocked-static.example.com", Groups: []string{"finance"}},
			{Group: "Pinned", Name: "Public Static", URL: "https://public.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	auth := newAuthService(AuthOptions{Enabled: true, CookieSecret: "test-secret"})
	cookieRR := httptest.NewRecorder()
	auth.setSessionCookie(cookieRR, newAuthSession(map[string]interface{}{
		"sub":    "user-1",
		"groups": []interface{}{"devops"},
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	for _, cookie := range cookieRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload DashboardResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Groups) != 1 {
		t.Fatalf("expected one visible group, got %+v", payload.Groups)
	}
	got := map[string]struct{}{}
	for _, link := range payload.Groups[0].Links {
		got[link.Name] = struct{}{}
	}
	for _, name := range []string{"Allowed CRD", "Allowed Static", "Public Static"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("expected visible link %q in %+v", name, payload.Groups[0].Links)
		}
	}
	for _, name := range []string{"Blocked CRD", "Blocked Static"} {
		if _, ok := got[name]; ok {
			t.Fatalf("expected restricted link %q to be hidden in %+v", name, payload.Groups[0].Links)
		}
	}
}

func TestForecastleAppDisplayGroupMergesWithConfiguredLinkGroup(t *testing.T) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := dashboardv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add dashboard scheme: %v", err)
	}
	if err := forecastlev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add forecastle scheme: %v", err)
	}

	app := &forecastlev1alpha1.ForecastleApp{
		ObjectMeta: metav1.ObjectMeta{Name: "quay-io", Namespace: "default"},
		Spec: forecastlev1alpha1.ForecastleAppSpec{
			Name:  "Quay.io",
			Group: "Code & DevOps",
			Icon:  "https://quay.io/static/img/quay_favicon.png",
			URL:   "https://quay.io",
		},
	}
	handler, err := NewHandler(fake.NewClientBuilder().WithScheme(s).WithObjects(app).Build(), stubDiscovery{
		forecastlev1alpha1.GroupVersion.String(): {
			APIResources: []metav1.APIResource{{Kind: "ForecastleApp"}},
		},
	}, Options{
		Auth:       AuthOptions{Enabled: false},
		LinkGroups: []LinkGroup{{Name: "code-devops", DisplayName: "Code & DevOps"}},
		StaticLinks: []StaticLink{
			{LinkGroup: "code-devops", Name: "Docker Hub", URL: "https://hub.docker.com"},
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
	if len(payload.Groups) != 1 {
		t.Fatalf("expected one merged group, got %+v", payload.Groups)
	}
	group := payload.Groups[0]
	if group.Name != "Code & DevOps" || group.LinkGroup != "code-devops" || len(group.Links) != 2 {
		t.Fatalf("unexpected merged group: %+v", group)
	}
	found := false
	for _, link := range group.Links {
		if link.Name == "Quay.io" {
			found = true
			if link.Source != "forecastleapp" {
				t.Fatalf("expected Quay.io source forecastleapp, got %q", link.Source)
			}
		}
	}
	if !found {
		t.Fatalf("expected Quay.io link in merged group: %+v", group.Links)
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

func TestRootPageServesSelectedTemplate(t *testing.T) {
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
	if !strings.Contains(body, "My Cupboard") || !strings.Contains(body, "content--grid") || !strings.Contains(body, "/custom.ico") {
		t.Fatalf("expected selected template page options: %s", body)
	}
	if strings.Contains(body, `<div id="root"></div>`) || strings.Contains(body, `window.config`) {
		t.Fatalf("expected template page instead of SPA index: %s", body)
	}
}

func TestIndexHTMLPathUsesInjectedSPAIndex(t *testing.T) {
	t.Helper()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{Auth: AuthOptions{Enabled: false}})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `<script>window.config = {"enabled":false};</script>`) {
		t.Fatalf("index.html path does not contain injected auth config: %s", rr.Body.String())
	}
}

func TestRootPageUsesTemplateSet(t *testing.T) {
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
	if !strings.Contains(body, `class="fc-header"`) || !strings.Contains(body, `class="container fc-groups"`) {
		t.Fatalf("expected forecastle template page: %s", body)
	}
}

func TestRootPageWithAuthAndNoSessionServesSPAIndex(t *testing.T) {
	t.Helper()

	handler, err := NewHandler(fake.NewClientBuilder().Build(), stubDiscovery{}, Options{
		Auth: AuthOptions{
			Enabled:      true,
			IssuerURL:    "https://issuer.example",
			ClientID:     "cupboard",
			CookieSecret: "test-secret",
		},
		Page: PageOptions{
			TemplateSet: "forecastle",
			Title:       "Forecastle",
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
	if !strings.Contains(body, `<div id="root"></div>`) {
		t.Fatalf("expected SPA root container for auth bootstrap: %s", body)
	}
	if strings.Contains(body, `class="fc-header"`) {
		t.Fatalf("expected SPA index instead of template without an auth session: %s", body)
	}
}
