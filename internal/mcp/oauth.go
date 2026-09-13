package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/oauth"
)

// ServerAuthOAuth is the value of an MCP server's auth field that selects the
// OAuth 2.0 + PKCE authorization-code flow.
const ServerAuthOAuth = "oauth"

const defaultLoginTimeout = 3 * time.Minute

// MCP OAuth delegates its transport/identity-agnostic engine to internal/oauth
// (PKCE, RFC 8414 discovery, authorize-URL build, token exchange/refresh) so the
// two share one implementation. MCP keeps its own LoginOptions/Login
// orchestration, OAuthConfig/StoredToken types, loopback handling, CLI, and
// on-disk token format — all behavior-preserving. The shared engine also adds an
// https-only token-endpoint guard (loopback exempt), a hardening MCP inherits.

// pkceToParams converts the shared PKCE pair to MCP's local type.
func pkceToParams(p oauth.PKCE) pkceParams {
	return pkceParams{Verifier: p.Verifier, Challenge: p.Challenge, Method: p.Method}
}

// pkceToOAuth converts MCP's local PKCE type back to the shared type.
func pkceToOAuth(p pkceParams) oauth.PKCE {
	return oauth.PKCE{Verifier: p.Verifier, Challenge: p.Challenge, Method: p.Method}
}

// configFor builds the shared oauth.Config from MCP's OAuthConfig + resolved
// endpoints for a flow. The resolved resource indicator (config override or
// discovered) is threaded through so authorize/token requests carry RFC 8707.
func configFor(cfg OAuthConfig, metadata authServerMetadata) oauth.Config {
	resource := strings.TrimSpace(cfg.Resource)
	if resource == "" {
		resource = strings.TrimSpace(metadata.Resource)
	}
	return oauth.Config{
		ClientID:              cfg.ClientID,
		ClientSecret:          cfg.ClientSecret,
		Scopes:                cfg.Scopes,
		AuthorizationEndpoint: metadata.AuthorizationEndpoint,
		TokenEndpoint:         metadata.TokenEndpoint,
		Resource:              resource,
	}
}

// tokenToStored converts a shared oauth.Token to MCP's StoredToken (MCP does not
// use the Account field).
func tokenToStored(t oauth.Token) StoredToken {
	return StoredToken{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Scopes:       t.Scopes,
		ExpiresAt:    t.ExpiresAt,
	}
}

// OAuthConfig describes how to authenticate to a remote MCP server using OAuth.
// Endpoints may be discovered from the server's metadata document; explicit
// values here override or fill in anything discovery cannot provide.
type OAuthConfig struct {
	ClientID              string
	ClientSecret          string
	Scopes                []string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	// IssuerURL overrides the base URL used for metadata discovery. When empty
	// the MCP server URL is used.
	IssuerURL string
	// RedirectURI pins the loopback redirect URI (some servers require a
	// pre-registered exact URI). Empty means an ephemeral 127.0.0.1 port.
	RedirectURI string
	// CallbackPort binds the loopback listener to a fixed port; ignored when
	// RedirectURI is set.
	CallbackPort int
	// Resource is the RFC 8707 resource indicator; empty means use the server URL
	// or the value advertised by protected-resource metadata.
	Resource string
}

// authServerMetadata is the subset of the OAuth 2.0 authorization server
// metadata document that the flow consumes.
type authServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	// Resource is the RFC 8707 resource indicator learned from protected-resource
	// metadata. Empty means "not advertised".
	Resource string `json:"resource,omitempty"`
}

// pkceParams holds a PKCE verifier/challenge pair.
type pkceParams struct {
	Verifier  string
	Challenge string
	Method    string
}

// LoginOptions configures a single interactive authorization-code login.
type LoginOptions struct {
	ServerName string
	ServerURL  string
	Config     OAuthConfig
	HTTPClient *http.Client
	// OpenBrowser is invoked with the authorization URL. The default prints the
	// URL; tests inject a function that drives the loopback redirect.
	OpenBrowser func(authURL string) error
	Timeout     time.Duration
	Now         func() time.Time
}

