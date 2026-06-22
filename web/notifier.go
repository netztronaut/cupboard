package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DashboardNotifier manages WebSocket connections and broadcasts change notifications.
// It implements manager.Runnable: add it to a controller-runtime manager so its lifecycle
// is tied to the manager context. Call Notify() to signal that dashboard data may have
// changed; notifications are rate-limited to at most one broadcast per second and skipped
// when no clients are connected.
type DashboardNotifier struct {
	mu       sync.Mutex
	clients  map[*websocket.Conn]struct{}
	notify   chan struct{}
	upgrader websocket.Upgrader
}

// NewDashboardNotifier creates a DashboardNotifier. Start it by adding it to a
// controller-runtime manager via mgr.Add.
func NewDashboardNotifier() *DashboardNotifier {
	return &DashboardNotifier{
		clients: make(map[*websocket.Conn]struct{}),
		notify:  make(chan struct{}, 1),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				u, err := url.Parse(origin)
				if err != nil {
					return false
				}
				return strings.EqualFold(u.Host, r.Host)
			},
		},
	}
}

// Notify signals a potential dashboard change. Non-blocking; bursts are coalesced.
func (n *DashboardNotifier) Notify() {
	select {
	case n.notify <- struct{}{}:
	default:
	}
}

// Start implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable.
// It rate-limits broadcasts to at most once per second and skips when no clients are connected.
func (n *DashboardNotifier) Start(ctx context.Context) error {
	limiter := time.NewTicker(time.Second)
	defer limiter.Stop()
	pinger := time.NewTicker(30 * time.Second)
	defer pinger.Stop()
	pending := false
	for {
		select {
		case <-ctx.Done():
			n.closeAll()
			return nil
		case <-n.notify:
			pending = true
		case <-limiter.C:
			if !pending {
				continue
			}
			pending = false
			n.mu.Lock()
			hasClients := len(n.clients) > 0
			n.mu.Unlock()
			if hasClients {
				n.broadcastPing()
			}
		case <-pinger.C:
			n.broadcastWSPing()
		}
	}
}

func (n *DashboardNotifier) register(conn *websocket.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clients[conn] = struct{}{}
}

func (n *DashboardNotifier) unregister(conn *websocket.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.clients, conn)
}

func (n *DashboardNotifier) closeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for conn := range n.clients {
		_ = conn.Close()
		delete(n.clients, conn)
	}
}

func (n *DashboardNotifier) broadcastPing() {
	snapshot := n.snapshot()
	for _, conn := range snapshot {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
			_ = conn.Close()
			n.unregister(conn)
		}
	}
}

func (n *DashboardNotifier) broadcastWSPing() {
	snapshot := n.snapshot()
	for _, conn := range snapshot {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			_ = conn.Close()
			n.unregister(conn)
		}
	}
}

func (n *DashboardNotifier) snapshot() []*websocket.Conn {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*websocket.Conn, 0, len(n.clients))
	for conn := range n.clients {
		out = append(out, conn)
	}
	return out
}
