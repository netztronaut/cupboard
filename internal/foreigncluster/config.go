package foreigncluster

import (
	"fmt"
	"strings"
)

const (
	// authAzureWorkloadIdentity authenticates using the Azure Workload Identity
	// projected service account token. Requires AZURE_CLIENT_ID, AZURE_TENANT_ID,
	// and AZURE_FEDERATED_TOKEN_FILE to be present in the environment (set by the
	// Azure Workload Identity mutating webhook).
	authAzureWorkloadIdentity = "azure-workload-identity"

	// defaultAKSServerAppID is the well-known AAD server application ID for AKS
	// in Azure public cloud. Used as the OAuth2 scope when authenticating.
	defaultAKSServerAppID = "6dae42f8-4368-4678-94ff-3960e28e3630"
)

// ClusterConfig describes a single foreign cluster to connect to.
type ClusterConfig struct {
	// Endpoint is the Kubernetes API server URL, e.g. "https://aks-prod.example.com:443".
	Endpoint string

	// authMethod is either authAzureWorkloadIdentity or a kubeconfig context name.
	authMethod string

	// azureScope is the AAD OAuth2 scope used when authMethod is azure-workload-identity.
	// Defaults to the well-known AKS server app ID scope.
	azureScope string
}

func (c ClusterConfig) isAzureWorkloadIdentity() bool {
	return c.authMethod == authAzureWorkloadIdentity
}

func (c ClusterConfig) kubeconfigContext() string {
	if c.isAzureWorkloadIdentity() {
		return ""
	}
	return c.authMethod
}

// ParseClusterList parses a list of "endpoint=auth-method" strings.
//
// Auth method is either:
//   - "azure-workload-identity" — use Azure Workload Identity
//   - "azure-workload-identity:SCOPE" — same but with a custom AAD scope
//   - anything else — treated as a kubeconfig context name
func ParseClusterList(entries []string) ([]ClusterConfig, error) {
	out := make([]ClusterConfig, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		ep, auth, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(ep) == "" || strings.TrimSpace(auth) == "" {
			return nil, fmt.Errorf("invalid cluster entry %q: expected format endpoint=auth-method", entry)
		}
		cfg := ClusterConfig{
			Endpoint:   strings.TrimSpace(ep),
			authMethod: strings.TrimSpace(auth),
		}
		if base, scope, hasSep := strings.Cut(cfg.authMethod, ":"); hasSep && base == authAzureWorkloadIdentity {
			cfg.authMethod = authAzureWorkloadIdentity
			cfg.azureScope = strings.TrimSpace(scope)
		}
		if cfg.isAzureWorkloadIdentity() && cfg.azureScope == "" {
			cfg.azureScope = defaultAKSServerAppID + "/.default"
		}
		out = append(out, cfg)
	}
	return out, nil
}
