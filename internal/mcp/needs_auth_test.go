package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
)

// TestStoreTokenSourceNeedsAuthWithoutToken: a server with auth:oauth and no
// stored token must report needs-auth, not a generic failure, so the CLI can
// point at `kajicode mcp oauth login <name>`.
func TestStoreTokenSourceNeedsAuthWithoutToken(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{})
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	server := Server{Name: "slack", Type: ServerTypeHTTP, URL: "https://mcp.slack.com/mcp", Auth: ServerAuthOAuth}
	server.Identity = computeServerIdentity(server)
	source := &storeTokenSource{
		server: server,
		store:  store,
	}
	_, err = source.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected an error when no token is stored")
	}
	if !needsAuth(err) {
		t.Fatalf("expected needs-auth error, got %v", err)
	}
	if !strings.Contains(err.Error(), "kajicode mcp oauth login slack") {
		t.Fatalf("error %q should name the login command", err)
	}
}

// TestRoundTripperClassifiesRejectedToken: a 401 whose token cannot be refreshed
// must surface as needs-auth carrying the challenge's resource_metadata URL.
func TestRoundTripperClassifiesRejectedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	transport := newOAuthRoundTripper(server.Client().Transport, rejectingTokenSource{}, "slack")
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = transport.RoundTrip(request)
	if err == nil {
		t.Fatal("expected an error for a rejected token")
	}
	if !needsAuth(err) {
		t.Fatalf("expected needs-auth error, got %v", err)
	}
	var target *needsAuthError
	if !errors.As(err, &target) {
		t.Fatalf("expected *needsAuthError, got %T", err)
	}
	if target.challenge.ResourceMetadataURL != "https://mcp.example.com/.well-known/oauth-protected-resource" {
		t.Fatalf("challenge = %+v", target.challenge)
	}
}

func TestNeedsClientRegistrationClassifier(t *testing.T) {
	if needsClientRegistration(nil) {
		t.Fatal("nil must not classify as needs-client-registration")
	}
	if needsClientRegistration(ErrNeedsAuth) {
		t.Fatal("needs-auth must not classify as needs-client-registration")
	}
	wrapped := fmt.Errorf("login failed: %w", ErrNeedsClientRegistration)
	if !needsClientRegistration(wrapped) {
		t.Fatalf("wrapped ErrNeedsClientRegistration must classify, got %v", wrapped)
	}
	if needsAuth(wrapped) {
		t.Fatal("needs-client-registration must not classify as needs-auth")
	}
}

type rejectingTokenSource struct{}

func (rejectingTokenSource) AccessToken(context.Context) (string, error) { return "stale-token", nil }
func (rejectingTokenSource) Refresh(context.Context) (string, error) {
	return "", errors.New("refresh token expired")
}

func TestParseAuthChallengeFromResponse(t *testing.T) {
	response := &http.Response{Header: http.Header{}}
	response.Header.Set("WWW-Authenticate", `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource", scope="a b"`)
	challenge := parseAuthChallenge(response)
	if challenge.ResourceMetadataURL != "https://x/.well-known/oauth-protected-resource" {
		t.Fatalf("resource_metadata = %q", challenge.ResourceMetadataURL)
	}
	if strings.Join(challenge.Scopes, ",") != "a,b" {
		t.Fatalf("scopes = %v", challenge.Scopes)
	}
	if got := parseAuthChallenge(nil); got.ResourceMetadataURL != "" {
		t.Fatalf("nil response should yield empty challenge, got %+v", got)
	}
}

// TestListenLoopbackRedirectURI pins the configured redirect URI and callback
// path so a server that requires a pre-registered redirect works.
func TestListenLoopbackRedirectURI(t *testing.T) {
	listener, redirectURI, err := listenLoopback(OAuthConfig{RedirectURI: "http://127.0.0.1:45999/oauth/callback"})
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer listener.Close()
	if redirectURI != "http://127.0.0.1:45999/oauth/callback" {
		t.Fatalf("redirectURI = %q", redirectURI)
	}
	if got := callbackPathFor(redirectURI); got != "/oauth/callback" {
		t.Fatalf("callbackPathFor = %q", got)
	}
}

func TestListenLoopbackRejectsNonLoopbackRedirect(t *testing.T) {
	if _, _, err := listenLoopback(OAuthConfig{RedirectURI: "https://evil.example/callback"}); err == nil {
		t.Fatal("expected a non-loopback redirect URI to be rejected")
	}
}

func TestCallbackPathDefault(t *testing.T) {
	if got := callbackPathFor("http://127.0.0.1:1234"); got != "/callback" {
		t.Fatalf("default callback path = %q", got)
	}
	if got := callbackPathFor(""); got != "/callback" {
		t.Fatalf("empty callback path = %q", got)
	}
}

// TestNormalizeOAuthConfigCarriesNewFields: redirectURI/callbackPort/resource
// must survive config normalization.
func TestNormalizeOAuthConfigCarriesNewFields(t *testing.T) {
	raw := &config.MCPOAuthConfig{
		ClientID:     "  client  ",
		RedirectURI:  " http://127.0.0.1:45999/callback ",
		CallbackPort: 45999,
		Resource:     " https://mcp.slack.com ",
	}
	cfg := normalizeOAuthConfig(raw)
	if cfg == nil {
		t.Fatal("normalizeOAuthConfig returned nil")
	}
	if cfg.ClientID != "client" {
		t.Fatalf("clientID = %q", cfg.ClientID)
	}
	if cfg.RedirectURI != "http://127.0.0.1:45999/callback" {
		t.Fatalf("redirectURI = %q", cfg.RedirectURI)
	}
	if cfg.CallbackPort != 45999 {
		t.Fatalf("callbackPort = %d", cfg.CallbackPort)
	}
	if cfg.Resource != "https://mcp.slack.com" {
		t.Fatalf("resource = %q", cfg.Resource)
	}
	if normalizeOAuthConfig(nil) != nil {
		t.Fatal("nil raw config should normalize to nil")
	}
}