// authorizationFlow carries the per-login state shared across helpers.
type authorizationFlow struct {
	httpClient *http.Client
	metadata   authServerMetadata
	config     OAuthConfig
	pkce       pkceParams
	state      string
	now        func() time.Time
}

// discoverAuthorizationServer fetches the RFC 8414 authorization server metadata
// at the well-known path under baseURL, via the shared engine.
func discoverAuthorizationServer(ctx context.Context, client *http.Client, baseURL string) (authServerMetadata, error) {
	meta, err := oauth.DiscoverAuthorizationServer(ctx, client, baseURL)
	if err != nil {
		return authServerMetadata{}, err
	}
	return authServerMetadata{
		Issuer:                meta.Issuer,
		AuthorizationEndpoint: meta.AuthorizationEndpoint,
		TokenEndpoint:         meta.TokenEndpoint,
		RegistrationEndpoint:  meta.RegistrationEndpoint,
		ScopesSupported:       meta.ScopesSupported,
	}, nil
}

// discoverIssuerServer resolves authorization-server metadata for an issuer,
// trying RFC 8414 first and the OIDC openid-configuration fallback second.
func discoverIssuerServer(ctx context.Context, client *http.Client, issuer string) (authServerMetadata, error) {
	meta, err := oauth.DiscoverIssuerMetadata(ctx, client, issuer)
	if err != nil {
		return authServerMetadata{}, err
	}
	return authServerMetadata{
		Issuer:                meta.Issuer,
		AuthorizationEndpoint: meta.AuthorizationEndpoint,
		TokenEndpoint:         meta.TokenEndpoint,
		RegistrationEndpoint:  meta.RegistrationEndpoint,
		ScopesSupported:       meta.ScopesSupported,
	}, nil
}

// protectedResourceChallenge POSTs an unauthenticated MCP initialize to the
// server URL and returns the resource_metadata URL and scopes from the server's
// 401 WWW-Authenticate challenge. This is how a client learns where a modern
// remote MCP server's authorization server lives without any configured
// endpoints.
func protectedResourceChallenge(ctx context.Context, client *http.Client, serverURL string) (string, []string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kajicode","version":"0"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, strings.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		return "", nil, fmt.Errorf("MCP OAuth discovery probe failed for %s: %w", serverURL, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
		return "", nil, fmt.Errorf("MCP OAuth discovery probe for %s returned HTTP %d (expected 401 to advertise OAuth)", serverURL, response.StatusCode)
	}
	metadataURL, scopes := oauth.ParseResourceMetadataChallenge(response.Header.Get("WWW-Authenticate"))
	if strings.TrimSpace(metadataURL) == "" {
		return "", nil, fmt.Errorf("MCP server %s requires OAuth but its 401 challenge advertised no resource_metadata URL", serverURL)
	}
	return metadataURL, scopes, nil
}

// resolveProtectedResource follows the MCP authorization discovery chain: an
// exact challenge URL when provided, otherwise RFC 9728 protected-resource
// metadata for the server URL. It returns the resource indicator, the first
// advertised authorization server, and the resource's supported scopes.
func resolveProtectedResource(ctx context.Context, client *http.Client, serverURL, challengeURL string) (resource string, authorizationServer string, scopes []string, err error) {
	var metadata oauth.ProtectedResourceMetadata
	if strings.TrimSpace(challengeURL) != "" {
		metadata, err = oauth.FetchProtectedResourceMetadata(ctx, client, challengeURL)
	} else {
		metadata, err = oauth.DiscoverProtectedResource(ctx, client, serverURL)
	}
	if err != nil {
		return "", "", nil, err
	}
	resource = strings.TrimSpace(metadata.Resource)
	if resource == "" {
		resource = strings.TrimSpace(serverURL)
	}
	for _, server := range metadata.AuthorizationServers {
		if server = strings.TrimSpace(server); server != "" {
			authorizationServer = server
			break
		}
	}
	if authorizationServer == "" {
		authorizationServer = resource
	}
	return resource, authorizationServer, trimScopeList(metadata.ScopesSupported), nil
}

