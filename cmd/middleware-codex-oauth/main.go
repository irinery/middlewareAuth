package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/irinery/middlewareAuth/internal/auth"
	"github.com/irinery/middlewareAuth/internal/codex"
	"github.com/irinery/middlewareAuth/internal/config"
	"github.com/irinery/middlewareAuth/internal/httpapi"
	"github.com/irinery/middlewareAuth/internal/observability"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadConfig(ctx, config.EnvironMap())
	if err != nil {
		slog.Error("falha ao carregar config", "error", err)
		os.Exit(1)
	}

	logger := observability.NewLogger(slog.Default())
	httpClient := config.NewHTTPClient(cfg.Codex)
	store := auth.NewFileStore(*cfg)
	refresher := auth.NewRefresher(*cfg, store, httpClient)
	codexTransport := codex.NewTransport(cfg.Codex, httpClient)
	handler := httpapi.NewHandler(*cfg, store, refresher, codexTransport, httpClient)
	server := config.NewHTTPServer(cfg.HTTP, handler)

	go func() {
		logger.Info(ctx, "middleware iniciado", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(ctx, "servidor HTTP falhou", slog.String("error", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error(context.Background(), "shutdown HTTP falhou", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info(context.Background(), "middleware encerrado")
}
