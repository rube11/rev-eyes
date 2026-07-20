package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai"
	"github.com/rube11/rev-eyes/backend/internal/auth"
	"github.com/rube11/rev-eyes/backend/internal/database"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/location"
	"github.com/rube11/rev-eyes/backend/internal/web"
)

const memoryAcknowledgment = "Got it, I'll remember that."

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseCtx, cancelDatabase := context.WithTimeout(ctx, 10*time.Second)
	databasePool, err := database.Open(databaseCtx, os.Getenv("DATABASE_URL"))
	cancelDatabase()
	if err != nil {
		return err
	}
	defer databasePool.Close()
	slog.Info("database connection established")
	sessionStore, err := session.NewStore(databasePool)
	if err != nil {
		return err
	}
	memoryStore, err := memory.NewStore(databasePool)
	if err != nil {
		return err
	}

	origins, err := web.NewOriginPolicy(os.Getenv("FRONTEND_ORIGIN"))
	if err != nil {
		return err
	}
	tokenVerifier, err := auth.NewSupabaseVerifier(ctx, os.Getenv("SUPABASE_URL"))
	if err != nil {
		return err
	}
	tickets := auth.NewTicketStore()
	ticketHandler, err := auth.NewTicketHandler(
		tokenVerifier.Verify,
		sessionStore.Resume,
		tickets,
	)
	if err != nil {
		return err
	}

	classifier, err := assistant.NewOpenAIClassifier(
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
	activityRouter := assistant.NewRouter(classifier)

	toolRegistry := tool.NewRegistry()
	locationStore := location.NewStore()
	locationTool, err := location.New(locationStore)
	if err != nil {
		return err
	}
	if err := toolRegistry.Register(locationTool); err != nil {
		return err
	}

	toolExecutor, err := tool.NewExecutor(toolRegistry)
	if err != nil {
		return err
	}
	agent, err := openai.NewAgent(
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_AGENT_MODEL"),
		toolRegistry,
		toolExecutor,
	)
	if err != nil {
		return err
	}
	assistantService, err := assistant.NewService(activityRouter, agent)
	if err != nil {
		return err
	}

	realtimeServer := realtime.NewServer(transcriber, realtime.Handlers{
		Authenticate: tickets.Consume,
		CheckOrigin:  origins.Allows,
		Utterance: func(ctx context.Context, scope tool.Scope, utterance string) (string, error) {
			return handleUtterance(
				ctx,
				scope,
				utterance,
				assistantService,
				sessionStore,
				memoryStore,
			)
		},
		Location: func(_ context.Context, scope tool.Scope, update realtime.LocationUpdate) error {
			return locationStore.Update(scope, location.Position{
				Latitude:       update.Latitude,
				Longitude:      update.Longitude,
				AccuracyMeters: update.AccuracyMeters,
			})
		},
		Disconnect: locationStore.Delete,
	})

	mux := http.NewServeMux()
	mux.Handle("/auth/ws-ticket", origins.Handler(ticketHandler))
	mux.Handle("/", realtimeServer)

	server := &http.Server{
		Addr:              listenAddress(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("server listening", "address", server.Addr)
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
		realtimeErr := realtimeServer.Shutdown(shutdownCtx)
		return errors.Join(serverErr, realtimeErr)
	}
}

type utteranceService interface {
	HandleUtterance(context.Context, tool.Scope, string) (assistant.Outcome, error)
}

type transcriptStore interface {
	Append(context.Context, tool.Scope, session.Speaker, string) (string, error)
}

type memoryStore interface {
	Remember(context.Context, tool.Scope, string, string) error
}

func handleUtterance(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
	service utteranceService,
	transcripts transcriptStore,
	memories memoryStore,
) (string, error) {
	utteranceID, err := transcripts.Append(ctx, scope, session.SpeakerUser, utterance)
	if err != nil {
		return "", fmt.Errorf("persist user utterance: %w", err)
	}

	outcome, err := service.HandleUtterance(ctx, scope, utterance)
	if err != nil {
		return "", err
	}

	response := outcome.Response
	if outcome.Decision.Action == assistant.ActionRemember {
		memoryText := strings.TrimSpace(outcome.Decision.Query)
		if memoryText == "" {
			memoryText = utterance
		}
		if err := memories.Remember(ctx, scope, utteranceID, memoryText); err != nil {
			return "", fmt.Errorf("persist memory: %w", err)
		}
		response = memoryAcknowledgment
	}

	if response != "" {
		if _, err := transcripts.Append(
			ctx,
			scope,
			session.SpeakerAssistant,
			response,
		); err != nil {
			return "", fmt.Errorf("persist assistant utterance: %w", err)
		}
	}

	slog.InfoContext(ctx, "utterance handled",
		"action", outcome.Decision.Action,
		"responded", response != "",
	)
	return response, nil
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
