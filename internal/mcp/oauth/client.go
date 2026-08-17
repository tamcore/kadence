package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidGrant is the authorization server's invalid_grant answer. On a
// refresh it means the token family is gone: the presented token was already
// consumed, or a replay revoked it. Re-authorization is the only way back —
// retrying is what destroys the family in the first place.
var ErrInvalidGrant = errors.New("oauth: invalid_grant")

// ErrServerFault is a named refusal from the server itself: it answered, with a
// status and an error code, rather than leaving the outcome unknown. A refresh
// that fails this way did not consume the presented token.
var ErrServerFault = errors.New("oauth: the authorization server refused the request")

// The wire parameter names, spelled once.
const (
	paramGrantType    = "grant_type"
	paramCode         = "code"
	paramCodeVerifier = "code_verifier"
	paramRedirectURI  = "redirect_uri"
	paramResource     = "resource"
	paramClientID     = "client_id"
	paramRefreshToken = "refresh_token"
	paramScope        = "scope"
	paramState        = "state"
	paramChallenge    = "code_challenge"
	paramChallengeAlg = "code_challenge_method"
	paramResponseType = "response_type"
	paramToken        = "token"

	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	challengeMethodS256    = "S256"

	// errInvalidGrant is the wire code the authorization server sends when a
	// grant is gone.
	errCodeInvalidGrant = "invalid_grant"
)

// maxBodyBytes bounds every document and error body this package reads.
const maxBodyBytes = 64 << 10

// defaultTimeout applies when the caller supplies no HTTP client.
const defaultTimeout = 30 * time.Second

// Metadata is the deployment's published identity, assembled from the two
// well-known documents.
type Metadata struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RevocationEndpoint    string
	Resource              string
	ScopesSupported       []string
}

// Tokens is one grant's credentials as the token endpoint returned them.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	ExpiresIn    time.Duration
}

// Client talks to one deployment's token and revocation endpoints.
type Client struct {
	http     *http.Client
	md       Metadata
	clientID string
	secret   string
}

// NewClient builds a client for md. A nil httpClient is replaced with a bounded
// default: mcp.HTTPClientWithCA returns nil when no custom CA is configured,
// which is the ordinary deployment.
func NewClient(httpClient *http.Client, md Metadata, clientID, clientSecret string) *Client {
	return &Client{http: normalizeHTTPClient(httpClient), md: md, clientID: clientID, secret: clientSecret}
}

// Metadata returns the deployment identity this client was built for.
func (c *Client) Metadata() Metadata { return c.md }

func normalizeHTTPClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultTimeout}
}

// Discover reads the protected-resource document at the resource's origin and
// then the authorization server's own metadata.
//
// Two refusals are structural rather than cosmetic: a cleartext authorization
// server would publish bearer tokens in the clear, and an issuer that is not
// the origin the document came from is a server naming someone else as its
// authority, which is how token confusion starts.
func Discover(ctx context.Context, httpClient *http.Client, resourceURL string) (Metadata, error) {
	client := normalizeHTTPClient(httpClient)

	parsed, err := url.Parse(strings.TrimSpace(resourceURL))
	if err != nil || parsed.Host == "" {
		return Metadata{}, fmt.Errorf("oauth: resource %q is not an absolute URL", resourceURL)
	}
	origin := parsed.Scheme + "://" + parsed.Host

	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := getJSON(ctx, client, origin+"/.well-known/oauth-protected-resource", &prm); err != nil {
		return Metadata{}, err
	}
	if len(prm.AuthorizationServers) == 0 {
		return Metadata{}, errors.New("oauth: the resource names no authorization server")
	}
	issuer := strings.TrimRight(prm.AuthorizationServers[0], "/")
	if !strings.HasPrefix(issuer, "https://") {
		return Metadata{}, fmt.Errorf("oauth: authorization server %q is not https", issuer)
	}

	var asm struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RevocationEndpoint    string   `json:"revocation_endpoint"`
		ScopesSupported       []string `json:"scopes_supported"`
	}
	if err := getJSON(ctx, client, issuer+"/.well-known/oauth-authorization-server", &asm); err != nil {
		return Metadata{}, err
	}
	if strings.TrimRight(asm.Issuer, "/") != issuer {
		return Metadata{}, fmt.Errorf("oauth: issuer %q is not the origin it was served from", asm.Issuer)
	}
	if asm.AuthorizationEndpoint == "" || asm.TokenEndpoint == "" {
		return Metadata{}, errors.New("oauth: the authorization server publishes no usable endpoints")
	}

	scopes := asm.ScopesSupported
	if len(scopes) == 0 {
		scopes = prm.ScopesSupported
	}
	return Metadata{
		Issuer:                asm.Issuer,
		AuthorizationEndpoint: asm.AuthorizationEndpoint,
		TokenEndpoint:         asm.TokenEndpoint,
		RevocationEndpoint:    asm.RevocationEndpoint,
		Resource:              prm.Resource,
		ScopesSupported:       scopes,
	}, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("oauth: fetch metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth: metadata request answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(into); err != nil {
		return fmt.Errorf("oauth: decode metadata: %w", err)
	}
	return nil
}