func trimScopeList(scopes []string) []string {
	trimmed := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			trimmed = append(trimmed, scope)
		}
	}
	return trimmed
}

// validateResolvedEndpoints applies the shared https/loopback endpoint rule to
// every non-empty endpoint in the resolved metadata — configured or discovered
// alike — so discovery metadata can never downgrade an MCP login to an insecure
// or attacker-controlled authorization, token, or registration endpoint.
func validateResolvedEndpoints(metadata authServerMetadata) error {
	for _, ep := range []struct{ kind, url string }{
		{"authorization", metadata.AuthorizationEndpoint},
		{"token", metadata.TokenEndpoint},
		{"registration", metadata.RegistrationEndpoint},
	} {
		if strings.TrimSpace(ep.url) == "" {
			continue
		}
		if err := oauth.ValidateEndpointURL(ep.url); err != nil {
			return fmt.Errorf("mcp oauth: %s endpoint: %w", ep.kind, err)
		}
	}
	return nil
}

// resolveAuthorizationServer discovers metadata by walking the MCP
// authorization chain and applies explicit config overrides. The order is:
//
//  1. Fully-specified config endpoints win immediately (no network).
//  2. The server's 401 WWW-Authenticate challenge names the protected-resource
//     metadata URL (RFC 9728), which names the authorization server.
//  3. Otherwise RFC 9728 metadata is discovered from the server URL directly.
//  4. The authorization server's metadata is read via RFC 8414, with an OIDC
//     openid-configuration fallback.
//  5. Explicit config endpoints always overlay whatever discovery produced.
//
// Configured endpoints remain the fallback whenever discovery fails or omits a
// value, so existing explicit configs keep working unchanged.
func resolveAuthorizationServer(ctx context.Context, client *http.Client, baseURL string, cfg OAuthConfig) (authServerMetadata, error) {
	// When the config supplies both the authorization and token endpoints
	// directly, skip network discovery entirely: there is nothing to discover,
	// and a hung/blocked discovery call (e.g. offline, or an unreachable issuer
	// in tests) must not gate an otherwise fully-specified login. Discovery stays
	// the fallback for any endpoint the config leaves blank.
	if strings.TrimSpace(cfg.AuthorizationEndpoint) != "" && strings.TrimSpace(cfg.TokenEndpoint) != "" {
		metadata := authServerMetadata{
			AuthorizationEndpoint: strings.TrimSpace(cfg.AuthorizationEndpoint),
			TokenEndpoint:         strings.TrimSpace(cfg.TokenEndpoint),
			RegistrationEndpoint:  strings.TrimSpace(cfg.RegistrationEndpoint),
			Resource:              strings.TrimSpace(cfg.Resource),
		}
		if err := validateResolvedEndpoints(metadata); err != nil {
			return authServerMetadata{}, err
		}
		return metadata, nil
	}

	metadata := discoverEndpointsViaChain(ctx, client, baseURL, cfg)

	// Scopes: a configured list wins; otherwise fall back to what the protected
	// resource advertised, so Slack-style servers work with no scope config.
	if len(cfg.Scopes) == 0 && len(metadata.ScopesSupported) > 0 {
		cfg.Scopes = metadata.ScopesSupported
	}

	if endpoint := strings.TrimSpace(cfg.AuthorizationEndpoint); endpoint != "" {
		metadata.AuthorizationEndpoint = endpoint
	}
	if endpoint := strings.TrimSpace(cfg.TokenEndpoint); endpoint != "" {
		metadata.TokenEndpoint = endpoint
	}
	if endpoint := strings.TrimSpace(cfg.RegistrationEndpoint); endpoint != "" {
		metadata.RegistrationEndpoint = endpoint
	}

	if strings.TrimSpace(metadata.AuthorizationEndpoint) == "" {
		return authServerMetadata{}, fmt.Errorf("no authorization endpoint discovered or configured for %s", discoveryDescription(baseURL, cfg))
	}
	if strings.TrimSpace(metadata.TokenEndpoint) == "" {
		return authServerMetadata{}, fmt.Errorf("no token endpoint discovered or configured for %s", discoveryDescription(baseURL, cfg))
	}
	if err := validateResolvedEndpoints(metadata); err != nil {
		return authServerMetadata{}, err
	}
	return metadata, nil
}

