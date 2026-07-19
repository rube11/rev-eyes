package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/audio"
	"github.com/rube11/rev-eyes/backend/internal/database"
	"github.com/rube11/rev-eyes/backend/internal/router"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseCtx, cancelDatabase := context.WithTimeout(context.Background(), 10*time.Second)
	databasePool, err := database.Open(databaseCtx, os.Getenv("DATABASE_URL"))
	cancelDatabase()
	if err != nil {
		return err
	}
	defer databasePool.Close()
	slog.Info("database connection established")

	classifier, err := router.NewOpenAIClassifier(
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_ROUTER_MODEL"),
	)
	if err != nil {
		return err
	}

	transcriber, err := stt.NewDeepGramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	if err != nil {
		return err
	}
	activityRouter := router.NewRouter(classifier)
	audioServer := audio.NewServer(transcriber, func(ctx context.Context, utterance string) {
		routeUtterance(ctx, utterance, activityRouter)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              listenAddress(),
		Handler:           audioServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("audio WebSocket server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		serverErr := server.Shutdown(shutdownCtx)
		audioErr := audioServer.Shutdown(shutdownCtx)
		return errors.Join(serverErr, audioErr)
	}
}

func routeUtterance(ctx context.Context, utterance string, activityRouter *router.Router) {
	decision, err := activityRouter.Route(ctx, utterance)
	if err != nil {
		slog.ErrorContext(ctx, "failed to route utterance", "error", err)
		return
	}

	slog.InfoContext(ctx, "utterance routed",
		"action", decision.Action,
	)
}

func listenAddress() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}
