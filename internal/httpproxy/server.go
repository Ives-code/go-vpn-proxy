package httpproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"dual-egress-gateway/internal/pool"
)

type Config struct {
	Username    string
	Password    string
	DialTimeout time.Duration
}

type Server struct {
	name     string
	config   Config
	registry *pool.Registry
	selector *pool.Selector
	logger   *slog.Logger
	http     *http.Server
	sessions sync.Map
	active   atomic.Int64
}

type connectionKey struct{}

func New(name string, cfg Config, registry *pool.Registry, selector *pool.Selector, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	server := &Server{name: name, config: cfg, registry: registry, selector: selector, logger: logger}
	server.http = &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			session := &connectionSession{server: server, client: conn}
			server.sessions.Store(conn, session)
			server.active.Add(1)
			return context.WithValue(ctx, connectionKey{}, session)
		},
		ConnState: func(conn net.Conn, state http.ConnState) {
			if state != http.StateClosed {
				return
			}
			if value, ok := server.sessions.LoadAndDelete(conn); ok {
				value.(*connectionSession).release()
			}
		},
	}
	return server
}

func (server *Server) Serve(listener net.Listener) error {
	err := server.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) Shutdown(ctx context.Context) error {
	return server.http.Shutdown(ctx)
}

func (server *Server) ActiveConnections() int64 {
	return server.active.Load()
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !server.authorized(request) {
		response.Header().Set("Proxy-Authenticate", `Basic realm="dual-egress-gateway"`)
		http.Error(response, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	request.Header.Del("Proxy-Authorization")
	if request.Method == http.MethodConnect {
		server.handleConnect(response, request)
		return
	}
	server.handleForward(response, request)
}

type connectionSession struct {
	server    *Server
	client    net.Conn
	mu        sync.Mutex
	selected  *pool.NodeLease
	transport *http.Transport
	once      sync.Once
}

func (session *connectionSession) dial(ctx context.Context, network, address string) (net.Conn, error) {
	session.mu.Lock()
	defer session.mu.Unlock()

	if session.selected != nil {
		conn, err := session.selected.Dialer.DialContext(ctx, network, address)
		if err != nil {
			session.server.registry.MarkFailure(session.selected.ID, err)
		}
		return conn, err
	}

	candidates := session.server.registry.RoutingSnapshot()
	halfOpen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		halfOpen[candidate.ID] = candidate.HalfOpen
	}
	attempt := session.server.selector.Begin(candidates)
	for {
		id, ok := attempt.Next()
		if !ok {
			return nil, errors.New("all proxy nodes failed")
		}
		lease, ok := session.server.registry.Acquire(id, halfOpen[id])
		if !ok {
			continue
		}
		dialContext := ctx
		cancel := func() {}
		if session.server.config.DialTimeout > 0 {
			dialContext, cancel = context.WithTimeout(ctx, session.server.config.DialTimeout)
		}
		conn, err := lease.Dialer.DialContext(dialContext, network, address)
		cancel()
		if err != nil {
			session.server.registry.MarkFailure(id, err)
			_ = lease.Close()
			continue
		}
		session.server.registry.MarkSuccess(id, 0)
		attempt.Commit(id)
		session.selected = lease
		return conn, nil
	}
}

func (session *connectionSession) release() {
	session.once.Do(func() {
		session.mu.Lock()
		if session.transport != nil {
			session.transport.CloseIdleConnections()
		}
		if session.selected != nil {
			_ = session.selected.Close()
			session.selected = nil
		}
		session.mu.Unlock()
		session.server.active.Add(-1)
	})
}

func sessionFrom(request *http.Request) *connectionSession {
	value, _ := request.Context().Value(connectionKey{}).(*connectionSession)
	return value
}