// discoverEndpointsViaChain runs the RFC 9728 + RFC 8414 discovery steps and
// returns whatever it learned. Failures along the way are non-fatal: an empty
// result still lets explicit config endpoints satisfy the flow.
func discoverEndpointsViaChain(ctx context.Context, client *http.Client, serverURL string, cfg OAuthConfig) authServerMetadata {
	// An explicit IssuerURL short-circuits discovery: the caller has already told
	// us which authorization server to read.
	if issuer := strings.TrimSpace(cfg.IssuerURL); issuer != "" {
		if metadata, err := discoverIssuerServer(ctx, client, issuer); err == nil {
			return metadata
		}
	}

	// Step 2/3: the 401 challenge names the protected-resource metadata URL;
	// fall back to discovering it from the server URL.
	challengeURL, challengeScopes, challengeErr := protectedResourceChallenge(ctx, client, serverURL)
	resource, authorizationServer, scopes, err := resolveProtectedResource(ctx, client, serverURL, challengeURL)
	if err != nil {
		// Step 4 fallback: treat the server URL itself as the issuer, which is the
		// legacy behavior and still correct for servers that publish RFC 8414
		// metadata at their own origin.
		if metadata, metaErr := discoverIssuerServer(ctx, client, serverURL); metaErr == nil {
			return metadata
		}
		return authServerMetadata{Resource: strings.TrimSpace(cfg.Resource)}
	}
	if len(scopes) == 0 {
		scopes = challengeScopes
	}
	if challengeErr != nil && len(scopes) == 0 {
		scopes = nil
	}

	metadata := authServerMetadata{Resource: resource, ScopesSupported: scopes}
	if discovered, err := discoverIssuerServer(ctx, client, authorizationServer); err == nil {
		metadata.Issuer = discovered.Issuer
		metadata.AuthorizationEndpoint = discovered.AuthorizationEndpoint
		metadata.TokenEndpoint = discovered.TokenEndpoint
		metadata.RegistrationEndpoint = discovered.RegistrationEndpoint
		if len(metadata.ScopesSupported) == 0 {
			metadata.ScopesSupported = discovered.ScopesSupported
		}
	}
	if resource := strings.TrimSpace(cfg.Resource); resource != "" {
		metadata.Resource = resource
	}
	return metadata
}

// discoveryDescription names the URLs the chain probed, so a discovery failure
// tells the user what to set explicitly.
func discoveryDescription(serverURL string, cfg OAuthConfig) string {
	if issuer := strings.TrimSpace(cfg.IssuerURL); issuer != "" {
		return fmt.Sprintf("issuer %s", issuer)
	}
	return fmt.Sprintf("server %s", serverURL)
}

// newPKCE generates a high-entropy code verifier and its S256 challenge via the
// shared engine.
func newPKCE() (pkceParams, error) {
	p, err := oauth.NewPKCE()
	if err != nil {
		return pkceParams{}, err
	}
	return pkceToParams(p), nil
}

func newState() (string, error) {
	return oauth.NewState()
}

// authorizationURL builds the authorization request URL via the shared engine.
func (flow *authorizationFlow) authorizationURL(redirectURI string) (string, error) {
	return oauth.BuildAuthorizationURL(configFor(flow.config, flow.metadata), pkceToOAuth(flow.pkce), flow.state, redirectURI, nil)
}

// parseCallback validates the redirect query and returns the authorization
// code. It rejects a mismatched state (CSRF) and surfaces provider errors.
func (flow *authorizationFlow) parseCallback(values url.Values) (string, error) {
	if got := values.Get("state"); got != flow.state {
		return "", errors.New("OAuth callback state mismatch: possible CSRF, login aborted")
	}
	if providerErr := strings.TrimSpace(values.Get("error")); providerErr != "" {
		description := strings.TrimSpace(values.Get("error_description"))
		if description != "" {
			return "", fmt.Errorf("authorization server returned error %q: %s", providerErr, description)
		}
		return "", fmt.Errorf("authorization server returned error %q", providerErr)
	}
	code := strings.TrimSpace(values.Get("code"))
	if code == "" {
		return "", errors.New("OAuth callback missing authorization code")
	}
	return code, nil
}

