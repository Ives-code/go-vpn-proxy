package main

import (
	"context"
	"dual-egress-gateway/internal/httpproxy"
	"dual-egress-gateway/internal/pool"
	"dual-egress-gateway/internal/proxycore"
	"dual-egress-gateway/internal/subscription"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func singleNode(path string) (subscription.NodeSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return subscription.NodeSpec{}, errors.New("cannot open node configuration")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return subscription.NodeSpec{}, errors.New("invalid node configuration size")
	}
	nodes, err := subscription.Parse(b, "application/json")
	if err != nil || len(nodes) != 1 {
		return subscription.NodeSpec{}, errors.New("fixed proxy requires exactly one valid node")
	}
	return nodes[0], nil
}

func serve(ctx context.Context, address, path string) error {
	user, password := os.Getenv("PROXY_USERNAME"), os.Getenv("PROXY_PASSWORD")
	if user == "" || password == "" {
		return errors.New("proxy credentials required")
	}
	node, err := singleNode(path)
	if err != nil {
		return err
	}
	engine, err := proxycore.NewEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()
	registry := pool.NewRegistry(engine, time.Second)
	if _, err = registry.Apply(subscription.Snapshot{Nodes: []subscription.NodeSpec{node}}); err != nil {
		return err
	}
	registry.MarkSuccess(node.ID, 0)
	server := httpproxy.New("fixed", httpproxy.Config{Username: user, Password: password, DialTimeout: 15 * time.Second}, registry, pool.NewSelector(), slog.Default())
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err = <-done:
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = server.Shutdown(drain)
	return err
}

func main() {
	address := flag.String("listen", "127.0.0.1:18084", "HTTP proxy listener")
	path := flag.String("node", "", "single-node JSON configuration")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, *address, *path); err != nil {
		slog.Error("fixed proxy stopped", "error", err)
		os.Exit(1)
	}
}
