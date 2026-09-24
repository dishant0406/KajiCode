package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/imageinput"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// Deps are the KAJICODE capabilities the ACP Agent drives. The CLI fills these with
// real implementations; tests inject fakes (e.g. a canned provider) to drive the
// full ACP flow without a live model. Keeping auth/model/keys behind these deps
// means the editor only hosts the thread — KAJICODE owns BYOK and telemetry-free
// operation.
type Deps struct {
	ResolveConfig func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error)
	NewProvider   func(profile config.ProviderProfile) (kajicoderuntime.Provider, error)
	RunAgent      func(ctx context.Context, prompt string, provider kajicoderuntime.Provider, opts agent.Options) (agent.Result, error)
	// BuildWorkspace builds the SCOPED tool registry, the sandbox engine, and the
	// skill catalog for a validated workspace root. The registry confines ACP
	// shell tools (bash/exec_command) exactly like the exec surface — never run
	// unconfined on the host.
	BuildWorkspace func(workspaceRoot string, resolved config.ResolvedConfig) (Workspace, error)
	// ResolveWorkspaceRoot validates + normalizes a client-supplied cwd (must be an
	// existing directory; never the bare root). It is the file-tool confinement root.
	ResolveWorkspaceRoot func(cwd string) (string, error)
	Store                *sessions.Store
	AgentInfo            Implementation
	// Commands is the slash-command catalog advertised to the client via
	// available_commands_update. Empty advertises none.
	Commands []AvailableCommand
	// RunCommand executes a slash command (name without the leading "/") for a
	// session. ok=false means the command is unknown and the text should be
	// treated as an ordinary prompt.
	RunCommand func(ctx context.Context, command, args, cwd, sessionID string) (output string, ok bool, err error)
	// Retitle generates and persists a title for a session (resolved in cwd) and
	// returns it, so the agent can emit session_info_update. nil disables /retitle.
	Retitle func(ctx context.Context, sessionID, cwd string) (string, error)
	// AuthMethods returns the login methods to advertise in `initialize`. The
	// terminalAuth argument reports whether the client advertised
	// clientCapabilities.auth.terminal, so the value can include terminal methods
	// only when the client can actually launch them. nil advertises none.
	AuthMethods func(terminalAuth bool) []AuthMethod
	// Authenticate completes an agent-type login for methodID. nil means no
	// agent-type method is offered.
	Authenticate func(ctx context.Context, methodID string) error
	// Logout clears the active provider credential (OAuth token and stored key).
	// nil means `logout` is not advertised.
	Logout func(ctx context.Context) error
	// ProviderAdd creates/updates a provider from NON-SECRET fields (name,
	// baseUrl, model, authHeader, authScheme, customKind). Credentials are never
	// passed here (form elicitation must not carry secrets). It returns a
	// human-readable summary. nil disables the provider-add flow.
	ProviderAdd func(ctx context.Context, fields map[string]string) (string, error)
}

// Workspace is the per-session toolkit ACP runs a turn against: the scoped tool
// registry, the sandbox engine (nil for ACP, which runs no sandboxed backend of
// its own), the skill catalog advertised in the system prompt, and the effective
// working directory after additional directories are folded into the scope.
type Workspace struct {
	Registry *tools.Registry
	Sandbox  *sandbox.Engine
	Skills   []agent.SkillInfo
}

// Agent is the ACP agent server bound to one JSON-RPC connection (one editor).
type Agent struct {
	conn *Conn
	deps Deps

	mu         sync.Mutex
	clientCaps ClientCapabilities
	sessions   map[string]*acpSession
}

type turnRecord struct {
	user      string
	assistant string
}

type acpSession struct {
	id  string
	cwd string

	// resolved is the config snapshot resolved at session creation, reused for
	// config-option advertisement so handlers never re-resolve on every read.
	resolved config.ResolvedConfig
	extra    []string // validated additional directories (absolute)

	// turnMu serializes prompt turns for one session: concurrent session/prompt
	// calls run one at a time so they can't interleave history or clobber the
	// single cancel slot.
	turnMu sync.Mutex

	mu          sync.Mutex
	mode        agent.PermissionMode
	model       string // override; "" => config default
	effortLevel string // reasoning effort override; "" => model default
	turns       int    // turn budget override; 0 => resolved default
	style       string // response style override; "" => balanced
	cancel      context.CancelFunc
	history     []turnRecord
}

// NewAgent builds the ACP server and registers its method handlers on conn.
func NewAgent(conn *Conn, deps Deps) *Agent {
	a := &Agent{conn: conn, deps: deps, sessions: make(map[string]*acpSession)}
	conn.Handle(MethodInitialize, a.handleInitialize)
	conn.Handle(MethodAuthenticate, a.handleAuthenticate)
	conn.Handle(MethodLogout, a.handleLogout)
	conn.Handle(MethodSessionNew, a.handleSessionNew)
	conn.Handle(MethodSessionLoad, a.handleSessionLoad)
	conn.Handle(MethodSessionResume, a.handleSessionResume)
	conn.Handle(MethodSessionFork, a.handleSessionFork)
	conn.Handle(MethodSessionList, a.handleSessionList)
	conn.Handle(MethodSessionClose, a.handleSessionClose)
	conn.Handle(MethodSessionDelete, a.handleSessionDelete)
	conn.Handle(MethodSessionPrompt, a.handleSessionPrompt)
	conn.Handle(MethodSessionSetMode, a.handleSetMode)
	conn.Handle(MethodSessionSetConfigOption, a.handleSetConfigOption)
	conn.Handle(MethodKajiCodeSetModel, a.handleKajiCodeSetModel)
	conn.HandleNotify(MethodSessionCancel, a.handleCancel)
	return a
}

