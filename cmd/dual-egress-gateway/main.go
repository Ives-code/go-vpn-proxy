package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"dual-egress-gateway/internal/app"
	"dual-egress-gateway/internal/config"
	"dual-egress-gateway/internal/inspect"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "subscriptions" {
		flags := flag.NewFlagSet("subscriptions", flag.ExitOnError)
		address := flags.String("admin", "http://127.0.0.1:19090", "loopback management URL")
		flags.Parse(os.Args[2:])
		if err := inspect.Subscriptions(context.Background(), *address, os.Stdout); err != nil {
			slog.Error("订阅查询失败", "error", err)
			os.Exit(1)
		}
		return
	}
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath, os.LookupEnv)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	application, err := app.New(cfg, slog.Default())
	if err != nil {
		slog.Error("initialization failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := application.Run(ctx); err != nil {
		slog.Error("gateway stopped with an error", "error", err)
		os.Exit(1)
	}
}
