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
	"github.com/hard-gainer/load-balancer/internal/models"
	"github.com/hard-gainer/load-balancer/internal/service"
	"github.com/hard-gainer/load-balancer/internal/storage"
)

func main() {
	slog.Info("initializing config")
	cfg, err := config.InitConfig()
	if err != nil {
		slog.Error("config error", "error", err)
	}

	slog.Info("config values:",
		"servers", cfg.Servers,
		"app_port", cfg.Port,
		"db_url", cfg.DBURL,
	)

	slog.Info("initializing storage")
	storage, err := storage.NewPostgres(cfg)
	if err != nil {
		slog.Error("failed to initialize storage", "error", err)
		os.Exit(1)
	}
	slog.Info("storage successfully initialized")

	slog.Info("initializing service")
	service, err := service.NewLoadBalancerService(storage)
	if err != nil {
		slog.Error("failed to initialize service", "error", err)
		os.Exit(1)
	}
	slog.Info("service successfully initialized")

	backends := make([]models.Backend, 0)
	for _, elem := range cfg.Servers {
		backend := models.Backend{
			URL:    elem.URL,
			Weight: elem.Weight,
		}
		backends = append(backends, backend)
	}

	handler := api.NewLoadBalancerHandler(service, backends)

	router := http.NewServeMux()
	router.HandleFunc("GET /clients", handler.FindAllClients)
	router.HandleFunc("POST /clients", handler.CreateClient)
	router.HandleFunc("PUT /clients", handler.UpdateClient)
	router.HandleFunc("DELETE /clients", handler.DeleteClient)
	router.HandleFunc("/request", handler.HandleRequest)

	loggedRouter := api.LoggingMiddleware(router)

	slog.Info("starting the server", "port", cfg.Port)
	server := http.Server{
		Addr:    fmt.Sprintf(":%v", cfg.Port),
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

	service.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("server exited properly")

	storage.Close()
}
