package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/irinery/middlewareAuth/internal/mcpstdio"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcpstdio.NewFromEnv(os.Stderr)
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
