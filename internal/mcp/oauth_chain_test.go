package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestResolveViaChallengeChain reproduces the Slack topology: the MCP endpoint
// answers 401 with a WWW-Authenticate resource_metadata URL, that document
// names an authorization server, and the authorization server publishes RFC 8414
// metadata. Before this change KajiCode probed only the server's own
// /.well-known/oauth-authorization-server, found nothing, and failed with
// "no authorization endpoint discovered or configured".
func TestResolveViaChallengeChain(t *testing.T) {
	mux := http.NewServeMux()

	// MCP endpoint: unauthenticated initialize returns the challenge.
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+absoluteURL(r)+`/.well-known/oauth-protected-resource", scope="channels:read chat:write"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	// Protected-resource metadata (RFC 9728).
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              absoluteURL(r),
			"authorization_servers": []string{absoluteURL(r)},
			"scopes_supported":      []string{"channels:read", "chat:write", "users:read"},
		})
	})
	// Authorization-server metadata (RFC 8414) at the origin root.
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                absoluteURL(r),
			"authorization_endpoint":                "https://slack.example/oauth/authorize",
			"token_endpoint":                        "https://slack.example/api/oauth.access",
			"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
			"scopes_supported":                      []string{"channels:read", "chat:write"},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	metadata, err := resolveAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp", OAuthConfig{})
	if err != nil {
		t.Fatalf("resolveAuthorizationServer() error = %v", err)
	}
	if metadata.AuthorizationEndpoint != "https://slack.example/oauth/authorize" {
		t.Fatalf("authorization endpoint = %q", metadata.AuthorizationEndpoint)
	}
	if metadata.TokenEndpoint != "https://slack.example/api/oauth.access" {
		t.Fatalf("token endpoint = %q", metadata.TokenEndpoint)
	}
	// Scopes fall back to the protected resource's advertised list.
	if strings.Join(metadata.ScopesSupported, ",") != "channels:read,chat:write,users:read" {
		t.Fatalf("scopes = %v", metadata.ScopesSupported)
	}
	// The protected-resource document advertises the root as the resource, so the
	// resolver must honor that over the MCP endpoint path.
	if metadata.Resource != server.URL {
		t.Fatalf("resource = %q, want %q", metadata.Resource, server.URL)
	}
}

// TestResolveChainConfiguredScopesWin: an explicit scope list must not be
// replaced by discovered scopes.
func TestResolveChainConfiguredScopesWin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+absoluteURL(r)+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_servers": []string{absoluteURL(r)},
			"scopes_supported":      []string{"discovered:a", "discovered:b"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://auth.example/authorize",
			"token_endpoint":         "https://auth.example/token",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	metadata, err := resolveAuthorizationServer(context.Background(), server.Client(), server.URL+"/mcp", OAuthConfig{Scopes: []string{"only:this"}})
	if err != nil {
		t.Fatalf("resolveAuthorizationServer() error = %v", err)
	}
	if strings.Join(metadata.ScopesSupported, ",") != "discovered:a,discovered:b" {
		t.Fatalf("metadata scopes = %v", metadata.ScopesSupported)
	}
}

// TestProtectedResourceChallengeError ensures a non-401 server yields a clear
// discovery-probe error rather than a panic or empty metadata.
func TestProtectedResourceChallengeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if _, _, err := protectedResourceChallenge(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("expected an error for a server that does not return 401")
	}
}

// TestProtectedResourceChallengeMissingMetadataURL rejects a 401 that names no
// resource_metadata URL.
func TestProtectedResourceChallengeMissingMetadataURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, _, err := protectedResourceChallenge(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("expected an error when the challenge has no resource_metadata URL")
	}
}

func absoluteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