// Serve runs the connection read loop until the stream closes or ctx is done.
func (a *Agent) Serve(ctx context.Context) error { return a.conn.Serve(ctx) }

// ---- initialize ----

func (a *Agent) handleInitialize(_ context.Context, params json.RawMessage) (any, error) {
	var p InitializeParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	negotiated := ProtocolVersion
	if p.ProtocolVersion > 0 && p.ProtocolVersion < ProtocolVersion {
		negotiated = p.ProtocolVersion
	}
	a.mu.Lock()
	a.clientCaps = p.ClientCapabilities
	a.mu.Unlock()

	info := a.deps.AgentInfo
	// Advertise login methods only when the agent has an auth surface. Terminal
	// methods are included only if the client can launch them in a terminal.
	var authMethods []AuthMethod
	authCaps := AgentAuthCapabilities{}
	if a.deps.AuthMethods != nil {
		authMethods = a.deps.AuthMethods(p.ClientCapabilities.Auth.Terminal)
	}
	if a.deps.Logout != nil {
		authCaps.Logout = &struct{}{}
	}
	if authMethods == nil {
		authMethods = []AuthMethod{}
	}
	return InitializeResult{
		ProtocolVersion: negotiated,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			// KajiCode parses text, image, resource_link, and embedded resource
			// prompt blocks, so it advertises the matching prompt capabilities.
			PromptCapabilities: PromptCapabilities{Image: true, EmbeddedContext: true},
			// KajiCode owns its MCP configuration, so it never connects to
			// editor-supplied MCP servers over either remote transport.
			McpCapabilities: McpCapabilities{HTTP: false, SSE: false},
			SessionCapabilities: SessionCapabilities{
				List:                  &struct{}{},
				Resume:                &struct{}{},
				Close:                 &struct{}{},
				Delete:                &struct{}{},
				Fork:                  &struct{}{},
				AdditionalDirectories: &struct{}{},
			},
			Auth: authCaps,
		},
		AgentInfo: &info,
		// KAJICODE owns credentials (BYOK): it advertises login methods for its
		// own providers, but never delegates credential storage to the editor.
		AuthMethods: authMethods,
	}, nil
}

// ---- authenticate / logout ----

func (a *Agent) handleAuthenticate(ctx context.Context, params json.RawMessage) (any, error) {
	var p AuthenticateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid authenticate params")
	}
	if strings.TrimSpace(p.MethodID) == "" {
		return nil, RPCError(codeInvalidParams, "authenticate requires methodId")
	}
	if a.deps.Authenticate == nil {
		return nil, RPCError(codeInvalidParams, "unsupported auth method: "+p.MethodID)
	}
	if err := a.deps.Authenticate(ctx, p.MethodID); err != nil {
		return nil, RPCError(codeInternalError, "authenticate: "+err.Error())
	}
	return AuthenticateResult{}, nil
}

func (a *Agent) handleLogout(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) > 0 {
		var p LogoutParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, RPCError(codeInvalidParams, "invalid logout params")
		}
	}
	if a.deps.Logout == nil {
		return nil, RPCError(codeMethodNotFound, "logout not supported")
	}
	if err := a.deps.Logout(ctx); err != nil {
		return nil, RPCError(codeInternalError, "logout: "+err.Error())
	}
	return LogoutResult{}, nil
}

// ---- session lifecycle ----

func (a *Agent) handleSessionNew(_ context.Context, params json.RawMessage) (any, error) {
	var p NewSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/new params")
	}
	root, err := a.deps.ResolveWorkspaceRoot(p.Cwd)
	if err != nil {
		return nil, RPCError(codeInvalidParams, err.Error())
	}
	extra, err := a.validateAdditionalDirs(root, p.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	meta, err := a.deps.Store.Create(sessions.CreateInput{Title: "ACP session", Cwd: root})
	if err != nil {
		return nil, RPCError(codeInternalError, "create session: "+err.Error())
	}
	sess, _, err := a.newSession(meta.SessionID, root, extra, nil)
	if err != nil {
		return nil, err
	}
	note := &notifier{conn: a.conn, sessionID: sess.id}
	a.advertise(note, sess)
	return NewSessionResult{
		SessionID:     sess.id,
		ConfigOptions: a.configOptions(sess),
		Modes:         a.modeState(sess),
	}, nil
}

