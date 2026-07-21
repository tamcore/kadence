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
	"github.com/tamcore/kadence/internal/chat/skill"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/embed"
	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/mcp"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/reindex"
	"github.com/tamcore/kadence/internal/secret"
	"github.com/tamcore/kadence/internal/store"
	"github.com/tamcore/kadence/internal/webauthn"
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

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()})))

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	startupCtx, cancel := context.WithTimeout(rootCtx, startupTimeout)
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
	deps.Profile = handlers.NewProfile(users, sessions, cfg)
	deps.SessionsAPI = handlers.NewSessions(sessions)

	webauthnCreds := store.NewWebAuthnCredentialRepository(pool)
	var waSvc *webauthn.Service
	var waCipher *crypto.Cipher
	if cfg.WebAuthnEnabled() {
		s, wErr := webauthn.NewService(cfg)
		if wErr != nil {
			return fmt.Errorf("webauthn service: %w", wErr)
		}
		waSvc = s
		c, cErr := crypto.NewCipher(cfg.EncryptionKey)
		if cErr != nil {
			return fmt.Errorf("webauthn cipher: %w", cErr)
		}
		waCipher = c
	}
	deps.WebAuthn = handlers.NewWebAuthn(waSvc, webauthnCreds, users, sessions, waCipher, cfg)

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
			chunkRepo := store.NewChunkRepository(pool, cfg.EmbedModel)
			rag = chat.NewRAG(embedder, chunkRepo, cfg.RAGTopK)
			slog.Info("rag enabled", "model", cfg.EmbedModel, "base_url", cfg.EmbedBaseURL, "top_k", cfg.RAGTopK)
			go reindex.Run(rootCtx, chunkRepo, embedder.Embed, slog.Default())

			docsRepo := store.NewDocumentRepository(pool)
			extractors := buildIngestExtractors(cfg)
			ingestSvc := ingest.NewService(
				extractors,
				embedder, docsRepo, chunkRepo, cfg.IngestChunkChars,
			)
			deps.Documents = handlers.NewDocuments(ingestSvc, docsRepo, cfg.UploadMaxBytes)
			deps.Context = handlers.NewContext(chunkRepo, docsRepo)
		}
		mcpHTTPClient, err := mcp.HTTPClientWithCA(cfg.MCPCAFile)
		if err != nil {
			return fmt.Errorf("mcp CA client: %w", err)
		}

		var mcpTools chat.MCPTools // nil interface = disabled

		var userSrc mcp.UserServerSource
		var userRepo *store.UserServerRepo
		if cfg.UserMCPEnabled() {
			cipher, cErr := crypto.NewCipher(cfg.EncryptionKey)
			if cErr != nil {
				return fmt.Errorf("user mcp cipher: %w", cErr)
			}
			userRepo = store.NewUserServerRepo(pool, cipher)
			userSrc = userRepo
		}

		servers, sErr := mcp.ServersFromEnv(os.Environ())
		if sErr != nil {
			slog.Warn("failed to parse MCP env, continuing without env tools", "err", sErr)
		}
		if len(servers) > 0 || userSrc != nil {
			registry := mcp.NewRegistry(servers, mcpHTTPClient, userSrc)
			mcpTools = registry
			slog.Info("mcp enabled", "env_servers", len(servers), "user_mcp", userSrc != nil)

			poller := mcp.NewHealthPoller(registry, mcp.DefaultHealthInterval)
			go poller.Run(rootCtx)

			// userRepo is a *store.UserServerRepo; passed as nil explicitly
			// when unset to avoid handing NewMCP a non-nil interface wrapping
			// a nil pointer (which would make h.store != nil checks pass
			// incorrectly).
			if userRepo != nil {
				deps.MCP = handlers.NewMCP(poller, userRepo, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled())
			} else {
				deps.MCP = handlers.NewMCP(poller, nil, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled())
			}
		}
		skills, err := skill.Load()
		if err != nil {
			return fmt.Errorf("load skills: %w", err)
		}
		// Single broker instance for the process: shared by the chat service
		// (request_credentials tool + substitution/redaction) and, in a later
		// phase, the credentials submit endpoint. Do not construct a second one.
		broker := secret.NewBroker()
		chatSvc := chat.NewService(prov, chat.ServiceConfig{
			Model:            cfg.LLMModel,
			MaxTokens:        cfg.LLMMaxTokens,
			Temperature:      cfg.LLMTemperature,
			SystemPrompt:     cfg.SystemPrompt,
			Timeout:          cfg.LLMTimeout,
			MCPMaxIterations: cfg.MCPMaxIterations,
			MCPMaxTools:      cfg.MCPMaxTools,
		}, chat.Deps{
			Convs: convs, Msgs: msgs, Guardrail: guardrail, RAG: rag, MCP: mcpTools, Skills: skills,
			Secrets: broker,
		})
		deps.Chat = handlers.NewChat(chatSvc, convs, msgs)
		deps.Credentials = handlers.NewCredentials(broker)
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

	select {
	case err := <-errCh:
		return err
	case <-rootCtx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel2()
	return srv.Shutdown(shutdownCtx)
}

// buildIngestExtractors returns the document extractors used for RAG
// ingestion. When markitdown-mcp is configured it is preferred (covers PDFs,
// images, and office documents); the built-in PDF extractor is always kept
// as a fallback. A markitdown connection failure is logged and does not
// prevent startup.
func buildIngestExtractors(cfg config.Config) []ingest.Extractor {
	pdf := ingest.NewPDFExtractor()
	if !cfg.MarkitdownEnabled() {
		return []ingest.Extractor{pdf}
	}

	mdHTTPClient, err := mcp.HTTPClientWithCA(cfg.MCPCAFile)
	if err != nil {
		slog.Warn("markitdown extractor unavailable (CA client), falling back to PDF-only ingestion",
			"url", cfg.MarkitdownURL, "err", err)
		return []ingest.Extractor{pdf}
	}

	md, err := ingest.NewMarkitdownExtractor(
		cfg.MarkitdownURL, cfg.MarkitdownAuthUser, cfg.MarkitdownAuthPass, cfg.MarkitdownTransport, mdHTTPClient,
	)
	if err != nil {
		slog.Warn("markitdown extractor unavailable, falling back to PDF-only ingestion",
			"url", cfg.MarkitdownURL, "err", err)
		return []ingest.Extractor{pdf}
	}

	slog.Info("markitdown extractor enabled", "url", cfg.MarkitdownURL)
	return []ingest.Extractor{md, pdf}
}
