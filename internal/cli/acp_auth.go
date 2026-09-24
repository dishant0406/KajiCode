package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/acp"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/providercatalog"
)

// ACP auth wiring. KajiCode owns its providers (BYOK): the editor only triggers
// login/logout and provider creation, and every credential ends up in KajiCode's
// own store. Browser OAuth logins are offered as terminal auth methods, so the
// editor launches `kajicode auth login <provider>` in a real terminal — the
// token never crosses the ACP wire.

// acpAuthMethods advertises terminal login methods for every OAuth-capable
// catalog provider, but only when the client can actually launch a terminal.
func acpAuthMethods(terminalAuth bool) []acp.AuthMethod {
	if !terminalAuth {
		return nil
	}
	var methods []acp.AuthMethod
	for _, descriptor := range providercatalog.All() {
		if !descriptor.OAuth {
			continue
		}
		methods = append(methods, acp.AuthMethod{
			ID:          "login-" + descriptor.ID,
			Name:        "Sign in with " + descriptor.Name,
			Description: "Runs `kajicode auth login " + descriptor.ID + "` in a terminal.",
			Type:        "terminal",
			Args:        []string{"auth", "login", descriptor.ID},
		})
	}
	return methods
}

// acpAuthenticate rejects agent-type logins. KajiCode's logins are browser-based
// and are advertised as terminal methods, so calling `authenticate` instead of
// launching the terminal flow is unsupported.
func acpAuthenticate() func(ctx context.Context, methodID string) error {
	return func(_ context.Context, methodID string) error {
		return fmt.Errorf("unsupported auth method %q; use the terminal sign-in method", methodID)
	}
}

// acpLogout clears the active provider's credential (OAuth token and stored API
// key), reusing the same `auth logout` path the CLI runs.
func acpLogout(deps appDeps) func(ctx context.Context) error {
	return func(_ context.Context) error {
		workspaceRoot, err := resolveWorkspaceRoot("", deps)
		if err != nil {
			return err
		}
		resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
		if err != nil {
			return err
		}
		provider := strings.TrimSpace(resolved.ActiveProvider)
		if provider == "" {
			provider = strings.TrimSpace(resolved.Provider.Name)
		}
		if provider == "" {
			return nil
		}
		var out strings.Builder
		if code := runAuthLogout([]string{provider}, &out, &out, deps); code != exitSuccess {
			return errFromWriter(out.String())
		}
		return nil
	}
}

// acpProviderAdd creates/updates a provider profile from the fields the editor
// collected, reusing `providers add` and the shared credential store.
func acpProviderAdd(deps appDeps) func(ctx context.Context, fields map[string]string) (string, error) {
	return func(_ context.Context, fields map[string]string) (string, error) {
		name := strings.TrimSpace(fields["name"])
		catalogID, err := resolveProviderCatalogID(name, fields["customKind"])
		if err != nil {
			return "", err
		}
		args := []string{catalogID, "--name", name}
		appendFlag := func(flag, value string) {
			if v := strings.TrimSpace(value); v != "" {
				args = append(args, flag, v)
			}
		}
		appendFlag("--base-url", fields["baseUrl"])
		appendFlag("--model", fields["model"])
		appendFlag("--auth-header", fields["authHeader"])
		appendFlag("--auth-scheme", fields["authScheme"])

		var out strings.Builder
		if code := runProvidersAdd(args, &out, &out, deps); code != exitSuccess {
			return "", errFromWriter(out.String())
		}
		summary := strings.TrimSpace(out.String())
		if summary == "" {
			summary = "Added provider " + name
		}
		return summary, nil
	}
}

// resolveProviderCatalogID maps a user-supplied provider name to the catalog id
// `providers add` expects. It uses an exact catalog match when one exists,
// otherwise the explicit custom-compatible entry named by customKind (chosen by
// the caller from the custom transport), so an unknown name never silently
// becomes an OpenAI-compatible profile.
func resolveProviderCatalogID(name, customKind string) (string, error) {
	if descriptor, ok := providercatalog.Get(name); ok {
		return descriptor.ID, nil
	}
	id := strings.TrimSpace(customKind)
	if id == "" {
		id = "custom-openai-compatible"
	}
	if _, ok := providercatalog.Get(id); !ok {
		return "", fmt.Errorf("unknown provider %q and unknown custom entry %q", name, id)
	}
	return id, nil
}

// errFromWriter turns captured command output into an error, trimming the
// "[kajicode] " prefix writeExecUsageError adds.
func errFromWriter(output string) error {
	message := strings.TrimPrefix(strings.TrimSpace(output), "[kajicode] ")
	if message == "" {
		message = "command failed"
	}
	return fmt.Errorf("%s", message)
}