func (a *Agent) handleSessionLoad(_ context.Context, params json.RawMessage) (any, error) {
	var p LoadSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/load params")
	}
	meta, err := a.deps.Store.Get(p.SessionID)
	if err != nil || meta == nil {
		return nil, RPCError(codeInvalidParams, "session not found: "+p.SessionID)
	}
	cwdInput := p.Cwd
	if strings.TrimSpace(cwdInput) == "" {
		cwdInput = meta.Cwd
	}
	root, err := a.deps.ResolveWorkspaceRoot(cwdInput)
	if err != nil {
		return nil, RPCError(codeInvalidParams, err.Error())
	}
	extra, err := a.validateAdditionalDirs(root, p.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	// Load history BEFORE publishing the session so no concurrent prompt observes
	// a half-initialized session (registerSession sets history under the lock and
	// reuses an already-live session rather than orphaning its in-flight turn).
	history, historyErr := a.loadHistory(meta.SessionID)
	sess, created, err := a.newSession(meta.SessionID, root, extra, history)
	if err != nil {
		return nil, err
	}
	note := &notifier{conn: a.conn, sessionID: sess.id}
	a.warnPersistence(
		note,
		"load session history",
		"Could not load session history. The session is open, but earlier turns may be missing until storage recovers.",
		historyErr,
	)
	// The spec requires replaying the full transcript via session/update before
	// responding, so a re-opened editor renders the earlier conversation. Skip
	// the replay for an already-live session, whose in-memory history is newer.
	if created {
		a.replayHistory(note, history)
	}
	a.advertise(note, sess)
	return LoadSessionResult{
		ConfigOptions: a.configOptions(sess),
		Modes:         a.modeState(sess),
	}, nil
}

func (a *Agent) handleSessionResume(_ context.Context, params json.RawMessage) (any, error) {
	var p ResumeSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/resume params")
	}
	meta, err := a.deps.Store.Get(p.SessionID)
	if err != nil || meta == nil {
		return nil, RPCError(codeInvalidParams, "session not found: "+p.SessionID)
	}
	cwdInput := p.Cwd
	if strings.TrimSpace(cwdInput) == "" {
		cwdInput = meta.Cwd
	}
	root, err := a.deps.ResolveWorkspaceRoot(cwdInput)
	if err != nil {
		return nil, RPCError(codeInvalidParams, err.Error())
	}
	extra, err := a.validateAdditionalDirs(root, p.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	// Resume keeps any already-live session (and its in-flight turn); unlike load
	// it does not replay history. History is still loaded so later prompts carry
	// the earlier conversation as context.
	history, historyErr := a.loadHistory(meta.SessionID)
	sess, _, err := a.newSession(meta.SessionID, root, extra, history)
	if err != nil {
		return nil, err
	}
	note := &notifier{conn: a.conn, sessionID: sess.id}
	a.warnPersistence(
		note,
		"load session history",
		"Could not load session history. The session is open, but earlier turns may be missing until storage recovers.",
		historyErr,
	)
	a.advertise(note, sess)
	return ResumeSessionResult{
		ConfigOptions: a.configOptions(sess),
		Modes:         a.modeState(sess),
	}, nil
}

// handleSessionFork branches a new session from a stored one, copying its
// conversation (via Store.Fork) while leaving the parent untouched.
func (a *Agent) handleSessionFork(_ context.Context, params json.RawMessage) (any, error) {
	var p ForkSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/fork params")
	}
	parent, err := a.deps.Store.Get(p.SessionID)
	if err != nil || parent == nil {
		return nil, RPCError(codeInvalidParams, "session not found: "+p.SessionID)
	}
	cwdInput := p.Cwd
	if strings.TrimSpace(cwdInput) == "" {
		cwdInput = parent.Cwd
	}
	root, err := a.deps.ResolveWorkspaceRoot(cwdInput)
	if err != nil {
		return nil, RPCError(codeInvalidParams, err.Error())
	}
	extra, err := a.validateAdditionalDirs(root, p.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	forkMeta, err := a.deps.Store.Fork(p.SessionID, sessions.ForkInput{Cwd: root})
	if err != nil {
		return nil, RPCError(codeInternalError, "fork session: "+err.Error())
	}
	history, historyErr := a.loadHistory(forkMeta.SessionID)
	sess, _, err := a.newSession(forkMeta.SessionID, root, extra, history)
	if err != nil {
		return nil, err
	}
	note := &notifier{conn: a.conn, sessionID: sess.id}
	a.warnPersistence(
		note,
		"load session history",
		"Could not load session history. The session is open, but earlier turns may be missing until storage recovers.",
		historyErr,
	)
	// The fork inherits the parent's transcript; replay it so the client sees the
	// branched conversation immediately.
	a.replayHistory(note, history)
	a.advertise(note, sess)
	return ForkSessionResult{
		SessionID:     sess.id,
		ConfigOptions: a.configOptions(sess),
		Modes:         a.modeState(sess),
	}, nil
}

