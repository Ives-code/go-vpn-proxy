package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"dual-egress-gateway/internal/admin"
	"dual-egress-gateway/internal/alert"
	"dual-egress-gateway/internal/config"
	"dual-egress-gateway/internal/httpproxy"
	"dual-egress-gateway/internal/notify"
	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/proxycore"
	"dual-egress-gateway/internal/subscription"
)

type Prober interface {
	Probe(context.Context, pool.Dialer) (time.Duration, error)
}

type App struct {
	config        config.Config
	logger        *slog.Logger
	engine        *proxycore.Engine
	registry      *pool.Registry
	subscriptions *subscription.Manager
	prober        Prober
	httpProxy     *httpproxy.Server
	wsProxy       *httpproxy.Server
	adminServer   *http.Server
	refreshMu     sync.Mutex
	statusMu      sync.RWMutex
	lastRefresh   time.Time
	closed        sync.Once
	alerts        *alert.Monitor
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	engine, err := proxycore.NewEngine(context.Background())
	if err != nil {
		return nil, err
	}
	registry := pool.NewRegistry(engine, cfg.ProbeInterval)
	sources := make([]subscription.Source, 0, len(cfg.SubscriptionURLs))
	for index, sourceURL := range cfg.SubscriptionURLs {
		sources = append(sources, subscription.Source{ID: fmt.Sprintf("%d", index+1), URL: sourceURL})
	}
	fetcher := subscription.NewHTTPFetcher(&http.Client{Timeout: 30 * time.Second}, cfg.MaxSubscriptionSize)
	manager := subscription.NewManager(sources, fetcher)
	prober, err := proxycore.NewProber(cfg.ProbeURL, http.StatusNoContent, &tls.Config{MinVersion: tls.VersionTLS12})
	if err != nil {
		_ = engine.Close()
		return nil, err
	}
	proxyConfig := httpproxy.Config{
		Username:    cfg.Username.Reveal(),
		Password:    cfg.Password.Reveal(),
		DialTimeout: cfg.DialTimeout,
	}
	application := &App{
		config:        cfg,
		logger:        logger,
		engine:        engine,
		registry:      registry,
		subscriptions: manager,
		prober:        prober,
	}
	application.httpProxy = httpproxy.New("http", proxyConfig, registry, pool.NewSelector(), logger)
	if cfg.PushBaseURL.Reveal() != "" {
		client, err := notify.NewClient(cfg.PushBaseURL.Reveal(), nil)
		if err != nil {
			_ = engine.Close()
			return nil, err
		}
		application.alerts = alert.NewMonitor(client, cfg.LowNodeThreshold, alert.SourceLabels(cfg.SubscriptionURLs))
	}
	application.wsProxy = httpproxy.New("ws", proxyConfig, registry, pool.NewSelector(), logger)
	adminHandler := admin.NewHandler(application, cfg.AdminToken.Reveal(), []string{
		cfg.Username.Reveal(), cfg.Password.Reveal(), cfg.AdminToken.Reveal(),
	})
	application.adminServer = &http.Server{Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second}
	return application, nil
}

func (application *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listeners, err := listenAll(
		[]string{application.config.HTTPListen, application.config.WSListen, application.config.AdminListen},
		net.Listen,
	)
	if err != nil {
		return err
	}

	serveErrors := make(chan error, 3)
	go func() { serveErrors <- application.httpProxy.Serve(listeners[0]) }()
	go func() { serveErrors <- application.wsProxy.Serve(listeners[1]) }()
	go func() {
		err := application.adminServer.Serve(listeners[2])
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErrors <- err
	}()

	go func() {
		if refreshErr := application.Refresh(ctx); refreshErr != nil {
			application.logger.Warn("initial subscription refresh was partial or failed", "error", refreshErr)
		}
	}()

	loopDone := make(chan struct{})
	go application.backgroundLoop(ctx, loopDone)
	alertDone := make(chan struct{})
	go application.alertLoop(ctx, alertDone)

	select {
	case <-ctx.Done():
		err = nil
	case serveErr := <-serveErrors:
		if serveErr != nil {
			err = serveErr
		}
	}
	cancel()
	application.shutdown()
	<-loopDone
	<-alertDone
	return err
}

func (application *App) Refresh(ctx context.Context) error {
	application.refreshMu.Lock()
	defer application.refreshMu.Unlock()

	snapshot, refreshErr := application.subscriptions.Refresh(ctx)
	if len(snapshot.Nodes) > 0 {
		if _, applyErr := application.registry.Apply(snapshot); applyErr != nil {
			return fmt.Errorf("apply subscription snapshot: %w", applyErr)
		}
		probeDue(ctx, application.registry, application.prober, 16, application.config.DialTimeout, application.logger)
	}
	application.statusMu.Lock()
	application.lastRefresh = time.Now().UTC()
	application.statusMu.Unlock()
	return refreshErr
}

