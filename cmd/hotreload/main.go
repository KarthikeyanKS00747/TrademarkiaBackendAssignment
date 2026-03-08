package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hotreload/internal/engine"
)

var version = "dev"

func main() {
	root := flag.String("root", ".", "Directory to watch for file changes (including all subfolders)")
	build := flag.String("build", "", "Command used to build the project when a change is detected")
	exec := flag.String("exec", "", "Command used to run the built server after a successful build")
	showVersion := flag.Bool("version", false, "Print version and exit")
	debounceMs := flag.Int("debounce", 300, "Debounce interval in milliseconds")
	extensions := flag.String("ext", ".go", "Comma-separated list of file extensions to watch (e.g. .go,.html,.yaml)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "hotreload - Automatic rebuild & restart on code changes\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  hotreload --root <project-folder> --build \"<build-command>\" --exec \"<run-command>\"\n\n")
		fmt.Fprintf(os.Stderr, "Example:\n")
		fmt.Fprintf(os.Stderr, "  hotreload --root ./myproject --build \"go build -o ./bin/server ./cmd/server\" --exec \"./bin/server\"\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("hotreload %s\n", version)
		os.Exit(0)
	}

	if *build == "" || *exec == "" {
		slog.Error("both --build and --exec flags are required")
		flag.Usage()
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := engine.Config{
		Root:       *root,
		BuildCmd:   *build,
		ExecCmd:    *exec,
		DebounceMs: *debounceMs,
		Extensions: *extensions,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	eng, err := engine.New(cfg, logger)
	if err != nil {
		slog.Error("failed to initialize engine", "error", err)
		os.Exit(1)
	}

	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	if err := eng.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("engine error", "error", err)
		os.Exit(1)
	}

	slog.Info("hotreload stopped")
}