func (a *Agent) handleSessionList(_ context.Context, params json.RawMessage) (any, error) {
	var p ListSessionsParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	filter := ""
	if strings.TrimSpace(p.Cwd) != "" {
		resolved, err := a.deps.ResolveWorkspaceRoot(p.Cwd)
		if err != nil {
			return nil, RPCError(codeInvalidParams, err.Error())
		}
		filter = resolved
	}
	metas, err := a.deps.Store.List()
	if err != nil {
		return nil, RPCError(codeInternalError, "list sessions: "+err.Error())
	}
	infos := []SessionInfo{}
	for _, m := range metas {
		if !sessions.IsResumableKind(m.SessionKind) {
			continue
		}
		if filter != "" && m.Cwd != filter {
			continue
		}
		infos = append(infos, SessionInfo{
			SessionID: m.SessionID,
			Cwd:       m.Cwd,
			Title:     m.Title,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return ListSessionsResult{Sessions: infos}, nil
}

func (a *Agent) handleSessionClose(_ context.Context, params json.RawMessage) (any, error) {
	var p CloseSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/close params")
	}
	// Close detaches the session from this connection but keeps it on disk so it
	// can be resumed later. ACP requires close to cancel any ongoing work first,
	// so an in-flight turn stops rather than running on unreachable.
	a.dropSession(p.SessionID)
	return CloseSessionResult{}, nil
}

func (a *Agent) handleSessionDelete(_ context.Context, params json.RawMessage) (any, error) {
	var p DeleteSessionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/delete params")
	}
	if err := a.deps.Store.Delete(p.SessionID); err != nil {
		return nil, RPCError(codeInternalError, "delete session: "+err.Error())
	}
	a.dropSession(p.SessionID)
	return DeleteSessionResult{}, nil
}

// ---- prompt turn ----

func (a *Agent) handleSessionPrompt(ctx context.Context, params json.RawMessage) (any, error) {
	var p PromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid session/prompt params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}

	// Serialize turns for this session so two prompts can't interleave history or
	// fight over the single cancel slot. session/cancel still works concurrently
	// (it doesn't take turnMu).
	sess.turnMu.Lock()
	defer sess.turnMu.Unlock()

	userText := promptText(p.Prompt)
	images, err := promptImages(p.Prompt)
	if err != nil {
		return nil, err
	}

	turnCtx, cancel := context.WithCancel(ctx)
	sess.setCancel(cancel)
	defer func() {
		cancel()
		sess.setCancel(nil)
	}()

	// A leading "/name args" is a slash command: run it directly and stream its
	// text result, exactly as if the user had typed it in the TUI. `/retitle` is
	// handled here so the generated title can be surfaced via session_info_update.
	// Unknown commands fall through to the model so an editor never loses the prompt.
	if name, args, ok := splitSlashCommand(userText); ok {
		note := &notifier{conn: a.conn, sessionID: sess.id}
		if strings.EqualFold(name, "retitle") && a.deps.Retitle != nil {
			title, retitleErr := a.deps.Retitle(turnCtx, sess.id, sess.cwd)
			if retitleErr != nil {
				return nil, RPCError(codeInternalError, "retitle: "+retitleErr.Error())
			}
			note.sessionInfo(title)
			note.text("Retitled: " + title)
			return PromptResult{StopReason: StopEndTurn}, nil
		}
		// `/add-provider` collects a provider's non-secret config via an
		// elicitation form (the native editor UI), then writes the profile through
		// the shared CLI path. Secrets are never collected here.
		if strings.EqualFold(name, "add-provider") {
			if a.deps.ProviderAdd == nil {
				note.text("Provider creation is not available in this session.")
				return PromptResult{StopReason: StopEndTurn}, nil
			}
			output, addErr := a.runAddProvider(turnCtx, sess.id)
			if addErr != nil {
				return nil, RPCError(codeInternalError, "add-provider: "+addErr.Error())
			}
			note.text(output)
			return PromptResult{StopReason: StopEndTurn}, nil
		}
		if a.deps.RunCommand != nil {
			if output, handled, runErr := a.deps.RunCommand(turnCtx, name, args, sess.cwd, sess.id); handled {
				note.text(output)
				if runErr != nil {
					return nil, RPCError(codeInternalError, name+": "+runErr.Error())
				}
				return PromptResult{StopReason: StopEndTurn}, nil
			}
		}
	}

	reason, err := a.runTurn(turnCtx, sess, userText, images)
	if err != nil {
		return nil, err
	}
	return PromptResult{StopReason: reason}, nil
}

// runAddProvider collects a provider's NON-SECRET configuration through an
// elicitation form and writes the profile via Deps.ProviderAdd. Credentials are
// never requested over ACP: form elicitation MUST NOT carry secrets
// (docs/protocol/v1/elicitation.mdx), so the API key is entered separately with
// the terminal sign-in flow (`kajicode auth login` / `kajicode providers add`).
func (a *Agent) runAddProvider(ctx context.Context, sessionID string) (string, error) {
	fields, supported, err := a.elicitForm(ctx, sessionID, "Add a KajiCode provider (no secrets)", providerAddSchema())
	if err != nil {
		return "", err
	}
	if !supported {
		return "Adding a provider needs form input this editor does not support.\n" +
			"Run `kajicode providers add <provider> --name <name> [--base-url <url>]` in a terminal instead.", nil
	}
	if len(fields) == 0 {
		return "Provider not added (cancelled).", nil
	}
	if strings.TrimSpace(fields["name"]) == "" {
		return "Provider not added: a name is required.", nil
	}
	summary, err := a.deps.ProviderAdd(ctx, fields)
	if err != nil {
		return "", err
	}
	return summary + "\nSet the API key with: kajicode providers add --name " + strings.TrimSpace(fields["name"]) +
		" --api-key-env <ENV_VAR>  (or run `kajicode auth login <provider>` for OAuth providers).", nil
}

// providerAddSchema is the elicitation form for adding a provider. It carries
// only non-secret fields — no API key, per the spec's form-mode prohibition.
func providerAddSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":       map[string]any{"type": "string", "title": "Name (unique id for this provider)"},
			"baseUrl":    map[string]any{"type": "string", "title": "Base URL (leave blank for a catalog provider)"},
			"model":      map[string]any{"type": "string", "title": "Default model"},
			"authHeader": map[string]any{"type": "string", "title": "Auth header name (custom providers only)"},
			"authScheme": map[string]any{"type": "string", "title": "Auth scheme, e.g. Bearer (custom providers only)"},
			"customKind": map[string]any{"type": "string", "title": "For a custom endpoint: custom-openai-compatible or custom-anthropic-compatible"},
		},
		"required": []string{"name"},
	}
}

