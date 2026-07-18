// Package serve runs the Kadence HTTP server with graceful shutdown.
package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/embed"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
	startupTimeout    = 30 * time.Second
)

// Run starts the HTTP server and blocks until SIGINT/SIGTERM.
func Run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	startupCtx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	pool, err := store.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close(pool)

	if err := store.Migrate(startupCtx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	users := store.NewUserRepository(pool)
	sessions := store.NewSessionRepository(pool)

	if err := auth.BootstrapAdmin(startupCtx, users, cfg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	deps := api.Deps{Users: users, Sessions: sessions, Config: cfg}

	if cfg.ChatEnabled() {
		convs := store.NewConversationRepository(pool)
		msgs := store.NewMessageRepository(pool)
		prov := provider.NewOpenAICompat(cfg.LLMBaseURL, cfg.LLMAPIKey)
		var guardrail *chat.Guardrail
		if cfg.GuardrailEnabled {
			gProv := provider.NewOpenAICompat(cfg.ResolvedGuardrailBaseURL(), cfg.ResolvedGuardrailAPIKey())
			guardrail = chat.NewGuardrail(gProv, chat.GuardrailConfig{
				Model:          cfg.ResolvedGuardrailModel(),
				DomainName:     cfg.DomainName,
				AllowedTopics:  cfg.AllowedTopics,
				RefusalMessage: cfg.RefusalMessage,
				HistoryWindow:  cfg.GuardrailHistoryWindow,
			})
			slog.Info("guardrail enabled", "model", cfg.ResolvedGuardrailModel(), "base_url", cfg.ResolvedGuardrailBaseURL())
		}
		var rag *chat.RAG
		if cfg.RAGEnabled() {
			embedder := embed.NewOpenAICompat(cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
			rag = chat.NewRAG(embedder, store.NewChunkRepository(pool), cfg.RAGTopK)
			slog.Info("rag enabled", "model", cfg.EmbedModel, "base_url", cfg.EmbedBaseURL, "top_k", cfg.RAGTopK)
		}
		chatSvc := chat.NewService(prov, chat.ServiceConfig{
			Model:        cfg.LLMModel,
			MaxTokens:    cfg.LLMMaxTokens,
			Temperature:  cfg.LLMTemperature,
			SystemPrompt: cfg.SystemPrompt,
			Timeout:      cfg.LLMTimeout,
		}, convs, msgs, guardrail, rag)
		deps.Chat = handlers.NewChat(chatSvc, convs, msgs)
		slog.Info("chat enabled", "model", cfg.LLMModel, "base_url", cfg.LLMBaseURL)
	} else {
		slog.Info("chat disabled (KADENCE_LLM_API_KEY not set)")
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", cfg.ListenAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		slog.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel2()
	return srv.Shutdown(shutdownCtx)
}
