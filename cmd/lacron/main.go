package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dismay712/lacron/internal/lacron"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "healthcheck":
		return lacron.Healthcheck()
	case "version", "--version":
		fmt.Println(lacron.Version)
		return nil
	case "trigger":
		if len(os.Args) != 3 {
			return fmt.Errorf("usage: lacron trigger JOB_ID")
		}
		socket := os.Getenv("LACRON_SOCKET")
		if socket == "" {
			socket = "/run/lacron/runner.sock"
		}
		return lacron.Trigger(context.Background(), socket, os.Args[2])
	case "serve":
		cfg, err := lacron.ConfigFromEnv()
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return lacron.Serve(ctx, cfg)
	default:
		return fmt.Errorf("usage: lacron [serve|trigger JOB_ID|healthcheck|version]")
	}
}