func (a *Agent) runTurn(ctx context.Context, sess *acpSession, userText string, images []kajicoderuntime.ImageBlock) (string, error) {
	overrides := config.Overrides{}
	if model := sess.currentModel(); model != "" {
		overrides.Provider.Model = model
	}
	resolved, err := a.deps.ResolveConfig(sess.cwd, overrides)
	if err != nil {
		return "", RPCError(codeInternalError, "config: "+err.Error())
	}
	// Normalize client images into the configured provider-safe envelope, exactly
	// like the exec/TUI surfaces, so an oversized screenshot is resized rather than
	// rejected by the provider.
	images, err = normalizeSessionImages(images, resolved.Images)
	if err != nil {
		return "", RPCError(codeInvalidParams, err.Error())
	}
	// Additional directories widen the sandbox scope for this session.
	if len(sess.extra) > 0 {
		resolved.Sandbox.AdditionalWriteRoots = append(resolved.Sandbox.AdditionalWriteRoots, sess.extra...)
	}
	provider, err := a.deps.NewProvider(resolved.Provider)
	if err != nil {
		return "", RPCError(codeInternalError, "provider: "+err.Error())
	}
	// Build the SCOPED registry + sandbox engine + skill catalog for this session's
	// workspace so shell/file tools are confined to the workspace exactly like the
	// exec surface, and the system prompt advertises the same skills the skill tool
	// can load.
	workspace, err := a.deps.BuildWorkspace(sess.cwd, resolved)
	if err != nil {
		return "", RPCError(codeInternalError, "workspace: "+err.Error())
	}
	registry := workspace.Registry
	note := &notifier{conn: a.conn, sessionID: sess.id}

	maxTurns := resolved.MaxTurns
	if t := sess.turnBudget(); t > 0 {
		maxTurns = t
	}

	opts := agent.Options{
		Cwd:             sess.cwd,
		SessionID:       sess.id,
		ProviderName:    resolved.Provider.Name,
		Model:           resolved.Provider.Model,
		ReasoningEffort: sess.effort(),
		ResponseStyle:   sess.responseStyle(),
		Registry:        registry,
		Sandbox:         workspace.Sandbox,
		PermissionMode:  sess.currentMode(),
		MaxTurns:        maxTurns,
		Images:          images,
		ImageLimits:     imageinput.LimitsFrom(resolved.Images.MaxWidth, resolved.Images.MaxHeight, resolved.Images.MaxBytes, resolved.Images.AutoResize),
		Skills:          workspace.Skills,
		OnText:          note.text,
		OnReasoning:     note.thought,
		OnUsage:         func(u agent.Usage) { note.usage(u.TotalTokens(), 0) },
		OnToolCall:      note.toolCall,
		OnToolResult: func(result agent.ToolResult) {
			note.toolResult(result)
			if result.Name == "todo_write" {
				a.emitPlan(registry, note)
			}
		},
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			return a.requestPermission(ctx, sess.id, req)
		},
		// Route ask_user to the editor via elicitation when it advertises
		// support; otherwise leave it nil and the agent uses its headless,
		// non-blocking fallback.
		OnAskUser: a.askUserHandler(sess.id),
	}

	agentPrompt := buildPrompt(sess.snapshotHistory(), userText)
	result, runErr := a.deps.RunAgent(ctx, agentPrompt, provider, opts)

	reason, stopErr := stopReasonFor(result, runErr)
	if stopErr != nil {
		return "", RPCError(codeInternalError, stopErr.Error())
	}
	if err := a.persistTurn(sess, userText, result.FinalAnswer); err != nil {
		a.warnPersistence(
			note,
			"save session history",
			"Could not save session history. This turn is available in memory, but future resume may miss it until storage recovers.",
			err,
		)
	}
	return reason, nil
}

func stopReasonFor(result agent.Result, err error) (string, error) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return StopCancelled, nil
		}
		return "", err
	}
	if result.FinishReason == "length" {
		return StopMaxTokens, nil
	}
	if result.FinishReason == "content_filter" {
		return StopRefusal, nil
	}
	return StopEndTurn, nil
}

// requestPermission forwards a KAJICODE permission prompt to the client as an ACP
// session/request_permission request and maps the outcome back. Failure to reach
// the client fails closed to deny.
func (a *Agent) requestPermission(ctx context.Context, sessionID string, req agent.PermissionRequest) (agent.PermissionDecision, error) {
	params := RequestPermissionParams{
		SessionID: sessionID,
		ToolCall:  permissionToolCall(req),
		Options:   buildPermissionOptions(req),
	}
	var result RequestPermissionResult
	if err := a.conn.Call(ctx, MethodSessionRequestPerm, params, &result); err != nil {
		if errors.Is(err, context.Canceled) {
			return agent.PermissionDecision{Action: agent.PermissionDecisionCancel, Reason: "cancelled"}, nil
		}
		return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission request failed: " + err.Error()}, nil
	}
	return decisionFromOutcome(result.Outcome, req.AvailableDecisions), nil
}

