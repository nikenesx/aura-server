package main

import (
	"log/slog"
	"os"

	"github.com/nikenesx/aura-server/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		slog.Error("application failed", "error", err)
		os.Exit(1)
	}
}