// exchangeCode swaps an authorization code + PKCE verifier for tokens via the
// shared engine.
func (flow *authorizationFlow) exchangeCode(ctx context.Context, code string, redirectURI string) (StoredToken, error) {
	token, err := oauth.ExchangeCode(ctx, flow.httpClient, configFor(flow.config, flow.metadata), code, flow.pkce.Verifier, redirectURI, flow.now)
	if err != nil {
		return StoredToken{}, err
	}
	return tokenToStored(token), nil
}

// refreshAccessToken exchanges a refresh token for a fresh access token via the
// shared engine. A response that omits a new refresh token preserves the
// previous one.
func refreshAccessToken(ctx context.Context, client *http.Client, cfg OAuthConfig, current StoredToken, now func() time.Time) (StoredToken, error) {
	token, err := oauth.Refresh(ctx, client, oauth.Config{
		ClientID:      cfg.ClientID,
		ClientSecret:  cfg.ClientSecret,
		Scopes:        cfg.Scopes,
		TokenEndpoint: cfg.TokenEndpoint,
		Resource:      cfg.Resource,
	}, oauth.Token{AccessToken: current.AccessToken, RefreshToken: current.RefreshToken, TokenType: current.TokenType, Scopes: current.Scopes, ExpiresAt: current.ExpiresAt}, now)
	if err != nil {
		return StoredToken{}, err
	}
	return tokenToStored(token), nil
}

