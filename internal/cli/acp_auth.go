package cli

import (
	"context"
	"fmt"
	"strconv"
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

// acpClassifierConfigure registers/updates a classifier profile and its feature
// switches from the fields the editor collected, reusing `classifier configure`
// and the shared credential store. Secrets are never among the fields.
func acpClassifierConfigure(deps appDeps) func(ctx context.Context, fields map[string]string) (string, error) {
	return func(_ context.Context, fields map[string]string) (string, error) {
		name := strings.TrimSpace(fields["name"])
		if name == "" {
			return "", fmt.Errorf("a classifier name is required")
		}
		args := []string{name}
		appendFlag := func(flag, value string) {
			if v := strings.TrimSpace(value); v != "" {
				args = append(args, flag, v)
			}
		}
		appendFlag("--endpoint", fields["endpoint"])
		appendFlag("--model", fields["model"])
		appendFlag("--auth-header", fields["authHeader"])
		appendFlag("--auth-scheme", fields["authScheme"])
		appendFlag("--timeout", fields["timeoutMs"])
		for _, header := range strings.Split(fields["headers"], "\n") {
			if trimmed := strings.TrimSpace(header); trimmed != "" {
				args = append(args, "--header", trimmed)
			}
		}
		appendBoolFlag(&args, "--enable", fields["enable"])
		appendBoolFlag(&args, "--compaction", fields["compaction"])
		appendFlag("--compaction-keep", fields["compactionKeep"])
		appendBoolFlag(&args, "--tool-result", fields["toolResult"])
		appendFlag("--tool-drop", fields["toolDrop"])
		appendFlag("--tool-keep", fields["toolKeep"])
		appendBoolFlag(&args, "--tool-shadow", fields["toolShadow"])

		var out strings.Builder
		if code := runClassifierConfigure(args, &out, &out, deps); code != exitSuccess {
			return "", errFromWriter(out.String())
		}
		summary := strings.TrimSpace(out.String())
		if summary == "" {
			summary = "Configured classifier " + name
		}
		return summary, nil
	}
}

// appendBoolFlag appends `--flag=<bool>` only when the field is a parseable bool,
// so a blank field leaves the stored value unchanged rather than erroring.
func appendBoolFlag(args *[]string, flag, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		*args = append(*args, flag+"="+strconv.FormatBool(parsed))
	}
}

// resolveProviderCatalogID maps a user-supplied provider name to the catalog id
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