func (a *Agent) emitPlan(registry *tools.Registry, note *notifier) {
	t, ok := registry.Get("todo_write")
	if !ok {
		return
	}
	planner, ok := t.(interface{ CurrentTodos() []tools.PlanItem })
	if !ok {
		return
	}
	note.plan(planner.CurrentTodos())
}

// ---- mode + model selection ----

func (a *Agent) handleSetMode(_ context.Context, params json.RawMessage) (any, error) {
	var p SetSessionModeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid set_mode params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}
	if !validPermissionMode(p.ModeID) {
		return nil, RPCError(codeInvalidParams, "unknown mode: "+p.ModeID)
	}
	sess.setMode(agent.PermissionMode(p.ModeID))
	note := &notifier{conn: a.conn, sessionID: sess.id}
	note.currentMode(p.ModeID)
	note.configOptions(a.configOptions(sess))
	return SetSessionModeResult{}, nil
}

func (a *Agent) handleSetConfigOption(_ context.Context, params json.RawMessage) (any, error) {
	var p SetSessionConfigOptionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid set_config_option params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}
	value := strings.TrimSpace(p.Value)
	switch p.ConfigID {
	case configIDModel:
		sess.setModel(value)
	case configIDMode:
		if !validPermissionMode(value) {
			return nil, RPCError(codeInvalidParams, "unknown mode: "+value)
		}
		sess.setMode(agent.PermissionMode(value))
	case configIDEffort:
		sess.setEffort(value)
	case configIDTurns:
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return nil, RPCError(codeInvalidParams, "invalid turns value: "+value)
		}
		sess.setTurnBudget(n)
	case configIDStyle:
		sess.setResponseStyle(value)
	default:
		return nil, RPCError(codeInvalidParams, "unknown config option: "+p.ConfigID)
	}
	options := a.configOptions(sess)
	(&notifier{conn: a.conn, sessionID: sess.id}).configOptions(options)
	return SetSessionConfigOptionResult{ConfigOptions: options}, nil
}

func (a *Agent) handleKajiCodeSetModel(_ context.Context, params json.RawMessage) (any, error) {
	var p KajiCodeSetModelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid _kajicode/set_model params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}
	sess.setModel(strings.TrimSpace(p.Model))
	(&notifier{conn: a.conn, sessionID: sess.id}).configOptions(a.configOptions(sess))
	return KajiCodeSetModelResult{Model: p.Model}, nil
}

func (a *Agent) handleCancel(_ context.Context, params json.RawMessage) {
	var p CancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if sess := a.session(p.SessionID); sess != nil {
		sess.invokeCancel()
	}
}

// ---- advertising helpers ----

// modeState is the legacy session-modes view. It lists exactly the modes
// validPermissionMode accepts, so a client that only understands modes cannot
// reach an unconfined profile that the config-option path rejects.
func (a *Agent) modeState(s *acpSession) *SessionModeState {
	return &SessionModeState{
		CurrentModeID: string(s.currentMode()),
		AvailableModes: []SessionMode{
			{ID: string(agent.PermissionModeAuto), Name: "Auto", Description: "Run safe tools automatically; ask before risky ones."},
			{ID: string(agent.PermissionModeAskAll), Name: "Ask", Description: "Ask before every tool that changes state."},
			{ID: string(agent.PermissionModeReadOnly), Name: "Read only", Description: "Allow reads; ask for writes, shell, and network."},
			{ID: string(agent.PermissionModeReadWrite), Name: "Read + write", Description: "Allow reads and file writes; ask for shell and network."},
		},
	}
}

// configOptions returns the full set of session config selectors, in priority
// order, with their current values.
func (a *Agent) configOptions(sess *acpSession) []SessionConfigOption {
	model := sess.currentModel()
	values := a.modelValues(sess)
	if model == "" && len(values) > 0 {
		model = values[0].Value
	}
	options := []SessionConfigOption{
		selectOption(configIDModel, "Model", "Active model for this session.", configCategoryModel, model, values),
		selectOption(configIDMode, "Permissions", "How tool calls are authorized.", configCategoryMode, string(sess.currentMode()), permissionModeValues()),
		selectOption(configIDEffort, "Reasoning effort", "Reasoning effort for supported models.", configCategoryThought, effortValue(sess.effort()), effortValues()),
		selectOption(configIDTurns, "Turn budget", "Maximum tool turns per prompt.", "_kajicode", strconv.Itoa(a.turnBudget(sess)), turnValues(a.turnBudget(sess))),
		selectOption(configIDStyle, "Response style", "Reply style directive.", "_kajicode", styleValue(sess.responseStyle()), styleValues()),
	}
	return options
}

// modelValues lists the provider-qualified model ids available to switch to. The
// resolved model is always included first so the option has a valid default.
func (a *Agent) modelValues(sess *acpSession) []SessionConfigOptionValue {
	seen := map[string]bool{}
	values := []SessionConfigOptionValue{}
	add := func(model string) {
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		values = append(values, SessionConfigOptionValue{Value: model, Name: model})
	}
	add(sess.resolved.Provider.Model)
	add(sess.currentModel())
	for _, p := range sess.resolved.Providers {
		add(p.Model)
	}
	add(sess.resolved.DefaultModel)
	return values
}

