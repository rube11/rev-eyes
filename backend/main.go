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

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai"
	"github.com/rube11/rev-eyes/backend/internal/auth"
	"github.com/rube11/rev-eyes/backend/internal/automation/proposal"
	"github.com/rube11/rev-eyes/backend/internal/automation/reminder"
	"github.com/rube11/rev-eyes/backend/internal/automation/scheduler"
	"github.com/rube11/rev-eyes/backend/internal/automation/scheduler/registration"
	"github.com/rube11/rev-eyes/backend/internal/automation/watch"
	"github.com/rube11/rev-eyes/backend/internal/database"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/notification"
	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
	"github.com/rube11/rev-eyes/backend/internal/tool/location"
	"github.com/rube11/rev-eyes/backend/internal/tool/websearch"
	"github.com/rube11/rev-eyes/backend/internal/web"
)

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
	reminderStore, err := reminder.NewStore(databasePool)
	if err != nil {
		return err
	}
	watchStore, err := watch.NewStore(databasePool)
	if err != nil {
		return err
	}
	proposalStore, err := proposal.NewStore(databasePool)
	if err != nil {
		return err
	}
	notificationStore, err := notification.NewStore(databasePool)
	if err != nil {
		return err
	}
	registrationStore, err := registration.NewStore(databasePool)
	if err != nil {
		return err
	}
	scheduledEventStore, err := scheduler.NewStore(databasePool)
	if err != nil {
		return err
	}
	scheduleRegistrar, err := registration.NewClient(
		os.Getenv("SCHEDULE_REGISTRAR_URL"),
		&http.Client{Timeout: 10 * time.Second},
	)
	if err != nil {
		return err
	}
	registrationDispatcher, err := registration.NewDispatcher(
		registrationStore,
		scheduleRegistrar,
	)
	if err != nil {
		return err
	}
	proposalConfirmer, err := proposal.NewConfirmer(
		proposalStore,
		registrationDispatcher.Trigger,
	)
	if err != nil {
		return err
	}

	origins, err := web.NewOriginPolicy(os.Getenv("FRONTEND_ORIGIN"))
	if err != nil {
		return err
	}
	tokenVerifier, err := auth.NewSupabaseVerifier(
		ctx,
		os.Getenv("SUPABASE_URL"),
		os.Getenv("BETA_ALLOWED_EMAILS"),
	)
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

	classifier, err := openai.NewClassifier(
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_ROUTER_MODEL"),
	)
	if err != nil {
		return err
	}

	transcriber, err := stt.NewDeepgramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
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
	reminderTool, err := reminder.NewTool(reminderStore)
	if err != nil {
		return err
	}
	webSearchTool, err := websearch.New(os.Getenv("TAVILY_API_KEY"))
	if err != nil {
		return err
	}
	watchTool, err := watch.NewTool(watchStore)
	if err != nil {
		return err
	}
	for _, candidate := range []tool.Tool{locationTool, reminderTool, watchTool, webSearchTool} {
		if err := toolRegistry.Register(candidate); err != nil {
			return err
		}
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
	conversationManager, err := session.NewConversationManager(
		sessionStore,
		agent,
	)
	if err != nil {
		return err
	}
	assistantService, err := assistant.NewService(
		activityRouter,
		agent,
		memoryStore,
		conversationManager,
		proposalConfirmer,
	)
	if err != nil {
		return err
	}

	realtimeHub := realtime.NewHub()
	notificationService, err := notification.NewService(notificationStore, realtimeHub)
	if err != nil {
		return err
	}
	reminderDispatcher, err := reminder.NewDispatcher(reminderStore, notificationService)
	if err != nil {
		return err
	}
	watchDispatcher, err := watch.NewDispatcher(
		watchStore,
		watch.SearchFunc(func(ctx context.Context, query string) ([]watch.Item, error) {
			results, err := webSearchTool.SearchNews(ctx, query)
			if err != nil {
				return nil, err
			}
			items := make([]watch.Item, 0, len(results))
			for _, result := range results {
				items = append(items, watch.Item{Title: result.Title, URL: result.URL})
			}
			return items, nil
		}),
		notificationService,
	)
	if err != nil {
		return err
	}
	scheduledEventDispatcher, err := scheduler.NewDispatcher(
		scheduledEventStore,
		reminderDispatcher,
		watchDispatcher,
	)
	if err != nil {
		return err
	}
	schedulerHandler, err := scheduler.NewHandler(
		os.Getenv("SCHEDULER_SECRET"),
		scheduledEventDispatcher,
	)
	if err != nil {
		return err
	}
	go registrationDispatcher.Run(ctx)
	go scheduledEventDispatcher.Run(ctx)
	realtimeServer := realtime.NewServerWithHub(transcriber, realtimeHub, realtime.Handlers{
		Authenticate: tickets.Consume,
		CheckOrigin:  origins.Allows,
		Connect: func(ctx context.Context, scope tool.Scope) error {
			return notificationService.Flush(ctx, scope.UserID)
		},
		NotificationAck: func(
			ctx context.Context,
			scope tool.Scope,
			notificationID string,
		) error {
			return notificationService.Acknowledge(ctx, scope.UserID, notificationID)
		},
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
	mux.HandleFunc("GET /health", web.Health)
	mux.Handle("/auth/ws-ticket", origins.Handler(ticketHandler))
	mux.Handle("/internal/scheduler/run", schedulerHandler)
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
