package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type dashboardUpdateNotifier struct {
	collector *dashboardCollector
	mu        sync.Mutex
	clients   map[*websocket.Conn]struct{}
	upgrader  websocket.Upgrader
}

func newDashboardUpdateNotifier(collector *dashboardCollector) *dashboardUpdateNotifier {
	return &dashboardUpdateNotifier{
		collector: collector,
		clients:   map[*websocket.Conn]struct{}{},
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
}

func (n *dashboardUpdateNotifier) start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastSignature := ""
		for {
			select {
			case <-ctx.Done():
				n.closeAll()
				return
			case <-ticker.C:
				signature := n.collectSignature(ctx)
				if signature == "" || signature == lastSignature {
					continue
				}
				lastSignature = signature
				n.broadcastPing()
			}
		}
	}()
}

func (n *dashboardUpdateNotifier) collectSignature(ctx context.Context) string {
	payload, err := n.collector.collectDashboard(ctx)
	if err != nil {
		return ""
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

func (n *dashboardUpdateNotifier) register(conn *websocket.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clients[conn] = struct{}{}
}

func (n *dashboardUpdateNotifier) unregister(conn *websocket.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.clients, conn)
}

func (n *dashboardUpdateNotifier) closeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for conn := range n.clients {
		_ = conn.Close()
		delete(n.clients, conn)
	}
}

func (n *dashboardUpdateNotifier) broadcastPing() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for conn := range n.clients {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
			_ = conn.Close()
			delete(n.clients, conn)
		}
	}
}

func NewHandler(k8sClient client.Client, discovery dashboardDiscovery, options Options) (http.Handler, error) {
	frontendFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(distFS, "static")
	if err != nil {
		return nil, err
	}
	pageTemplate, err := loadPageTemplate(options.Page.TemplateSet)
	if err != nil {
		return nil, err
	}
	pageTitle := strings.TrimSpace(options.Page.Title)
	if pageTitle == "" {
		pageTitle = "cupboard"
	}
	contentLayout := strings.ToLower(strings.TrimSpace(options.Page.ContentLayout))
	if contentLayout != "grid" {
		contentLayout = "list"
	}
	faviconURL := strings.TrimSpace(options.Page.FaviconURL)
	if faviconURL == "" {
		faviconURL = "/favicon.svg"
	}

	auth := newAuthService(options.Auth)
	collector := newDashboardCollector(k8sClient, discovery, options.LinkGroups, options.StaticLinks)
	collector.logMissingOptionalResources(context.Background())
	notifier := newDashboardUpdateNotifier(collector)
	notifier.start(context.Background())
	fileServer := http.FileServer(http.FS(frontendFS))
	staticServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", staticServer))
	mux.HandleFunc("/api/public/auth-config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !auth.enabled {
			_, _ = w.Write([]byte(`{"enabled":false}`))
			return
		}
		_ = json.NewEncoder(w).Encode(auth.authConfig(r.Context(), requestBaseURL(r)))
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		auth.serveOpenIDConfiguration(w, r)
	})
	mux.HandleFunc("/api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openAPISpec())
	})
	mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>cupboard API docs</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.ui = SwaggerUIBundle({ url: '/api/openapi.json', dom_id: '#swagger-ui' })
    </script>
  </body>
</html>`)
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if !auth.enabled {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"userInfo": map[string]interface{}{"sub": "anonymous"},
			})
			return
		}
		switch r.Method {
		case http.MethodGet:
			userInfo, err := auth.userInfoFromCookie(r)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"userInfo": userInfo})
		case http.MethodPost:
			token := bearerTokenFromRequest(r)
			if token == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			userInfo, err := auth.fetchUserInfo(r.Context(), token)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			auth.setSessionCookie(w, userInfo)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"userInfo": userInfo})
		case http.MethodDelete:
			auth.clearSessionCookie(w)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
	dashboardHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		payload, collectErr := collector.collectDashboard(r.Context())
		if collectErr != nil {
			http.Error(w, collectErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	if auth.enabled {
		mux.Handle("/api/dashboard", requireAuthentication(auth, dashboardHandler))
	} else {
		mux.Handle("/api/dashboard", dashboardHandler)
	}
	mux.HandleFunc("/api/dashboard/updates", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if auth.enabled {
			if _, err := auth.authenticateRequest(r.Context(), r, w); err != nil {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}
		conn, err := notifier.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		notifier.register(conn)
		go func() {
			defer func() {
				notifier.unregister(conn)
				_ = conn.Close()
			}()
			for {
				if _, _, readErr := conn.ReadMessage(); readErr != nil {
					return
				}
			}
		}()
	})

	pageHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderErr := pageTemplate.tmpl.ExecuteTemplate(w, "page", pageTemplateData{
			Title:         pageTitle,
			FaviconURL:    faviconURL,
			ContentLayout: contentLayout,
		})
		if renderErr != nil {
			http.Error(w, renderErr.Error(), http.StatusInternalServerError)
			return
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean(r.URL.Path)
		if cleanPath == "." || cleanPath == "/" {
			if auth.enabled {
				requireAuthentication(auth, pageHandler).ServeHTTP(w, r)
			} else {
				pageHandler.ServeHTTP(w, r)
			}
			return
		}

		assetPath := strings.TrimPrefix(cleanPath, "/")
		if _, statErr := fs.Stat(frontendFS, assetPath); statErr == nil {
			fileServer.ServeHTTP(w, r)
			return
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		fallbackReq := r.Clone(r.Context())
		fallbackReq.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, fallbackReq)
	})

	return mux, nil
}

func requireAuthentication(auth *authService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userInfo, err := auth.authenticateRequest(r.Context(), r, w)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUserInfo(r.Context(), userInfo)))
	})
}

func openAPISpec() map[string]interface{} {
	return map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":   "cupboard API",
			"version": "v1",
		},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{
					"type":   "http",
					"scheme": "bearer",
				},
			},
		},
		"paths": map[string]interface{}{
			"/api/public/auth-config": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "OIDC PKCE config for frontend",
				},
			},
			"/api/session": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Get session userinfo from cookie",
				},
				"post": map[string]interface{}{
					"summary":  "Create backend session from bearer token",
					"security": []map[string]interface{}{{"bearerAuth": []string{}}},
				},
				"delete": map[string]interface{}{
					"summary": "Clear backend session cookie",
				},
			},
			"/api/dashboard": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":  "Get grouped dashboard links",
					"security": []map[string]interface{}{{"bearerAuth": []string{}}},
				},
			},
		},
	}
}
