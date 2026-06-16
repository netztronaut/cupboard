package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "cupboard_userinfo"
	sessionTTL        = 24 * time.Hour
	issuerCheckTTL    = 30 * time.Second
)

type authContextKey struct{}

type sessionPayload struct {
	ExpiresAt int64                  `json:"expiresAt"`
	UserInfo  map[string]interface{} `json:"userInfo"`
}

type authService struct {
	enabled                  bool
	cookieName               string
	secret                   []byte
	client                   *http.Client
	issuerURL                string
	clientID                 string
	redirectPath             string
	scopes                   string
	userInfoEndpointOverride string

	mu                    sync.Mutex
	cachedUserInfoEP      string
	cachedIssuerReachable bool
	cachedIssuerCheckedAt time.Time
	cachedIssuerForURL    string
	cachedDiscoveryForURL string
}

type authConfigResponse struct {
	Enabled                bool   `json:"enabled"`
	IssuerURL              string `json:"issuerUrl"`
	OpenIDConfigurationURL string `json:"openidConfigurationUrl,omitempty"`
	ClientID               string `json:"clientId"`
	RedirectPath           string `json:"redirectPath"`
	Scopes                 string `json:"scopes"`
}

func newAuthService(options AuthOptions) *authService {
	secret := strings.TrimSpace(options.CookieSecret)
	if strings.TrimSpace(secret) == "" {
		secret = "local-dev-cookie-secret-change-me"
	}
	return &authService{
		enabled:                  options.Enabled,
		cookieName:               sessionCookieName,
		secret:                   []byte(secret),
		issuerURL:                strings.TrimSpace(options.IssuerURL),
		clientID:                 strings.TrimSpace(options.ClientID),
		redirectPath:             firstNonEmptyString(options.RedirectPath, "/auth/callback"),
		scopes:                   firstNonEmptyString(options.Scopes, "openid profile email"),
		userInfoEndpointOverride: strings.TrimSpace(options.UserInfoEndpointURL),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (a *authService) authConfig(ctx context.Context, requestIssuerURL string) authConfigResponse {
	issuerURL := a.issuerURL
	discoveryURL := oidcDiscoveryURL(issuerURL)

	resolvedIssuerURL := issuerURL
	resolvedDiscoveryURL := discoveryURL
	if issuerURL != "" && discoveryURL != "" && a.issuerAndDiscoveryReachable(ctx, issuerURL, discoveryURL) {
		resolvedIssuerURL = requestIssuerURL
		resolvedDiscoveryURL = strings.TrimRight(requestIssuerURL, "/") + "/.well-known/openid-configuration"
	}

	return authConfigResponse{
		Enabled:                a.enabled,
		IssuerURL:              resolvedIssuerURL,
		OpenIDConfigurationURL: resolvedDiscoveryURL,
		ClientID:               a.clientID,
		RedirectPath:           a.redirectPath,
		Scopes:                 a.scopes,
	}
}

func (a *authService) serveOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		http.NotFound(w, r)
		return
	}

	issuerURL := a.issuerURL
	if issuerURL == "" {
		http.Error(w, "OIDC_ISSUER_URL must be configured", http.StatusServiceUnavailable)
		return
	}
	discoveryURL := oidcDiscoveryURL(issuerURL)
	if !a.issuerAndDiscoveryReachable(r.Context(), issuerURL, discoveryURL) {
		http.NotFound(w, r)
		return
	}

	targetURL, err := url.Parse(discoveryURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	requestIssuerURL := requestBaseURL(r)
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.URL.Path = targetURL.Path
			req.URL.RawPath = targetURL.RawPath
			req.URL.RawQuery = targetURL.RawQuery
			req.Host = targetURL.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return readErr
			}
			defer resp.Body.Close()

			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				resp.Body = io.NopCloser(bytes.NewReader(body))
				resp.ContentLength = int64(len(body))
				resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
				return nil
			}

			payload["issuer"] = requestIssuerURL
			rewritten, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			resp.Body = io.NopCloser(bytes.NewReader(rewritten))
			resp.ContentLength = int64(len(rewritten))
			resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

func (a *authService) authenticateRequest(ctx context.Context, r *http.Request, w http.ResponseWriter) (map[string]interface{}, error) {
	if !a.enabled {
		return map[string]interface{}{"sub": "anonymous"}, nil
	}
	token := bearerTokenFromRequest(r)
	if token != "" {
		userInfo, err := a.fetchUserInfo(ctx, token)
		if err != nil {
			return nil, err
		}
		a.setSessionCookie(w, userInfo)
		return userInfo, nil
	}
	return a.userInfoFromCookie(r)
}

