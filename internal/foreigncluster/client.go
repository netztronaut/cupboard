package foreigncluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// bearerTokenRoundTripper injects a Bearer token from an azureWITokenSource into
// every outbound request.
type bearerTokenRoundTripper struct {
	source *azureWITokenSource
	base   http.RoundTripper
}

func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := rt.source.Token()
	if err != nil {
		return nil, fmt.Errorf("obtaining Bearer token: %w", err)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return rt.base.RoundTrip(clone)
}

// BuildRESTConfig builds a *rest.Config for a single foreign cluster.
//
// caPool is the fleet trust store. When non-nil it replaces any CA embedded in
// the kubeconfig. Pass nil to fall back to system CAs.
// kubeconfigPath is only used for kubeconfig-context entries.
func BuildRESTConfig(cfg ClusterConfig, caPool *x509.CertPool, kubeconfigPath string) (*rest.Config, error) {
	if cfg.isAzureWorkloadIdentity() {
		return buildAzureRESTConfig(cfg, caPool)
	}
	return buildKubeconfigRESTConfig(cfg, caPool, kubeconfigPath)
}

func tlsTransport(caPool *x509.CertPool, clientCert *tls.Certificate) http.RoundTripper {
	tlsCfg := &tls.Config{}
	if caPool != nil {
		tlsCfg.RootCAs = caPool
	}
	if clientCert != nil {
		tlsCfg.Certificates = []tls.Certificate{*clientCert}
	}
	return &http.Transport{
		TLSClientConfig:       tlsCfg,
		MaxIdleConnsPerHost:   25,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

func buildAzureRESTConfig(cfg ClusterConfig, caPool *x509.CertPool) (*rest.Config, error) {
	// The azure token endpoint uses public CAs, so use a separate plain HTTP client for it.
	tokenSource, err := newAzureWITokenSource(cfg.azureScope, nil)
	if err != nil {
		return nil, fmt.Errorf("cluster %s: azure workload identity: %w", cfg.Endpoint, err)
	}
	transport := &bearerTokenRoundTripper{
		source: tokenSource,
		base:   tlsTransport(caPool, nil),
	}
	return &rest.Config{
		Host:      cfg.Endpoint,
		Transport: transport,
	}, nil
}

func buildKubeconfigRESTConfig(cfg ClusterConfig, caPool *x509.CertPool, kubeconfigPath string) (*rest.Config, error) {
	contextName := cfg.kubeconfigContext()
	if kubeconfigPath == "" {
		kubeconfigPath = clientcmd.RecommendedHomeFile
	}
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return nil, fmt.Errorf("cluster %s: kubeconfig %q not accessible: %w", cfg.Endpoint, kubeconfigPath, err)
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("cluster %s: building REST config from kubeconfig context %q: %w", cfg.Endpoint, contextName, err)
	}

	// Explicit endpoint always wins over whatever the kubeconfig says.
	restCfg.Host = cfg.Endpoint

	// When the fleet trust store is provided, override the CA and build a custom transport.
	// We may need to carry over a client certificate from the kubeconfig.
	if caPool != nil {
		var clientCert *tls.Certificate
		if len(restCfg.TLSClientConfig.CertData) > 0 && len(restCfg.TLSClientConfig.KeyData) > 0 {
			cert, loadErr := tls.X509KeyPair(
				restCfg.TLSClientConfig.CertData,
				restCfg.TLSClientConfig.KeyData,
			)
			if loadErr != nil {
				return nil, fmt.Errorf("cluster %s: loading kubeconfig client certificate: %w", cfg.Endpoint, loadErr)
			}
			clientCert = &cert
		}
		// Strip embedded CA so rest.Config doesn't compete with our transport.
		restCfg.CAData = nil
		restCfg.CAFile = ""
		restCfg.TLSClientConfig = rest.TLSClientConfig{}

		// Wrap with bearer token roundtripper if kubeconfig had a bearer token / exec.
		// For simplicity we build a standalone transport; exec plugins inside the kubeconfig
		// still work because rest.Config.WrapTransport is not set here — callers that need
		// exec plugins should not set a fleet trust store for kubeconfig contexts.
		restCfg.Transport = tlsTransport(caPool, clientCert)
	}

	return restCfg, nil
}
