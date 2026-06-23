package foreigncluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

var log = ctrl.Log.WithName("foreigncluster")

const permissionCheckInterval = 15 * time.Minute

// ClusterInfo holds live API clients for one reachable foreign cluster.
// Name is the cluster's endpoint URL and serves as the display/source identifier.
type ClusterInfo struct {
	Name      string
	Client    client.Client
	Discovery discovery.DiscoveryInterface
}

// Manager maintains connections to foreign Kubernetes clusters and periodically
// logs a warning for any dashboard-relevant resource types the operator cannot list.
//
// It implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable.
type Manager struct {
	configs        []ClusterConfig
	caPool         *x509.CertPool
	kubeconfigPath string
	scheme         *runtime.Scheme

	mu      sync.RWMutex
	handles []*clusterHandle
}

// NewManager creates a Manager.
// scheme should contain all resource types you want to list on foreign clusters
// (normally the same global scheme used by the main controller-runtime manager).
func NewManager(configs []ClusterConfig, caPool *x509.CertPool, kubeconfigPath string, scheme *runtime.Scheme) *Manager {
	return &Manager{
		configs:        configs,
		caPool:         caPool,
		kubeconfigPath: kubeconfigPath,
		scheme:         scheme,
	}
}

// ActiveClusters returns a snapshot of currently reachable clusters.
func (m *Manager) ActiveClusters() []ClusterInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ClusterInfo, 0, len(m.handles))
	for _, h := range m.handles {
		if h.ready {
			out = append(out, ClusterInfo{
				Name:      h.cfg.Endpoint,
				Client:    h.k8sClient,
				Discovery: h.discovery,
			})
		}
	}
	return out
}

// Start implements manager.Runnable.
func (m *Manager) Start(ctx context.Context) error {
	m.connectAll()
	m.checkAllPermissions(ctx)

	ticker := time.NewTicker(permissionCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.reconnectDown()
			m.checkAllPermissions(ctx)
		}
	}
}

func (m *Manager) connectAll() {
	handles := make([]*clusterHandle, 0, len(m.configs))
	for _, cfg := range m.configs {
		h := &clusterHandle{cfg: cfg}
		if err := h.connect(cfg, m.caPool, m.kubeconfigPath, m.scheme); err != nil {
			log.Error(err, "Failed to connect to foreign cluster", "cluster", cfg.Endpoint)
		} else {
			h.ready = true
			log.Info("Connected to foreign cluster", "cluster", cfg.Endpoint)
		}
		handles = append(handles, h)
	}
	m.mu.Lock()
	m.handles = handles
	m.mu.Unlock()
}

func (m *Manager) reconnectDown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.handles {
		if h.ready {
			continue
		}
		if err := h.connect(h.cfg, m.caPool, m.kubeconfigPath, m.scheme); err != nil {
			log.Error(err, "Failed to reconnect to foreign cluster", "cluster", h.cfg.Endpoint)
		} else {
			h.ready = true
			log.Info("Reconnected to foreign cluster", "cluster", h.cfg.Endpoint)
		}
	}
}

func (m *Manager) checkAllPermissions(ctx context.Context) {
	m.mu.RLock()
	handles := make([]*clusterHandle, len(m.handles))
	copy(handles, m.handles)
	m.mu.RUnlock()

	for _, h := range handles {
		if h.ready {
			h.checkPermissions(ctx)
		}
	}
}

// clusterHandle holds mutable state for one foreign cluster connection.
type clusterHandle struct {
	cfg       ClusterConfig
	k8sClient client.Client
	discovery discovery.DiscoveryInterface
	ready     bool
}

func (h *clusterHandle) connect(cfg ClusterConfig, caPool *x509.CertPool, kubeconfigPath string, scheme *runtime.Scheme) error {
	restCfg, err := BuildRESTConfig(cfg, caPool, kubeconfigPath)
	if err != nil {
		return err
	}

	httpClient, err := rest.HTTPClientFor(restCfg)
	if err != nil {
		// rest.HTTPClientFor can fail when a custom Transport is already set because
		// it tries to wrap it — fall back to a plain client that honours restCfg.Transport.
		if restCfg.Transport != nil {
			httpClient = &http.Client{Transport: restCfg.Transport}
		} else {
			return fmt.Errorf("building HTTP client: %w", err)
		}
	}

	mapper, err := apiutil.NewDynamicRESTMapper(restCfg, httpClient)
	if err != nil {
		return fmt.Errorf("building REST mapper: %w", err)
	}

	c, err := client.NewWithWatch(restCfg, client.Options{
		Scheme:     scheme,
		Mapper:     mapper,
		HTTPClient: httpClient,
	})
	if err != nil {
		return fmt.Errorf("building client: %w", err)
	}

	dc, err := discovery.NewDiscoveryClientForConfigAndClient(restCfg, httpClient)
	if err != nil {
		return fmt.Errorf("building discovery client: %w", err)
	}

	h.k8sClient = c
	h.discovery = dc
	return nil
}

// probeResource is a resource whose list permission we check on foreign clusters.
type probeResource struct {
	group   string
	version string
	kind    string
}

var dashboardProbeResources = []probeResource{
	{group: "networking.k8s.io", version: "v1", kind: "Ingress"},
	{group: "", version: "v1", kind: "Service"},
	{group: "gateway.networking.k8s.io", version: "v1", kind: "HTTPRoute"},
	{group: "gateway.networking.k8s.io", version: "v1alpha2", kind: "TLSRoute"},
	{group: "gateway.networking.k8s.io", version: "v1alpha2", kind: "TCPRoute"},
	{group: "traefik.io", version: "v1alpha1", kind: "IngressRoute"},
	{group: "traefik.containo.us", version: "v1alpha1", kind: "IngressRoute"},
}

func (h *clusterHandle) checkPermissions(ctx context.Context) {
	for _, p := range dashboardProbeResources {
		gv := p.group + "/" + p.version
		if p.group == "" {
			gv = p.version
		}
		list, err := h.discovery.ServerResourcesForGroupVersion(gv)
		if err != nil {
			continue // group absent on this cluster
		}
		found := false
		for _, r := range list.APIResources {
			if r.Kind == p.kind {
				found = true
				break
			}
		}
		if !found {
			continue
		}

		ul := &unstructured.UnstructuredList{}
		ul.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   p.group,
			Version: p.version,
			Kind:    p.kind + "List",
		})
		if listErr := h.k8sClient.List(ctx, ul, client.Limit(1)); listErr != nil {
			if k8serrors.IsForbidden(listErr) || k8serrors.IsUnauthorized(listErr) {
				log.Info("Missing list permission on foreign cluster (will retry in 15m)",
					"cluster", h.cfg.Endpoint,
					"resource", p.kind,
					"groupVersion", gv,
				)
			}
		}
	}
}

// LoadFleetCAPool reads a PEM-encoded CA bundle from path and returns a cert pool.
// Returns nil (no error) when path is empty, which causes the default system CA
// verification to be used.
func LoadFleetCAPool(path string) (*x509.CertPool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fleet trust store from %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("no valid PEM certificates found in fleet trust store %s", path)
	}
	return pool, nil
}
