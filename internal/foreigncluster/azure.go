package foreigncluster

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// azureWITokenSource exchanges a Kubernetes projected service account token for
// an Azure AD access token using the workload identity federation flow.
//
// It reads AZURE_CLIENT_ID, AZURE_TENANT_ID, and AZURE_FEDERATED_TOKEN_FILE from
// the environment. These are injected by the Azure Workload Identity webhook.
type azureWITokenSource struct {
	tenantID   string
	clientID   string
	tokenFile  string
	scope      string
	httpClient *http.Client

	mu     sync.Mutex
	cached *azureCachedToken
}

type azureCachedToken struct {
	value     string
	expiresAt time.Time
}

func newAzureWITokenSource(scope string, httpClient *http.Client) (*azureWITokenSource, error) {
	tenantID := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	tokenFile := os.Getenv("AZURE_FEDERATED_TOKEN_FILE")
	if tenantID == "" {
		return nil, fmt.Errorf("AZURE_TENANT_ID environment variable not set")
	}
	if clientID == "" {
		return nil, fmt.Errorf("AZURE_CLIENT_ID environment variable not set")
	}
	if tokenFile == "" {
		return nil, fmt.Errorf("AZURE_FEDERATED_TOKEN_FILE environment variable not set")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &azureWITokenSource{
		tenantID:   tenantID,
		clientID:   clientID,
		tokenFile:  tokenFile,
		scope:      scope,
		httpClient: httpClient,
	}, nil
}

// Token returns a valid Azure AD Bearer token, refreshing if necessary.
// The federated token file is re-read on each refresh because Kubernetes rotates it.
func (s *azureWITokenSource) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Now().Before(s.cached.expiresAt.Add(-30*time.Second)) {
		return s.cached.value, nil
	}
	token, expiry, err := s.exchangeToken()
	if err != nil {
		return "", err
	}
	s.cached = &azureCachedToken{value: token, expiresAt: expiry}
	return token, nil
}

func (s *azureWITokenSource) exchangeToken() (string, time.Time, error) {
	federatedToken, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading federated token from %s: %w", s.tokenFile, err)
	}

	tokenEndpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", s.tenantID)
	body := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {s.clientID},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {strings.TrimSpace(string(federatedToken))},
		"scope":                 {s.scope},
	}

	resp, err := s.httpClient.PostForm(tokenEndpoint, body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("Azure token request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading Azure token response: %w", err)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parsing Azure token response: %w", err)
	}
	if result.Error != "" {
		return "", time.Time{}, fmt.Errorf("Azure token error %s: %s", result.Error, result.Description)
	}
	if result.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("Azure token response contained no access_token")
	}
	expiresIn := result.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiry := time.Now().Add(time.Duration(expiresIn) * time.Second)
	return result.AccessToken, expiry, nil
}
