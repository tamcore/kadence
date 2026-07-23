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
	"sync"
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
	// shutdownTimeout bounds how long srv.Shutdown waits for in-flight
	// requests to finish, including long-lived SSE chat streams which can run
	// up to cfg.LLMTimeout (default 300s) — 310s keeps a 10s margin.
	shutdownTimeout = 310 * time.Second
	// idleTimeout bounds how long a keep-alive connection may sit idle
	// between requests before the server closes it.
	idleTimeout = 120 * time.Second
	// goroutineDrainTimeout bounds how long Run waits, after srv.Shutdown
	// returns, for background goroutines (reindex worker, MCP health poller,
	// session reaper) to observe rootCtx cancellation and exit, before
	// proceeding to close the DB pool regardless.
	goroutineDrainTimeout = 15 * time.Second
	startupTimeout        = 30 * time.Second
)

// mcpToolsAdapter satisfies chat.MCPTools over *mcp.Registry. It exists only
// because chat.MCPTools.SnapshotFor must return the chat package's own
// MCPUserSnapshot interface (to keep the chat package decoupled from mcp),
// while mcp.Registry.SnapshotFor returns the concrete *mcp.UserSnapshot —
// Go's interface satisfaction requires an exact return-type match, so this
// thin wiring-layer wrapper bridges the two.
type mcpToolsAdapter struct{ reg *mcp.Registry }

func (a mcpToolsAdapter) Enabled() bool { return a.reg.Enabled() }

func (a mcpToolsAdapter) SnapshotFor(ctx context.Context, username string) chat.MCPUserSnapshot {
	return mcpSnapshotAdapter{a.reg.SnapshotFor(ctx, username)}
}

// mcpSnapshotAdapter satisfies chat.MCPUserSnapshot over *mcp.UserSnapshot.
type mcpSnapshotAdapter struct{ snap *mcp.UserSnapshot }

func (a mcpSnapshotAdapter) ToolsFor(ctx context.Context) ([]provider.ToolDefinition, error) {
	return a.snap.ToolsFor(ctx)
}

func (a mcpSnapshotAdapter) Call(ctx context.Context, toolName, argsJSON string) (string, error) {
	return a.snap.Call(ctx, toolName, argsJSON)
}

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

	var bgWG sync.WaitGroup

	bgWG.Go(func() {
		runSessionReaper(rootCtx, sessions, sessionReapInterval, slog.Default())
	})

	deps := api.Deps{Users: users, Sessions: sessions, Config: cfg}
	deps.Profile = handlers.NewProfile(users, sessions, cfg)
	deps.SessionsAPI = handlers.NewSessions(sessions)

	webauthnCreds := store.NewWebAuthnCredentialRepository(pool)
	var waSvc *webauthn.Service
	var waCipher *crypto.Cipher
	if cfg.WebAuthnEnabled() {
		// cfg.Validate() (called above) already fails fast on the same
		// preconditions (TrustedOrigins set, valid 32-byte EncryptionKey) as
		// a config-level error; the branches below only fire for a
		// downstream construction failure Validate() can't anticipate (e.g.
		// go-webauthn itself rejecting a malformed RPID).
		s, wErr := webauthn.NewService(cfg)
		if wErr != nil {
			return fmt.Errorf(
				"passkeys enabled (KADENCE_WEBAUTHN_RP_ID set) but webauthn service failed "+
					"to initialize; also requires KADENCE_TRUSTED_ORIGINS: %w", wErr)
		}
		waSvc = s
		c, cErr := crypto.NewCipher(cfg.EncryptionKey)
		if cErr != nil {
			return fmt.Errorf(
				"passkeys enabled (KADENCE_WEBAUTHN_RP_ID set) but cipher failed to initialize; "+
					"also requires a 32-byte KADENCE_ENCRYPTION_KEY: %w", cErr)
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
			embedder := embed.NewOpenAICompat(cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel, cfg.EmbedDimensions)
			chunkRepo := store.NewChunkRepository(pool, cfg.EmbedModel)
			rag = chat.NewRAG(embedder, chunkRepo, cfg.RAGTopK)
			slog.Info("rag enabled", "model", cfg.EmbedModel, "base_url", cfg.EmbedBaseURL, "top_k", cfg.RAGTopK)
			bgWG.Go(func() {
				reindex.Run(rootCtx, chunkRepo, embedder.Embed, slog.Default())
			})

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
			mcpTools = mcpToolsAdapter{registry}
			slog.Info("mcp enabled", "env_servers", len(servers), "user_mcp", userSrc != nil)

			poller := mcp.NewHealthPoller(registry, mcp.DefaultHealthInterval)
			bgWG.Go(func() {
				poller.Run(rootCtx)
			})

			// userRepo is a *store.UserServerRepo; passed as nil explicitly
			// when unset to avoid handing NewMCP a non-nil interface wrapping
			// a nil pointer (which would make h.store != nil checks pass
			// incorrectly).
			if userRepo != nil {
				deps.MCP = handlers.NewMCP(poller, userRepo, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled(), cfg.UserMCPMaxServers)
			} else {
				deps.MCP = handlers.NewMCP(poller, nil, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled(), cfg.UserMCPMaxServers)
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
			Model:               cfg.LLMModel,
			MaxTokens:           cfg.LLMMaxTokens,
			Temperature:         cfg.LLMTemperature,
			SystemPrompt:        cfg.SystemPrompt,
			Timeout:             cfg.LLMTimeout,
			MCPMaxIterations:    cfg.MCPMaxIterations,
			MCPMaxTools:         cfg.MCPMaxTools,
			ContextBudgetTokens: cfg.LLMContextBudgetTokens,
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
		IdleTimeout:       idleTimeout,
	}

	// healthSrv is a second, dedicated listener that serves only GET
	// /healthz. It starts before the main server and is shut down LAST (see
	// below) so kubelet's liveness probe — pointed at this listener, not the
	// main one — never sees a failure while the main server is draining
	// in-flight requests. The main listener's /api/healthz remains the
	// readiness probe target: readiness failing during drain is the whole
	// point (it removes the pod from Service endpoints).
	healthSrv := newHealthServer(cfg.HealthAddr)

	errCh := make(chan error, 1)
	startHealthServer(healthSrv, errCh)
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

	shutdownErr := shutdownServer(srv, shutdownTimeout)
	if shutdownErr != nil {
		slog.Warn("graceful shutdown deadline exceeded", "err", shutdownErr)
	}

	// rootCtx is already cancelled (signal.NotifyContext fired it at signal
	// time, which is what unblocked the select above and is what the
	// background goroutines below are watching) — nothing left to cancel
	// here. What remains is waiting for those goroutines to actually have
	// exited before returning, so the deferred store.Close(pool) above never
	// runs while one of them is still mid-query against the pool.
	// stopSignals is deferred rather than called here: rootCtx has already
	// done its job, and calling it again would only be a redundant release
	// of signal.NotifyContext's internal resources ahead of the func return.
	if drainGoroutines(&bgWG, goroutineDrainTimeout, slog.Default()) {
		slog.Warn("proceeding with shutdown despite goroutine drain timeout")
	}

	// The health listener is shut down last, after the main server has
	// finished draining AND background goroutines have exited, so liveness
	// stays green for the full drain window regardless of how long either
	// step takes.
	if err := shutdownServer(healthSrv, shutdownTimeout); err != nil {
		slog.Warn("health listener shutdown deadline exceeded", "err", err)
	}

	return shutdownErr
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
