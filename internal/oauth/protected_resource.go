package oauth

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
)

const wellKnownProtectedResourcePath = "/.well-known/oauth-protected-resource"

// ProtectedResourceMetadata is the RFC 9728 OAuth 2.0 protected-resource
// metadata document. A remote MCP server advertises it (usually at
// /.well-known/oauth-protected-resource) so a client can learn which
// authorization server issues tokens for it and which scopes it accepts.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// DiscoverProtectedResource fetches RFC 9728 metadata for resourceURL. It tries
// the path-inserted well-known URL first (…/.well-known/oauth-protected-resource
// plus the resource path) and then the bare host form, which is what Slack and
// most MCP servers advertise in their 401 challenge.
func DiscoverProtectedResource(ctx context.Context, client *http.Client, resourceURL string) (ProtectedResourceMetadata, error) {
	candidates, err := protectedResourceCandidates(resourceURL)
	if err != nil {
		return ProtectedResourceMetadata{}, err
	}
	var lastErr error
	for _, candidate := range candidates {
		metadata, err := fetchProtectedResource(ctx, client, candidate)
		if err == nil {
			return metadata, nil
		}
		lastErr = err
	}
	return ProtectedResourceMetadata{}, lastErr
}

// FetchProtectedResourceMetadata fetches one protected-resource metadata
// document from an exact URL (as advertised in a WWW-Authenticate challenge).
// The URL is validated with the SSRF rule before the request is made.
func FetchProtectedResourceMetadata(ctx context.Context, client *http.Client, metadataURL string) (ProtectedResourceMetadata, error) {
	if err := ValidateDiscoveryURL(metadataURL); err != nil {
		return ProtectedResourceMetadata{}, err
	}
	return fetchProtectedResource(ctx, client, metadataURL)
}

// protectedResourceCandidates builds the RFC 9728 well-known URLs for a
// resource: the path-inserted form first, then the host-root form.
func protectedResourceCandidates(resourceURL string) ([]string, error) {
	parsed, err := url.Parse(trimmed(resourceURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("oauth: invalid resource URL for protected-resource discovery: %q", resourceURL)
	}
	if err := ValidateDiscoveryURL(resourceURL); err != nil {
		return nil, err
	}
	resourcePath := strings.Trim(parsed.Path, "/")

	pathScoped := *parsed
	pathScoped.Path = wellKnownProtectedResourcePath
	if resourcePath != "" {
		pathScoped.Path = wellKnownProtectedResourcePath + "/" + resourcePath
	}
	pathScoped.RawQuery = ""
	pathScoped.Fragment = ""

	hostRoot := *parsed
	hostRoot.Path = wellKnownProtectedResourcePath
	hostRoot.RawQuery = ""
	hostRoot.Fragment = ""

	if pathScoped.String() == hostRoot.String() {
		return []string{hostRoot.String()}, nil
	}
	return []string{pathScoped.String(), hostRoot.String()}, nil
}

// fetchProtectedResource GETs and decodes one protected-resource metadata
// document. A non-2xx response is an error so the caller can try the next
// candidate.
func fetchProtectedResource(ctx context.Context, client *http.Client, metadataURL string) (ProtectedResourceMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return ProtectedResourceMetadata{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := discoveryClient(client).Do(request)
	if err != nil {
		return ProtectedResourceMetadata{}, fmt.Errorf("oauth: fetch protected-resource metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ProtectedResourceMetadata{}, fmt.Errorf("oauth: protected-resource metadata returned HTTP %d", response.StatusCode)
	}
	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, tokenResponseLimit)).Decode(&metadata); err != nil {
		return ProtectedResourceMetadata{}, fmt.Errorf("oauth: decode protected-resource metadata: %w", err)
	}
	return metadata, nil
}

// ParseResourceMetadataChallenge extracts the resource_metadata URL and scope
// from a Bearer WWW-Authenticate challenge (RFC 6750 / MCP authorization spec).
// It is tolerant of extra parameters and returns empty values when absent.
func ParseResourceMetadataChallenge(header string) (resourceMetadataURL string, scopes []string) {
	for _, token := range splitAuthParams(header) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "resource_metadata":
			resourceMetadataURL = value
		case "scope":
			scopes = strings.Fields(value)
		}
	}
	return resourceMetadataURL, scopes
}

// splitAuthParams splits an auth-param list on commas and whitespace while
// respecting double-quoted values (a scope list may itself contain spaces).
func splitAuthParams(header string) []string {
	var tokens []string
	var current strings.Builder
	quoted := false
	for _, r := range strings.TrimPrefix(strings.TrimSpace(header), "Bearer") {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case (r == ',' || r == ' ') && !quoted:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// DiscoverIssuerMetadata resolves authorization-server metadata for an issuer,
// trying the RFC 8414 OAuth path first and the OIDC openid-configuration path
// second (some issuers publish only the latter).
func DiscoverIssuerMetadata(ctx context.Context, client *http.Client, issuer string) (ServerMetadata, error) {
	metadata, err := DiscoverAuthorizationServer(ctx, client, issuer)
	if err == nil && (metadata.AuthorizationEndpoint != "" || metadata.TokenEndpoint != "") {
		return metadata, nil
	}
	oidcURL := strings.TrimRight(trimmed(issuer), "/") + oidcWellKnownPath
	oidc, oidcErr := fetchMetadata(ctx, discoveryClient(client), oidcURL)
	if oidcErr != nil {
		if err != nil {
			return ServerMetadata{}, err
		}
		return ServerMetadata{}, oidcErr
	}
	return oidc, nil
}

// ValidateDiscoveryURL applies the SSRF rule to a discovery/metadata URL: https
// required (loopback exempt), and https literals may not point at a private,
// link-local, or unspecified address. This keeps an attacker-influenced
// resource_metadata URL from reaching internal infrastructure.
func ValidateDiscoveryURL(rawURL string) error {
	parsed, err := url.Parse(trimmed(rawURL))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("oauth: invalid discovery URL %q", rawURL)
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: %s", ErrInsecureTokenEndpoint, parsed.Scheme+"://"+parsed.Host)
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !ip.IsLoopback() {
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("oauth: refusing to discover from a non-public address: %s", parsed.Host)
		}
	}
	return nil
}

// discoveryClient returns a client that re-validates every redirect target with
// ValidateDiscoveryURL. It reuses the supplied client's transport but overrides
// CheckRedirect, so a discovery GET cannot be bounced to an internal address.
func discoveryClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("oauth: stopped after 10 redirects")
		}
		return ValidateDiscoveryURL(request.URL.String())
	}
	return &clone
}
