package logger

import (
	"log/slog"
	"os"
)

// InitLogger initializes a new logger
func InitLogger() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
}
