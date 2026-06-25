// Command server runs the summarize REST API service.
//
// Environment variables are loaded from .env or the process environment.
// See .env.example for all available configuration.
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/diag"
	"github.com/cschleiden/go-workflows/worker"
	"github.com/jimbo/summarize/internal/config"
	"github.com/jimbo/summarize/internal/engine"
	"github.com/jimbo/summarize/internal/events"
	"github.com/jimbo/summarize/internal/httpapi"
	"github.com/jimbo/summarize/internal/store"
	"github.com/jimbo/summarize/internal/workflow"
)

// Version info set by goreleaser ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Setup logging
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Create data directories
	os.MkdirAll(cfg.DataDir, 0755)
	os.MkdirAll(filepath.Join(cfg.DataDir, "workdirs"), 0755)

	appDBPath := filepath.Join(cfg.DataDir, "summarize.db")
	workflowDBPath := filepath.Join(cfg.DataDir, "workflows.db")

	// Open app store
	appStore, err := store.New(appDBPath)
	if err != nil {
		log.Fatalf("Failed to open app store: %v", err)
	}
	defer appStore.Close()

	// Start embedded NATS
	natsServer, err := events.Start(cfg.NATSHost, cfg.NATSPort)
	if err != nil {
		log.Fatalf("Failed to start embedded NATS: %v", err)
	}
	defer natsServer.Shutdown()

	// Create event publisher
	publisher := events.NewPublisher(natsServer.Conn(), cfg.NATSSubject)

	// Setup go-workflows backend
	wfBackend := sqlite.NewSqliteBackend(workflowDBPath,
		sqlite.WithBackendOptions(
			backend.WithLogger(logger),
			backend.WithWorkerName("summarize-worker"),
		),
	)
	defer wfBackend.Close()

	// Create workflow client
	wfClient := client.New(wfBackend)

	// Create engines
	piEngine := engine.NewPiAdapter(cfg.PiBin)
	agyEngine := engine.NewAgyAdapter(cfg.AgyBin)

	// Setup workflow dependencies
	workflow.SetDeps(workflow.Deps{
		Store:     appStore,
		Config:    cfg,
		PiEngine:  piEngine,
		AgyEngine: agyEngine,
	})

	// Create and start worker
	w := worker.New(wfBackend, nil)
	w.RegisterWorkflow(workflow.SummaryWorkflow)
	w.RegisterActivity(workflow.MarkRunningActivity)
	w.RegisterActivity(workflow.LoadRunActivity)
	w.RegisterActivity(workflow.FetchTranscriptActivity)
	w.RegisterActivity(workflow.SummarizeActivity)
	w.RegisterActivity(workflow.FailRunActivity)

	if err := w.Start(ctx); err != nil {
		log.Fatalf("Failed to start worker: %v", err)
	}

	// Create workflow starter
	starter := workflow.NewStarter(wfClient)

	// Start NATS subscriber
	subscriber := events.NewSubscriber(
		natsServer.Conn(),
		cfg.NATSSubject,
		starter.HandleEvent(ctx),
	)
	if err := subscriber.Start(); err != nil {
		log.Fatalf("Failed to start subscriber: %v", err)
	}
	defer subscriber.Stop()

	// Recover queued runs from previous crash
	if err := workflow.RecoverQueuedRuns(ctx, publisher); err != nil {
		slog.Error("Failed to recover queued runs", "error", err)
	}

	// Create HTTP handlers
	handlers := &httpapi.Handlers{
		Store:     appStore,
		Config:    cfg,
		Publisher: publisher,
		Models:    engine.NewModelCatalog(engine.DefaultModelCatalogTTL, piEngine, agyEngine),
	}

	// Create router
	diagBackend, _ := any(wfBackend).(diag.Backend)
	router := httpapi.NewRouter(handlers, diagBackend)

	// Start HTTP server
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	slog.Info("Starting summarize server",
		"version", version,
		"commit", commit,
		"date", date,
		"port", cfg.Port,
		"engine", cfg.DefaultEngine,
		"data_dir", cfg.DataDir,
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("Shutting down...")
	cancel()

	// Graceful shutdown
	if err := srv.Shutdown(context.Background()); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("Server stopped")
}