func (a *Agent) turnBudget(sess *acpSession) int {
	if t := sess.turnBudget(); t > 0 {
		return t
	}
	if sess.resolved.MaxTurns > 0 {
		return sess.resolved.MaxTurns
	}
	return 0
}

// advertise emits the slash-command catalog and config options for a session.
func (a *Agent) advertise(note *notifier, sess *acpSession) {
	if len(a.deps.Commands) > 0 {
		note.availableCommands(a.deps.Commands)
	}
	note.configOptions(a.configOptions(sess))
}

// replayHistory re-emits a stored conversation as session/update chunks so a
// re-opened editor renders the earlier turns.
func (a *Agent) replayHistory(note *notifier, history []turnRecord) {
	for _, t := range history {
		note.userText(t.user)
		note.text(t.assistant)
	}
}

// ---- persistence + continuity ----

func (a *Agent) persistTurn(sess *acpSession, user, assistant string) error {
	defer sess.appendHistory(turnRecord{user: user, assistant: assistant})
	if a.deps.Store == nil {
		return nil
	}
	events := []sessions.AppendEventInput{
		{
			Type:    sessions.EventMessage,
			Payload: map[string]any{"role": "user", "content": user},
		},
	}
	if assistant != "" {
		events = append(events, sessions.AppendEventInput{
			Type:    sessions.EventMessage,
			Payload: map[string]any{"role": "assistant", "content": assistant},
		})
	}
	_, err := a.deps.Store.AppendEvents(sess.id, events)
	return err
}

func (a *Agent) loadHistory(sessionID string) ([]turnRecord, error) {
	if a.deps.Store == nil {
		return nil, nil
	}
	events, err := a.deps.Store.ReadEvents(sessionID)
	if err != nil {
		return nil, err
	}
	var records []turnRecord
	var pendingUser string
	havePending := false
	for _, e := range events {
		if e.Type != sessions.EventMessage {
			continue
		}
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			continue
		}
		var msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Role {
		case "user":
			if havePending {
				records = append(records, turnRecord{user: pendingUser})
			}
			pendingUser = msg.Content
			havePending = true
		case "assistant":
			records = append(records, turnRecord{user: pendingUser, assistant: msg.Content})
			pendingUser = ""
			havePending = false
		}
	}
	if havePending {
		records = append(records, turnRecord{user: pendingUser})
	}
	return records, nil
}

func (a *Agent) warnPersistence(note *notifier, action string, message string, err error) {
	if err == nil {
		return
	}
	sessionID := ""
	if note != nil {
		sessionID = note.sessionID
	}
	log.Printf("kajicode acp: failed to %s for session %s: %v", action, sessionID, err)
	if note != nil {
		note.text("\n\n[kajicode warning] " + message + "\n")
	}
}

// newSession resolves config for the root, validates additional directories,
// and publishes the session. It returns an error the caller maps to a JSON-RPC
// error, mirroring what handleSessionNew used to do inline.
func (a *Agent) newSession(id, root string, extra []string, history []turnRecord) (*acpSession, bool, error) {
	resolved, err := a.deps.ResolveConfig(root, config.Overrides{})
	if err != nil {
		return nil, false, RPCError(codeInternalError, "config: "+err.Error())
	}
	mode := agent.PermissionModeAuto
	if p := strings.TrimSpace(resolved.Preferences.PermissionProfile); p != "" {
		// Only inherit a profile an editor is allowed to hold. A configured
		// `unsafe`/`bypass-all` default must not leak unconfined host access into
		// an ACP session, so fall back to Auto.
		if normalized := agent.NormalizePermissionMode(agent.PermissionMode(p)); validPermissionMode(string(normalized)) {
			mode = normalized
		}
	}
	sess, created := a.registerSession(id, root, resolved, extra, mode, history)
	return sess, created, nil
}

// validateAdditionalDirs resolves and confines every client-supplied additional
// directory to a subdirectory of the workspace root. An editor may widen its own
// write scope within the project it opened, but must not be able to hand itself
// the whole filesystem (or an ancestor of home) through this field.
func (a *Agent) validateAdditionalDirs(root string, dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		resolved, err := a.deps.ResolveWorkspaceRoot(d)
		if err != nil {
			return nil, RPCError(codeInvalidParams, "additional directory: "+err.Error())
		}
		if !withinRoot(root, resolved) {
			return nil, RPCError(codeInvalidParams, "additional directory must be inside the workspace: "+resolved)
		}
		out = append(out, resolved)
	}
	return out, nil
}

