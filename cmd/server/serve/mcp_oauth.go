package serve

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tamcore/kadence/internal/api"
	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/config"
	"github.com/tamcore/kadence/internal/crypto"
	"github.com/tamcore/kadence/internal/mcp"
	"github.com/tamcore/kadence/internal/mcp/oauth"
	"github.com/tamcore/kadence/internal/store"
)

// callbackPath is where the authorization server sends the browser back. It is
// appended to the configured public origin and must match the registered
// redirect URI byte for byte.
const callbackPath = "/api/mcp/oauth/callback"

// discoveryTimeout bounds one metadata fetch. A server that accepts the
// connection and then goes quiet must not stall start-up or a request.
const discoveryTimeout = 10 * time.Second

// discovererFor returns the retry path for an integration whose metadata could
// not be read yet: the service calls it the next time that integration is
// needed, so a server that was down at boot recovers without a restart.
func discovererFor(servers []mcp.Server, httpClient *http.Client) oauth.Discoverer {
	byID := make(map[string]mcp.Server, len(servers))
	for _, s := range servers {
		byID[s.IntegrationID()] = s
	}
	return func(ctx context.Context, serverID string) (*oauth.Client, error) {
		s, ok := byID[serverID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", oauth.ErrUnknownServer, serverID)
		}
		dctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		defer cancel()
		md, err := oauth.Discover(dctx, httpClient, s.OAuthResource)
		if err != nil {
			return nil, err
		}
		slog.Info("mcp oauth: integration discovered on demand", "server", serverID, "issuer", md.Issuer)
		return oauth.NewClient(httpClient, md, s.OAuthClientID, s.OAuthClientSecret), nil
	}
}

// mcpHandlerDeps is what the MCP handlers need from Run's scope. It exists to
// keep Run's own branching bounded, not as an abstraction.
type mcpHandlerDeps struct {
	cfg        config.Config
	pool       *pgxpool.Pool
	servers    []mcp.Server
	httpClient *http.Client
	registry   *mcp.Registry
	users      *store.UserRepository
	userRepo   *store.UserServerRepo
	poller     *mcp.HealthPoller
}

// attachMCPHandlers wires the MCP health/CRUD handler and, when any server uses
// OAuth, the per-user link handler.
func attachMCPHandlers(ctx context.Context, deps *api.Deps, d mcpHandlerDeps) error {
	cfg := d.cfg
	// userRepo is a *store.UserServerRepo; passed as nil explicitly when unset
	// to avoid handing NewMCP a non-nil interface wrapping a nil pointer, which
	// would make its store != nil checks pass incorrectly.
	if d.userRepo != nil {
		deps.MCP = handlers.NewMCP(d.poller, d.userRepo, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled(), cfg.UserMCPMaxServers)
	} else {
		deps.MCP = handlers.NewMCP(d.poller, nil, cfg.UserMCPAllowedHosts, cfg.UserMCPEnabled(), cfg.UserMCPMaxServers)
	}

	oauthSvc, err := setupMCPOAuth(ctx, cfg, d.pool, d.servers, d.httpClient, d.registry, d.users)
	if err != nil {
		return err
	}
	if oauthSvc != nil {
		deps.MCPOAuth = handlers.NewMCPOAuth(oauthSvc, cfg.IsProd())
	}
	return nil
}

// userPrincipals resolves a username to the immutable user id a stored
// authorization is filed under.
type userPrincipals struct{ users *store.UserRepository }

func (p userPrincipals) UserIDFor(ctx context.Context, username string) (int64, error) {
	u, err := p.users.GetByUsername(ctx, username)
	if err != nil {
		return 0, fmt.Errorf("resolve user %q: %w", username, err)
	}
	return u.ID, nil
}

// setupMCPOAuth builds the per-user OAuth link service for every MCP server
// configured with AUTH_MODE=oauth and wires it into the registry.
//
// It returns nil when no server uses OAuth. Discovery is a network call to a
// server that may legitimately be down at boot, so a failure there is logged
// and that one server is left without a client: it then behaves exactly like a
// server nobody has linked, rather than stopping Kadence from starting.
func setupMCPOAuth(
	ctx context.Context, cfg config.Config, pool *pgxpool.Pool, servers []mcp.Server,
	httpClient *http.Client, registry *mcp.Registry, users *store.UserRepository,
) (*oauth.Service, error) {
	var oauthServers []mcp.Server
	for _, s := range servers {
		if s.PerPrincipal() {
			oauthServers = append(oauthServers, s)
		}
	}
	if len(oauthServers) == 0 {
		return nil, nil
	}

	cipher, err := crypto.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("mcp oauth cipher: %w", err)
	}

	clients := make(map[string]*oauth.Client, len(oauthServers))
	scopes := make(map[string][]string, len(oauthServers))
	for _, s := range oauthServers {
		id := s.IntegrationID()
		scopes[id] = s.OAuthScopes

		// Bounded: a server that accepts the connection and then never answers
		// must not hold up the whole process's start-up.
		dctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		md, dErr := oauth.Discover(dctx, httpClient, s.OAuthResource)
		cancel()
		if dErr != nil {
			slog.Warn("mcp oauth: discovery failed at boot; it will be retried on first use",
				"server", id, "err", dErr)
			continue
		}
		clients[id] = oauth.NewClient(httpClient, md, s.OAuthClientID, s.OAuthClientSecret)
		slog.Info("mcp oauth: integration ready", "server", id, "issuer", md.Issuer)
	}

	svc := oauth.NewService(store.NewMCPOAuthRepo(pool, cipher), clients,
		cfg.PublicURL+callbackPath, scopes, nil)
	// Discovery is retried on demand, so a server that was down at boot becomes
	// usable without a restart.
	svc.SetDiscoverer(discovererFor(oauthServers, httpClient))
	registry.SetPrincipalSource(userPrincipals{users: users})
	registry.SetTokenSource(svc)
	return svc, nil
}
