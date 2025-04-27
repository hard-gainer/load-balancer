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
		"app", cfg.AppConfig.Port,
		"db", cfg.DBConfig.URL,
	)

	storage, err := storage.NewPostgres(cfg)
	if err != nil {
		slog.Error("failed to initialize storage", "error", err)
		os.Exit(1)
	}

	service := service.NewLoadBalancerService(storage)

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
	// router.HandleFunc("/request", handler.HandleRequest)

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