func (application *App) Status() admin.Status {
	stats := application.registry.Stats()
	application.statusMu.RLock()
	lastRefresh := application.lastRefresh
	application.statusMu.RUnlock()
	snapshot := application.subscriptions.Snapshot()
	return admin.Status{
		Subscriptions: application.subscriptionStatuses(snapshot),
		Ready:         stats.Healthy > 0,
		Nodes:         stats,
		HTTPActive:    application.httpProxy.ActiveConnections(),
		WSActive:      application.wsProxy.ActiveConnections(),
		LastRefresh:   lastRefresh,
		SourceErrors:  snapshot.SourceErrors,
		ExpiresAt:     snapshot.ExpiresAt,
	}
}

func (application *App) subscriptionStatuses(snapshot subscription.Snapshot) []admin.SubscriptionStatus {
	stats := application.registry.StatsBySource()
	counts := make(map[string]int)
	for _, node := range snapshot.Nodes {
		for _, id := range node.SourceIDs {
			counts[id]++
		}
	}
	result := make([]admin.SubscriptionStatus, 0, len(application.config.SubscriptionURLs))
	for i, raw := range application.config.SubscriptionURLs {
		id := fmt.Sprint(i + 1)
		address := "[隐藏]"
		if u, err := url.Parse(raw); err == nil {
			address = u.Scheme + "://" + u.Host + "/[已隐藏路径和密钥]"
		}
		state := "pending"
		if snapshot.SourceErrors[id] != "" {
			state = "failed"
		} else if counts[id] > 0 {
			state = "ok"
		}
		result = append(result, admin.SubscriptionStatus{ID: id, Address: address, ExpiresAt: snapshot.ExpiresAt[id], RefreshState: state, ParsedNodes: counts[id], Nodes: stats[id]})
	}
	return result
}

func (application *App) backgroundLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	refreshTicker := time.NewTicker(application.config.RefreshInterval)
	probeTicker := time.NewTicker(application.config.ProbeInterval)
	defer refreshTicker.Stop()
	defer probeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshTicker.C:
			if err := application.Refresh(ctx); err != nil {
				application.logger.Warn("subscription refresh was partial or failed", "error", err)
			}
		case <-probeTicker.C:
			application.refreshMu.Lock()
			probeDue(ctx, application.registry, application.prober, 16, application.config.DialTimeout, application.logger)
			application.refreshMu.Unlock()
		}
	}
}

// Alerts sample settled state; network delivery never holds the refresh/probe lock.
func (application *App) alertLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	if application.alerts == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			application.refreshMu.Lock()
			application.statusMu.RLock()
			ready := !application.lastRefresh.IsZero()
			application.statusMu.RUnlock()
			snapshot := application.subscriptions.Snapshot()
			stats := application.registry.Stats()
			application.refreshMu.Unlock()
			if !ready || ctx.Err() != nil {
				continue
			}
			failures := application.alerts.Evaluate(ctx, snapshot.SourceErrors, stats)
			failures = append(failures, application.alerts.EvaluateExpiry(ctx, snapshot.ExpiresAt, time.Now())...)
			if len(failures) > 0 {
				application.logger.Warn("notification delivery failed; will retry", "count", len(failures))
			}
		}
	}
}

func (application *App) shutdown() {
	application.closed.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), application.config.ShutdownDrain)
		defer cancel()
		_ = application.httpProxy.Shutdown(ctx)
		_ = application.wsProxy.Shutdown(ctx)
		_ = application.adminServer.Shutdown(ctx)
		_ = application.engine.Close()
	})
}

func listenAll(addresses []string, listen func(string, string) (net.Listener, error)) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(addresses))
	for _, address := range addresses {
		listener, err := listen("tcp", address)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("listen on configured address: %w", err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func probeDue(ctx context.Context, registry *pool.Registry, prober Prober, concurrency int, timeout time.Duration, loggers ...*slog.Logger) {
	leases := registry.ProbeDue(time.Now())
	if concurrency < 1 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	semaphore := make(chan struct{}, concurrency)
	var wait sync.WaitGroup
	for _, lease := range leases {
		lease := lease
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			probeContext, cancel := context.WithTimeout(ctx, timeout)
			latency, err := prober.Probe(probeContext, lease.Dialer)
			cancel()
			if err != nil {
				if registry.MarkFailure(lease.ID, err) && len(loggers) > 0 && loggers[0] != nil {
					loggers[0].Warn("proxy node became unhealthy", "node_id", lease.ID, "source_ids", registry.SourceIDs(lease.ID), "failure_class", "health_probe")
				}
			} else {
				if registry.MarkSuccess(lease.ID, latency) && len(loggers) > 0 && loggers[0] != nil {
					loggers[0].Info("proxy node recovered", "node_id", lease.ID, "source_ids", registry.SourceIDs(lease.ID))
				}
			}
			_ = lease.Close()
		}()
	}
	wait.Wait()
}