// AuthorizeURL is where the browser goes to start an authorization.
func (c *Client) AuthorizeURL(redirectURI, state, challenge string, scopes []string) string {
	q := url.Values{
		paramResponseType: {paramCode},
		paramClientID:     {c.clientID},
		paramRedirectURI:  {redirectURI},
		paramState:        {state},
		paramChallenge:    {challenge},
		paramChallengeAlg: {challengeMethodS256},
		paramResource:     {c.md.Resource},
	}
	if len(scopes) > 0 {
		q.Set(paramScope, strings.Join(scopes, " "))
	}
	sep := "?"
	if strings.Contains(c.md.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return c.md.AuthorizationEndpoint + sep + q.Encode()
}

// Exchange trades an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, code, verifier, redirectURI string) (Tokens, error) {
	return c.token(ctx, url.Values{
		paramGrantType:    {grantAuthorizationCode},
		paramCode:         {code},
		paramCodeVerifier: {verifier},
		paramRedirectURI:  {redirectURI},
		paramResource:     {c.md.Resource},
	})
}

// Refresh rotates the grant. The presented token is consumed whatever the
// outcome, so a caller must never retry with the same one.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.token(ctx, url.Values{
		paramGrantType:    {grantRefreshToken},
		paramRefreshToken: {refreshToken},
		paramResource:     {c.md.Resource},
	})
}

// Revoke asks the server to drop a token. An already-dead token is the desired
// end state, so RFC 7009's invalid_token answer counts as success.
func (c *Client) Revoke(ctx context.Context, token string) error {
	if c.md.RevocationEndpoint == "" {
		return errors.New("oauth: the deployment publishes no revocation endpoint")
	}
	resp, err := c.post(ctx, c.md.RevocationEndpoint, url.Values{paramToken: {token}})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 400 {
		return nil
	}
	code, err := errorCode(resp)
	if err != nil {
		return err
	}
	if code == "invalid_token" {
		return nil
	}
	return fmt.Errorf("oauth: revocation answered %d (%s)", resp.StatusCode, code)
}

func (c *Client) token(ctx context.Context, form url.Values) (Tokens, error) {
	resp, err := c.post(ctx, c.md.TokenEndpoint, form)
	if err != nil {
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		code, decodeErr := errorCode(resp)
		if decodeErr != nil {
			return Tokens{}, decodeErr
		}
		if code == errCodeInvalidGrant {
			return Tokens{}, ErrInvalidGrant
		}
		return Tokens{}, fmt.Errorf("%w: token endpoint answered %d (%s)", ErrServerFault, resp.StatusCode, code)
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return Tokens{}, fmt.Errorf("oauth: decode token response: %w", err)
	}
	if body.AccessToken == "" {
		return Tokens{}, errors.New("oauth: token response carries no access token")
	}
	return Tokens{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		Scope:        body.Scope,
		ExpiresIn:    time.Duration(body.ExpiresIn) * time.Second,
	}, nil
}

func (c *Client) post(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	if c.secret == "" {
		// A public client identifies itself in the body; the deployment
		// advertises "none" as a token endpoint auth method.
		form.Set(paramClientID, c.clientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.secret != "" {
		req.SetBasicAuth(url.QueryEscape(c.clientID), url.QueryEscape(c.secret))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: request %s: %w", endpointName(endpoint), err)
	}
	return resp, nil
}

// errorCode reads the RFC 6749 error code and deliberately drops the
// description: it is server-controlled text that can quote a credential.
func errorCode(resp *http.Response) (string, error) {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("oauth: endpoint answered %d with an undecodable body", resp.StatusCode)
	}
	if body.Error == "" {
		return "unspecified", nil
	}
	return body.Error, nil
}

// endpointName strips the query from a URL before it reaches an error string.
func endpointName(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Scheme + "://" + u.Host + u.Path
	}
	return "the authorization server"
}
