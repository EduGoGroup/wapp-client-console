package main

import (
	"log/slog"
	"os"

	"github.com/EduGoGroup/wapp-client-console/internal/bootstrap"
	"github.com/EduGoGroup/wapp-client-console/internal/config"
)

func main() {
	cfg := config.Load()

	var handler slog.Handler
	if cfg.Environment == "local" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	slog.SetDefault(slog.New(handler))

	slog.Info("arrancando consola de cliente",
		"http_addr", cfg.HTTPAddr,
		"public_api", cfg.PublicAPIBaseURL,
		"identity", cfg.IdentityBaseURL,
		"env", cfg.Environment)

	if err := bootstrap.Run(&cfg); err != nil {
		slog.Error("servidor terminado con error", "error", err)
		os.Exit(1)
	}
}
