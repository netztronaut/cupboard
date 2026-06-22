package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)


func NewHandler(k8sClient client.Client, discovery dashboardDiscovery, options Options, notifier *DashboardNotifier) (http.Handler, error) {
	frontendFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(distFS, "static")
	if err != nil {
		return nil, err
	}
	auth := newAuthService(options.Auth)
	collector := newDashboardCollector(k8sClient, discovery, options)
	collector.logMissingOptionalResources(context.Background())
	pageTemplate, err := loadPageTemplate(options.Page.TemplateSet)
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(frontendFS))
	staticServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", staticServer))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		favicon, err := readRootAsset(frontendFS, "favicon.ico")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(favicon)
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
				"groups":   []string{},
			})
			return
		}
		switch r.Method {
		case http.MethodGet:
			session, err := auth.userInfoFromCookie(r)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(session)
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
			session := newAuthSession(userInfo)
			auth.setSessionCookie(w, session)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(session)
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
		var userGroups []string
		if session, ok := authSessionFromContext(r.Context()); ok {
			userGroups = session.Groups
		}
		payload, collectErr := collector.collectDashboard(r.Context(), userGroups)
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
			_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
			conn.SetPongHandler(func(_ string) error {
				return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
			})
			for {
				if _, _, readErr := conn.ReadMessage(); readErr != nil {
					return
				}
			}
		}()
	})

	frontendIndexHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		index, readErr := fs.ReadFile(frontendFS, "index.html")
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}
		index, injectErr := injectAuthConfig(index, auth.authConfig(r.Context(), requestBaseURL(r)))
		if injectErr != nil {
			http.Error(w, injectErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
	pageTemplateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if auth.enabled {
			session, err := auth.userInfoFromCookie(r)
			if err != nil {
				frontendIndexHandler.ServeHTTP(w, r)
				return
			}
			r = r.WithContext(withAuthSession(r.Context(), session))
		}
		var groups []DashboardGroup
		var userGroups []string
		if session, ok := authSessionFromContext(r.Context()); ok {
			userGroups = session.Groups
		}
		if dashboard, collectErr := collector.collectDashboard(r.Context(), userGroups); collectErr == nil {
			groups = dashboard.Groups
		} else {
			setupLog.Error(collectErr, "Could not collect dashboard for template render")
		}
		data := pageTemplateData{
			Title:         firstNonEmptyString(options.Page.Title, "cupboard"),
			FaviconURL:    options.Page.FaviconURL,
			ContentLayout: firstNonEmptyString(options.Page.ContentLayout, "list"),
			Groups:        groups,
		}
		var body bytes.Buffer
		if err := pageTemplate.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body.Bytes())
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean(r.URL.Path)
		if cleanPath == "." || cleanPath == "/" {
			pageTemplateHandler.ServeHTTP(w, r)
			return
		}

		assetPath := strings.TrimPrefix(cleanPath, "/")
		if assetPath == "index.html" {
			frontendIndexHandler.ServeHTTP(w, r)
			return
		}
		if _, statErr := fs.Stat(frontendFS, assetPath); statErr == nil {
			fileServer.ServeHTTP(w, r)
			return
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		frontendIndexHandler.ServeHTTP(w, r)
	})

	return mux, nil
}

func readRootAsset(embeddedFS fs.FS, name string) ([]byte, error) {
	if strings.Contains(name, `/`) || strings.Contains(name, `\`) || name == "." || name == ".." {
		return nil, fs.ErrInvalid
	}
	if contents, err := fs.ReadFile(filesystemTemplateFS, name); err == nil {
		return contents, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return fs.ReadFile(embeddedFS, name)
}

func injectAuthConfig(index []byte, config authConfigResponse) ([]byte, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	injection := []byte("\n    <script>window.config = " + string(configJSON) + ";</script>")
	headClose := []byte("</head>")
	if !bytes.Contains(index, headClose) {
		return nil, errors.New("index.html is missing </head>")
	}
	return bytes.Replace(index, headClose, append(injection, headClose...), 1), nil
}

func requireAuthentication(auth *authService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := auth.authenticateRequest(r.Context(), r, w)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(withAuthSession(r.Context(), session)))
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
