package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseResourceMetadataChallenge(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantURL    string
		wantScopes []string
	}{
		{
			name:       "resource_metadata only",
			header:     `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`,
			wantURL:    "https://mcp.example.com/.well-known/oauth-protected-resource",
			wantScopes: nil,
		},
		{
			name:       "resource_metadata and scope",
			header:     `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource", scope="channels:read chat:write"`,
			wantURL:    "https://mcp.example.com/.well-known/oauth-protected-resource",
			wantScopes: []string{"channels:read", "chat:write"},
		},
		{
			name:       "scope list with spaces and no resource_metadata",
			header:     `Bearer error="insufficient_scope", scope="a b c"`,
			wantURL:    "",
			wantScopes: []string{"a", "b", "c"},
		},
		{
			name:       "malformed header",
			header:     "Bearer",
			wantURL:    "",
			wantScopes: nil,
		},
		{
			name:       "empty header",
			header:     "",
			wantURL:    "",
			wantScopes: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, scopes := ParseResourceMetadataChallenge(tc.header)
			if url != tc.wantURL {
				t.Errorf("url = %q, want %q", url, tc.wantURL)
			}
			if strings.Join(scopes, ",") != strings.Join(tc.wantScopes, ",") {
				t.Errorf("scopes = %v, want %v", scopes, tc.wantScopes)
			}
		})
	}
}

func TestValidateDiscoveryURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https public", "https://mcp.slack.com/.well-known/oauth-protected-resource", false},
		{"http loopback", "http://127.0.0.1:8080/.well-known/oauth-protected-resource", false},
		{"http public is refused", "http://mcp.example.com/.well-known/oauth-protected-resource", true},
		{"https private ip refused", "https://10.0.0.5/.well-known/oauth-protected-resource", true},
		{"https link-local refused", "https://169.254.169.254/latest/meta-data", true},
		{"empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDiscoveryURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateDiscoveryURL(%q) err = %v, wantErr %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestDiscoverProtectedResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-protected-resource/mcp" &&
			r.URL.Path != "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + serverURL(r) + `","authorization_servers":["https://auth.example.com"],"scopes_supported":["a","b"]}`))
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResource(context.Background(), server.Client(), server.URL+"/mcp")
	if err != nil {
		t.Fatalf("DiscoverProtectedResource: %v", err)
	}
	if len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != "https://auth.example.com" {
		t.Errorf("authorization_servers = %v", metadata.AuthorizationServers)
	}
	if strings.Join(metadata.ScopesSupported, ",") != "a,b" {
		t.Errorf("scopes = %v", metadata.ScopesSupported)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestFetchProtectedResourceRejectsInsecureURL(t *testing.T) {
	_, err := FetchProtectedResourceMetadata(context.Background(), http.DefaultClient, "http://public.example.com/.well-known/oauth-protected-resource")
	if err == nil {
		t.Fatal("expected insecure URL to be rejected")
	}
}