func (a *authService) userInfoFromCookie(r *http.Request) (map[string]interface{}, error) {
	cookie, err := r.Cookie(a.cookieName)
	if err != nil {
		return nil, err
	}
	payloadJSON, err := verifySignedCookie(cookie.Value, a.secret)
	if err != nil {
		return nil, err
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, err
	}
	if time.Now().Unix() > payload.ExpiresAt {
		return nil, errors.New("session cookie expired")
	}
	return payload.UserInfo, nil
}

func (a *authService) fetchUserInfo(ctx context.Context, token string) (map[string]interface{}, error) {
	userInfoEndpoint, err := a.userInfoEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoEndpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo request failed with %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var userInfo map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}
	return userInfo, nil
}

func (a *authService) userInfoEndpoint(ctx context.Context) (string, error) {
	a.mu.Lock()
	cached := a.cachedUserInfoEP
	a.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	if a.userInfoEndpointOverride != "" {
		a.mu.Lock()
		a.cachedUserInfoEP = a.userInfoEndpointOverride
		a.mu.Unlock()
		return a.userInfoEndpointOverride, nil
	}

	issuerURL := a.issuerURL
	if issuerURL == "" {
		return "", errors.New("OIDC_USERINFO_ENDPOINT or OIDC_ISSUER_URL must be configured")
	}
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, http.NoBody)
	if err != nil {
		return "", err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("oidc discovery failed with %d", resp.StatusCode)
	}
	var discovery struct {
		UserInfoEndpoint string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return "", err
	}
	if strings.TrimSpace(discovery.UserInfoEndpoint) == "" {
		return "", errors.New("userinfo_endpoint missing from OIDC discovery")
	}
	a.mu.Lock()
	a.cachedUserInfoEP = discovery.UserInfoEndpoint
	a.mu.Unlock()
	return discovery.UserInfoEndpoint, nil
}

func (a *authService) setSessionCookie(w http.ResponseWriter, userInfo map[string]interface{}) {
	payload := sessionPayload{
		ExpiresAt: time.Now().Add(sessionTTL).Unix(),
		UserInfo:  userInfo,
	}
	data, _ := json.Marshal(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    signCookie(data, a.secret),
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *authService) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func userInfoFromContext(ctx context.Context) (map[string]interface{}, bool) {
	userInfo, ok := ctx.Value(authContextKey{}).(map[string]interface{})
	return userInfo, ok
}

func withUserInfo(ctx context.Context, userInfo map[string]interface{}) context.Context {
	return context.WithValue(ctx, authContextKey{}, userInfo)
}

func bearerTokenFromRequest(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func signCookie(payload []byte, secret []byte) string {
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payloadEncoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadEncoded + "." + signature
}

func verifySignedCookie(value string, secret []byte) ([]byte, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid cookie format")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(expected, provided) {
		return nil, errors.New("invalid cookie signature")
	}
	return base64.RawURLEncoding.DecodeString(parts[0])
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *authService) issuerAndDiscoveryReachable(ctx context.Context, issuerURL, discoveryURL string) bool {
	now := time.Now()

	a.mu.Lock()
	if a.cachedIssuerForURL == issuerURL &&
		a.cachedDiscoveryForURL == discoveryURL &&
		now.Sub(a.cachedIssuerCheckedAt) <= issuerCheckTTL {
		reachable := a.cachedIssuerReachable
		a.mu.Unlock()
		return reachable
	}
	a.mu.Unlock()

	reachable := isReachable(ctx, a.client, issuerURL) && isReachable(ctx, a.client, discoveryURL)

	a.mu.Lock()
	a.cachedIssuerForURL = issuerURL
	a.cachedDiscoveryForURL = discoveryURL
	a.cachedIssuerReachable = reachable
	a.cachedIssuerCheckedAt = now
	a.mu.Unlock()
	return reachable
}

func isReachable(ctx context.Context, httpClient *http.Client, rawURL string) bool {
	if strings.TrimSpace(rawURL) == "" {
		return false
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest
}

func oidcDiscoveryURL(issuerURL string) string {
	if strings.TrimSpace(issuerURL) == "" {
		return ""
	}
	return strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
}

func requestBaseURL(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if parts := strings.SplitN(scheme, ",", 2); len(parts) > 0 {
		scheme = strings.TrimSpace(parts[0])
	}
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if parts := strings.SplitN(host, ",", 2); len(parts) > 0 {
		host = strings.TrimSpace(parts[0])
	}
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func isAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
