package main

import (
	"context"
	"dyndns-updater/internal/updater"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := updater.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
