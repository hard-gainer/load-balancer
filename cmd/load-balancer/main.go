package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hard-gainer/load-balancer/internal/api"
	"github.com/hard-gainer/load-balancer/internal/config"
)

func main() {
	slog.Info("initializing config")
	cfg, err := config.InitConfig()
	if err != nil {
		slog.Error("config error", "error", err)
	}

	slog.Info("config values:",
		"servers", cfg.Servers,
		"app", cfg.AppConfig.Port,
		"db", cfg.DBConfig.URL,
	)

	router := http.NewServeMux()
	router.HandleFunc("GET /clients", api.FindAllClients)
	router.HandleFunc("POST /clients", api.CreateClient)
	router.HandleFunc("PATCH /clients", api.UpdateClient)
	router.HandleFunc("DELETE /clients", api.DeleteClient)

	loggedRouter := api.LoggingMiddleware(router)

	server := http.Server{
		Addr:    fmt.Sprintf(":%v", cfg.AppConfig.Port),
		Handler: loggedRouter,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("server exited properly")
}
