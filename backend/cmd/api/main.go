package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"greenwich-fire-responder/backend/internal/audioanalysis"
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/database"
	"greenwich-fire-responder/backend/internal/httpapi"
	"greenwich-fire-responder/backend/internal/operations"
	"greenwich-fire-responder/backend/internal/recordings"
	"greenwich-fire-responder/backend/internal/transcription"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithConfig(ctx, cfg, nil, slog.New(slog.NewJSONHandler(os.Stderr, nil)))
}

// Explicit context and transport make complete API lifecycle tests possible
// without signals, external providers, or permanent processes.
func runWithConfig(ctx context.Context, cfg config.Config, transport http.RoundTripper, logger *slog.Logger) error {

	db, err := database.Open(ctx, cfg.DatabaseURL, cfg.DatabaseRequired)
	if err != nil {
		return err
	}
	defer db.Close()

	ingestionCtx, cancelIngestion := context.WithCancel(ctx)
	ingestionDone := make(chan struct{})
	transcriptionDone := make(chan struct{})
	monitor := operations.New(db, cfg.Recordings.Directory, cfg.Transcription.MaxAttempts, cfg.Transcription.Enabled, cfg.Operations, logger)
	monitorDone := make(chan struct{})
	go func() { defer close(monitorDone); monitor.Run(ingestionCtx) }()
	defer func() { cancelIngestion(); <-monitorDone }()
	if cfg.Transcription.Enabled && cfg.Recordings.Directory != "" {
		provider, err := transcription.NewHTTPProvider(cfg.Transcription, transport)
		if err != nil {
			cancelIngestion()
			return err
		}
		worker := &transcription.Worker{Options: cfg.Transcription, Directory: cfg.Recordings.Directory, Store: db, Provider: provider, Logger: logger, Monitor: monitor}
		go func() { defer close(transcriptionDone); defer provider.Close(); worker.Run(ingestionCtx) }()
	} else {
		logger.Info("transcription", "outcome", "disabled")
		close(transcriptionDone)
	}
	defer func() { cancelIngestion(); <-transcriptionDone }()
	processor := &audioanalysis.Processor{Options: cfg.Audio,
		Analyzer: audioanalysis.Analyzer{Options: cfg.Audio, Tools: audioanalysis.ProcessTools{}}, Store: db, Logger: logger}
	if cfg.Recordings.Directory == "" {
		logger.Info("recording_ingestion", "outcome", "disabled", "reason", "directory_unconfigured")
		close(ingestionDone)
	} else if watcher, err := recordings.NewWatcher(cfg.Recordings, db, logger, processor); err != nil {
		// A filesystem/configuration failure must not take down the HTTP API.
		logger.Error("recording_ingestion", "outcome", "failed", "reason", "watcher_start_failed")
		close(ingestionDone)
	} else {
		go func() {
			defer close(ingestionDone)
			watcher.Run(ingestionCtx)
		}()
	}
	defer func() {
		cancelIngestion()
		<-ingestionDone
	}()

	authHandlers, closeAuth, err := buildAuthHandlers(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeAuth()

	handler := httpapi.NewHandler(db, monitor)
	if authHandlers != nil {
		root := http.NewServeMux()
		root.Handle("/api/auth/", authHandlers.Mux())
		root.Handle("/", handler)
		handler = root
		logger.Info("authentication", "outcome", "enabled")
	} else {
		logger.Info("authentication", "outcome", "disabled")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("starting greenwich-fire-responder-api on %s", cfg.HTTPAddr)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Print("shutting down greenwich-fire-responder-api")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
