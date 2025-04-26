package main

import (
	"log/slog"

	"github.com/hard-gainer/load-balancer/internal/config"
)

func main() {
	_, err := config.InitConfig()
	if err != nil {
		slog.Error("config error", "error", err)
	}
}