// registerClient performs dynamic client registration against the registration
// endpoint and returns the issued client_id and optional client_secret.
func registerClient(ctx context.Context, client *http.Client, registrationEndpoint string, redirectURI string, scopes []string) (string, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	payload := map[string]any{
		"client_name":                "kajicode",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if len(scopes) > 0 {
		payload["scope"] = strings.Join(scopes, " ")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("client registration failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("client registration returned HTTP %d", response.StatusCode)
	}
	var registered struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&registered); err != nil {
		return "", "", fmt.Errorf("decode client registration response: %w", err)
	}
	if strings.TrimSpace(registered.ClientID) == "" {
		return "", "", errors.New("client registration returned no client_id")
	}
	return registered.ClientID, registered.ClientSecret, nil
}

// listenLoopback binds the OAuth redirect listener and returns the redirect URI
// to register with the authorization server. A configured RedirectURI pins the
// exact URI (some servers require it pre-registered); CallbackPort pins just the
// port; otherwise the OS assigns an ephemeral port. It always binds 127.0.0.1.
func listenLoopback(cfg OAuthConfig) (net.Listener, string, error) {
	address := "127.0.0.1:0"
	uriPath := "/callback"
	if explicit := strings.TrimSpace(cfg.RedirectURI); explicit != "" {
		parsed, err := url.Parse(explicit)
		if err != nil || parsed.Host == "" {
			return nil, "", fmt.Errorf("invalid oauth.redirectURI %q", explicit)
		}
		if parsed.Scheme != "http" || !isLoopbackRedirectHost(parsed.Hostname()) {
			return nil, "", fmt.Errorf("oauth.redirectURI %q must be a loopback http URL", explicit)
		}
		address = parsed.Host
		if parsed.Path != "" {
			uriPath = parsed.Path
		}
	} else if cfg.CallbackPort > 0 {
		address = fmt.Sprintf("127.0.0.1:%d", cfg.CallbackPort)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("start loopback redirect listener on %s: %w", address, err)
	}
	redirectURI := "http://" + listener.Addr().String() + uriPath
	return listener, redirectURI, nil
}

// isLoopbackRedirectHost reports whether a redirect host is a loopback name,
// mirroring the shared engine's https/loopback rule.
func isLoopbackRedirectHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// callbackPathFor extracts the URL path the loopback server should answer on
// from the redirect URI, defaulting to /callback.
func callbackPathFor(redirectURI string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Path == "" {
		return "/callback"
	}
	return parsed.Path
}

// Login runs the full OAuth 2.0 + PKCE authorization-code flow: it discovers (or
// falls back to configured) endpoints, optionally registers a client, starts a
// loopback redirect listener, opens the authorization URL, validates the
// callback state, and exchanges the code for tokens. Tokens are returned and are
// never logged.
func Login(ctx context.Context, options LoginOptions) (StoredToken, error) {
	if err := ValidateServerName(options.ServerName); err != nil {
		return StoredToken{}, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultLoginTimeout
	}
	// One deadline bounds the WHOLE interactive login — discovery, optional client
	// registration, the callback wait, and the code exchange — so a hung
	// metadata/registration/token endpoint can't block the command forever (the
	// CLI passes a non-cancelable context).
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := options.Config
	metadata, err := resolveAuthorizationServer(loginCtx, httpClient, options.ServerURL, cfg)
	if err != nil {
		return StoredToken{}, err
	}
	// Fall back to the scopes the protected resource advertised when the user did
	// not configure any, so Slack-style servers work with no scope config.
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = metadata.ScopesSupported
	}

	// Bind the loopback redirect listener first so the redirect URI is known
	// before client registration and authorization URL construction. A configured
	// RedirectURI/CallbackPort is honored because some servers (Slack) require the
	// exact redirect URI to be pre-registered and reject an ephemeral port.
	listener, redirectURI, err := listenLoopback(cfg)
	if err != nil {
		return StoredToken{}, err
	}
	defer listener.Close()

	if strings.TrimSpace(cfg.ClientID) == "" {
		if registration := strings.TrimSpace(metadata.RegistrationEndpoint); registration != "" {
			clientID, clientSecret, regErr := registerClient(loginCtx, httpClient, registration, redirectURI, cfg.Scopes)
			if regErr != nil {
				return StoredToken{}, regErr
			}
			cfg.ClientID = clientID
			if clientSecret != "" {
				cfg.ClientSecret = clientSecret
			}
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return StoredToken{}, fmt.Errorf("%w: set oauth.clientID (and oauth.clientSecret) in the MCP server config", ErrNeedsClientRegistration)
	}

	pkce, err := newPKCE()
	if err != nil {
		return StoredToken{}, err
	}
	state, err := newState()
	if err != nil {
		return StoredToken{}, err
	}

	flow := &authorizationFlow{
		httpClient: httpClient,
		metadata:   metadata,
		config:     cfg,
		pkce:       pkce,
		state:      state,
		now:        now,
	}

	authURL, err := flow.authorizationURL(redirectURI)
	if err != nil {
		return StoredToken{}, err
	}

	type callbackResult struct {
		code string
		err  error
	}
	resultChan := make(chan callbackResult, 1)
	callbackPath := callbackPathFor(redirectURI)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != callbackPath {
				http.NotFound(w, r)
				return
			}
			code, parseErr := flow.parseCallback(r.URL.Query())
			if parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "Authorization failed. You may close this window.")
			} else {
				_, _ = io.WriteString(w, "Authorization complete. You may close this window.")
			}
			select {
			case resultChan <- callbackResult{code: code, err: parseErr}:
			default:
			}
		}),
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	open := options.OpenBrowser
	if open == nil {
		open = func(string) error { return nil }
	}
	if err := open(authURL); err != nil {
		return StoredToken{}, fmt.Errorf("open authorization URL: %w", err)
	}

	select {
	case result := <-resultChan:
		if result.err != nil {
			return StoredToken{}, result.err
		}
		token, err := flow.exchangeCode(loginCtx, result.code, redirectURI)
		if err != nil {
			return StoredToken{}, err
		}
		return token, nil
	case <-loginCtx.Done():
		return StoredToken{}, fmt.Errorf("timed out waiting for OAuth authorization callback: %w", loginCtx.Err())
	}
}
