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
)

func main() {
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