// withinRoot reports whether path is root itself or a descendant of it.
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// buildPrompt prepends prior conversation as context, since agent.Run drives a
// single seeded turn. Mirrors how headless resume folds history into the prompt.
func buildPrompt(history []turnRecord, userText string) string {
	if len(history) == 0 {
		return userText
	}
	var b strings.Builder
	b.WriteString("Conversation so far:\n")
	for _, t := range history {
		b.WriteString("User: ")
		b.WriteString(t.user)
		b.WriteString("\n")
		if t.assistant != "" {
			b.WriteString("Assistant: ")
			b.WriteString(t.assistant)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n---\nContinue with this request:\n")
	b.WriteString(userText)
	return b.String()
}

// maxPromptImageBytes caps a single base64-decoded client image at 10 MiB, the
// same pre-decode bound stream-json enforces, so a client cannot force an
// unbounded allocation by advertising a huge image. The image is normalized into
// the configured envelope afterward, which is the size actually sent.
const maxPromptImageBytes = 10 << 20

// promptImages decodes every image block carried on the prompt. A block with
// malformed base64 or one whose decoded size exceeds the cap fails the request
// loudly rather than being dropped, so a client is never silently answered from a
// text-only turn it did not intend.
func promptImages(blocks []ContentBlock) ([]kajicoderuntime.ImageBlock, error) {
	var images []kajicoderuntime.ImageBlock
	for _, blk := range blocks {
		if blk.Type != "image" || blk.Data == "" {
			continue
		}
		// Bound the encoded length before decoding so the decoder never allocates
		// more than the cap: 4 base64 chars encode 3 bytes.
		if len(blk.Data) > (maxPromptImageBytes/3+1)*4 {
			return nil, RPCError(codeInvalidParams, "image exceeds the 10 MiB limit")
		}
		data, err := base64.StdEncoding.DecodeString(blk.Data)
		if err != nil {
			return nil, RPCError(codeInvalidParams, "image data is not valid base64")
		}
		if len(data) > maxPromptImageBytes {
			return nil, RPCError(codeInvalidParams, "image exceeds the 10 MiB limit")
		}
		images = append(images, kajicoderuntime.ImageBlock{MediaType: blk.MimeType, Data: data})
	}
	return images, nil
}

// normalizeSessionImages puts client-supplied images through the shared
// provider-safe loader so the ACP surface matches exec and the TUI. A block that
// cannot be decoded or normalized fails the request with a params error rather
// than being dropped, so a client is told its image was rejected instead of
// silently losing it.
func normalizeSessionImages(images []kajicoderuntime.ImageBlock, cfg config.ImagesConfig) ([]kajicoderuntime.ImageBlock, error) {
	if len(images) == 0 {
		return nil, nil
	}
	limits := imageinput.LimitsFrom(cfg.MaxWidth, cfg.MaxHeight, cfg.MaxBytes, cfg.AutoResize)
	out := make([]kajicoderuntime.ImageBlock, 0, len(images))
	for _, img := range images {
		norm, err := imageinput.Normalize(img.MediaType, img.Data, limits)
		if err != nil {
			return nil, err
		}
		out = append(out, norm)
	}
	return out, nil
}

// splitSlashCommand parses a leading "/name args" prompt into a command name
// (without the slash) and its argument string.
func splitSlashCommand(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	body := strings.TrimSpace(trimmed[1:])
	if body == "" {
		return "", "", false
	}
	if i := strings.IndexAny(body, " \t\n"); i >= 0 {
		return body[:i], strings.TrimSpace(body[i+1:]), true
	}
	return body, "", true
}

// ---- session registry + accessors ----

// registerSession publishes a session under the agent's lock. If one is already
// registered for id (e.g. a re-load of an in-flight session) the existing live
// session is returned unchanged rather than orphaning its turn or resetting its
// mode/model. history is set BEFORE publishing so no concurrent prompt can read a
// half-initialized session.
// The bool reports whether this call created the session. session/load must not
// replay history for an already-live session: the in-memory conversation is
// newer than disk, so replaying stale disk history would mislead the client.
func (a *Agent) registerSession(id, cwd string, resolved config.ResolvedConfig, extra []string, mode agent.PermissionMode, history []turnRecord) (*acpSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing := a.sessions[id]; existing != nil {
		return existing, false
	}
	sess := &acpSession{
		id:       id,
		cwd:      cwd,
		resolved: resolved,
		extra:    extra,
		mode:     mode,
		history:  history,
	}
	a.sessions[id] = sess
	return sess, true
}

func (a *Agent) session(id string) *acpSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *Agent) dropSession(id string) {
	a.mu.Lock()
	sess := a.sessions[id]
	delete(a.sessions, id)
	a.mu.Unlock()
	// Cancel after unregistering: the session is gone from the map, so any
	// concurrent session/cancel is a no-op regardless of ordering.
	if sess != nil {
		sess.invokeCancel()
	}
}

func (s *acpSession) setCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
}

func (s *acpSession) invokeCancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *acpSession) setMode(mode agent.PermissionMode) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *acpSession) currentMode() agent.PermissionMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == "" {
		return agent.PermissionModeAuto
	}
	return s.mode
}

func (s *acpSession) setModel(model string) {
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
}

func (s *acpSession) currentModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *acpSession) setEffort(effort string) {
	s.mu.Lock()
	s.effortLevel = effort
	s.mu.Unlock()
}

func (s *acpSession) effort() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.effortLevel == "auto" {
		return ""
	}
	return s.effortLevel
}

func (s *acpSession) setTurnBudget(turns int) {
	s.mu.Lock()
	s.turns = turns
	s.mu.Unlock()
}

func (s *acpSession) turnBudget() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns
}

func (s *acpSession) setResponseStyle(style string) {
	s.mu.Lock()
	s.style = style
	s.mu.Unlock()
}

func (s *acpSession) responseStyle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.style
}

func (s *acpSession) appendHistory(rec turnRecord) {
	s.mu.Lock()
	s.history = append(s.history, rec)
	s.mu.Unlock()
}

func (s *acpSession) snapshotHistory() []turnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]turnRecord(nil), s.history...)
}
