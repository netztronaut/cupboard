package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

var syncLog = ctrl.Log.WithName("web").WithName("sync")

// SyncClient fetches dashboard data from peer instances periodically.
type SyncClient struct {
	options    SyncOptions
	httpClient *http.Client
	mu         sync.RWMutex
	cache      map[string]DashboardResponse
}

// NewSyncClient creates a SyncClient with the given options.
func NewSyncClient(options SyncOptions) (*SyncClient, error) {
	httpClient, err := buildSyncHTTPClient(options.TLS)
	if err != nil {
		return nil, err
	}
	return &SyncClient{
		options:    options,
		httpClient: httpClient,
		cache:      make(map[string]DashboardResponse),
	}, nil
}

// Start implements manager.Runnable. It polls peers every 30 seconds.
func (s *SyncClient) Start(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.fetchAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.fetchAll(ctx)
		}
	}
}

func (s *SyncClient) fetchAll(ctx context.Context) {
	peers := s.discoverPeers(ctx)

	peerSet := make(map[string]struct{}, len(peers))
	for _, p := range peers {
		peerSet[p] = struct{}{}
	}

	for _, peer := range peers {
		resp, err := s.fetchFromPeer(ctx, peer)
		if err != nil {
			syncLog.Error(err, "Failed to fetch from sync peer", "peer", peer)
			continue
		}
		s.mu.Lock()
		s.cache[peer] = resp
		s.mu.Unlock()
	}

	// Remove entries for peers that are no longer discovered.
	s.mu.Lock()
	for peer := range s.cache {
		if _, ok := peerSet[peer]; !ok {
			delete(s.cache, peer)
		}
	}
	s.mu.Unlock()
}

func (s *SyncClient) discoverPeers(ctx context.Context) []string {
	seen := make(map[string]struct{})
	var peers []string
	add := func(u string) {
		if u = strings.TrimSpace(u); u == "" {
			return
		}
		if _, ok := seen[u]; !ok {
			seen[u] = struct{}{}
			peers = append(peers, u)
		}
	}
	for _, u := range s.options.URLs {
		add(u)
	}
	for _, record := range s.options.SRVRecords {
		for _, u := range lookupSRVPeers(ctx, record) {
			add(u)
		}
	}
	return peers
}

// lookupSRVPeers performs a DNS SRV lookup on record and returns HTTPS URLs
// for all discovered targets. The record must be in standard SRV DNS name form,
// e.g. "_cupboard-sync._tcp.example.com".
func lookupSRVPeers(ctx context.Context, record string) []string {
	// Passing empty service and proto makes net.LookupSRV look up the name directly.
	_, addrs, err := net.DefaultResolver.LookupSRV(ctx, "", "", record)
	if err != nil {
		syncLog.Error(err, "SRV lookup failed", "record", record)
		return nil
	}
	peers := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		host := strings.TrimSuffix(addr.Target, ".")
		peers = append(peers, fmt.Sprintf("https://%s:%d", host, addr.Port))
	}
	return peers
}

func (s *SyncClient) fetchFromPeer(ctx context.Context, peerURL string) (DashboardResponse, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, peerURL+"/dashboard", nil)
	if err != nil {
		return DashboardResponse{}, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return DashboardResponse{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return DashboardResponse{}, fmt.Errorf("peer returned HTTP %d", resp.StatusCode)
	}
	var result DashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return DashboardResponse{}, err
	}
	return result, nil
}

// GetRemoteData returns a snapshot of all cached remote dashboard data keyed by peer URL.
func (s *SyncClient) GetRemoteData() map[string]DashboardResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]DashboardResponse, len(s.cache))
	maps.Copy(result, s.cache)
	return result
}

// newSyncHandler returns an http.Handler serving local dashboard data for peer consumption.
// It never re-exports data received from other peers to prevent sync cycles.
func newSyncHandler(collector *dashboardCollector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		payload, err := collector.collectDashboard(r.Context(), []string{allLinkGroupsWildcard}, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	return mux
}

func buildSyncHTTPClient(opts SyncTLSOptions) (*http.Client, error) {
	transport := &http.Transport{}
	authCert, authKey := opts.AuthCert, opts.AuthKey
	if authCert == "" && authKey == "" {
		authCert, authKey = opts.Cert, opts.Key
	}
	if opts.CA != "" || (authCert != "" && authKey != "") {
		tlsConfig, err := buildClientTLSConfig(opts.CA, authCert, authKey)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{Transport: transport}, nil
}

func buildClientTLSConfig(ca, certFile, keyFile string) (*tls.Config, error) {
	tlsConfig := &tls.Config{}
	if ca != "" {
		pool, err := loadCertPool(ca)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = pool
	}
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("loading sync client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return tlsConfig, nil
}

// BuildSyncServerTLSConfig builds the TLS config for the sync server.
// Returns nil if no certificate is configured (plain HTTP mode).
// When a CA is provided, client certificates are required (mTLS).
func BuildSyncServerTLSConfig(opts SyncTLSOptions) (*tls.Config, error) {
	if opts.Cert == "" || opts.Key == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(opts.Cert, opts.Key)
	if err != nil {
		return nil, fmt.Errorf("loading sync server certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	if opts.CA != "" {
		pool, err := loadCertPool(opts.CA)
		if err != nil {
			return nil, err
		}
		tlsConfig.ClientCAs = pool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tlsConfig, nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate from %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("parsing CA certificate from %s", path)
	}
	return pool, nil
}
