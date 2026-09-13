package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dishant0406/KajiCode/internal/oauth"
)

// ErrNeedsClientRegistration marks a connection/login failure that means "this
// OAuth server does not support dynamic client registration, so the user must
// supply a pre-registered client ID". Callers match it with errors.Is to report
// a needs_client_registration status (distinct from needs-auth) with an
// actionable message, mirroring opencode.
var ErrNeedsClientRegistration = errors.New("mcp: server requires a pre-registered OAuth client ID")

// ErrNeedsAuth marks a connection failure that means "this OAuth server needs
// the user to run `kajicode mcp oauth login <server>`". Callers match it with
// errors.Is so an unauthenticated server is reported as needs-auth rather than a
// generic failure.
var ErrNeedsAuth = errors.New("mcp: server requires OAuth login")

// AuthChallenge describes what a server's 401 told us: where its
// protected-resource metadata lives and which scopes it asked for.
type AuthChallenge struct {
	ResourceMetadataURL string
	Scopes              []string
}

// needsAuthError wraps ErrNeedsAuth with an actionable message and, when the
// server advertised one, the challenge details.
type needsAuthError struct {
	serverName string
	challenge  AuthChallenge
}

func (e *needsAuthError) Error() string {
	return fmt.Sprintf("MCP server %s requires OAuth authentication: run `kajicode mcp oauth login %s`", e.serverName, e.serverName)
}

func (e *needsAuthError) Unwrap() error { return ErrNeedsAuth }

// newNeedsAuthError builds a needs-auth error for a server.
func newNeedsAuthError(serverName string, challenge AuthChallenge) error {
	return &needsAuthError{serverName: serverName, challenge: challenge}
}

// needsAuth reports whether an error chain means the server needs OAuth login.
func needsAuth(err error) bool {
	return errors.Is(err, ErrNeedsAuth)
}

// needsClientRegistration reports whether an error chain means the server needs a
// pre-registered OAuth client ID (no dynamic registration available).
func needsClientRegistration(err error) bool {
	return errors.Is(err, ErrNeedsClientRegistration)
}

// parseAuthChallenge extracts the resource_metadata URL and scopes from a 401
// response's WWW-Authenticate header.
func parseAuthChallenge(response *http.Response) AuthChallenge {
	if response == nil {
		return AuthChallenge{}
	}
	metadataURL, scopes := oauth.ParseResourceMetadataChallenge(response.Header.Get("WWW-Authenticate"))
	return AuthChallenge{ResourceMetadataURL: strings.TrimSpace(metadataURL), Scopes: scopes}
}
