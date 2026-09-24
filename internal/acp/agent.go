package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/execprofile"
	"github.com/dishant0406/KajiCode/internal/imageinput"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/lsp"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
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
	// Commands returns the slash-command catalog advertised to the client via
	// available_commands_update for a workspace. It is a function (not a slice)
	// so user-defined prompt snippets saved mid-session appear immediately when
	// the catalog is re-emitted. nil advertises none.
	Commands func(workspaceRoot string) []AvailableCommand
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
	// SavePromptSnippet writes a reusable prompt snippet (slug + body). It
	// returns the saved name for confirmation. nil disables /prompt creation.
	SavePromptSnippet func(workspaceRoot, slug, body string) (string, error)
	// ExpandPromptSnippet resolves "/name args" against the saved snippets,
	// returning the expanded prompt text. ok=false means no such snippet.
	ExpandPromptSnippet func(workspaceRoot, name, args string) (string, bool)
	// ExpandSkill resolves a "/name args" against the workspace's installed
	// skills, returning the prompt to run (the skill body plus the request).
	// ok=false means no skill matches. Checked after prompt snippets and before
	// the command catalog, matching the TUI's builtin > user command > skill
	// precedence. nil disables skill invocation, falling through to the model.
	ExpandSkill func(workspaceRoot, name, args string) (string, bool)
	// ProviderAdd creates/updates a provider from NON-SECRET fields (name,
	// baseUrl, model, authHeader, authScheme, customKind). Credentials are never
	// passed here (form elicitation must not carry secrets). It returns a
	// human-readable summary. nil disables the provider-add flow.
	ProviderAdd func(ctx context.Context, fields map[string]string) (string, error)
	// DiscoverModels lists the models a provider actually serves (live discovery,
	// catalog fallback), for the model config selector. nil falls back to the
	// session's resolved model set.
	DiscoverModels func(ctx context.Context, profile config.ProviderProfile) ([]SessionConfigOptionValue, error)
	// Providers returns the usable saved provider profiles (credential-bearing
	// or no-auth local) so the model selector lists every provider's models, the
	// way the TUI picker does, not only the active provider's. nil means "active
	// provider only".
	Providers func() []config.ProviderProfile
	// ResolveContextWindow returns a model's context window (max input tokens)
	// for the profile's model, resolved from the curated registry then models.dev
	// (as exec/TUI do, minus the live-discovery step since ACP resolves per turn
	// rather than once per run). The raw value feeds the ACP usage_update size;
	// agent.Options.ContextWindow wraps it in modelregistry.AgentContextWindow so
	// compaction is enabled even for an unknown model. 0 means unknown.
	ResolveContextWindow func(profile config.ProviderProfile) int
	// SuggestNes produces a next-edit suggestion for one buffered document.
	// nil means nes/suggest returns an empty suggestion list.
	SuggestNes func(ctx context.Context, input NesSuggestInput) (*NesEditSuggestion, error)
	// ListProviders returns the configured providers (non-secret routing config).
	// nil means providers/list returns an empty list.
	ListProviders func() ([]ProviderInfo, error)
	// SetProvider applies a non-secret routing config for a provider.
	SetProvider func(providerID, apiType, baseURL string, headers map[string]string) error
	// DisableProvider disables a provider.
	DisableProvider func(providerID string) error
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

	mu               sync.Mutex
	mode             agent.PermissionMode
	model            string // override; "" => config default
	provider         string // provider name override; "" => resolved active provider
	effortLevel      string // reasoning effort override; "" => model default
	turns            int    // turn budget override; 0 => resolved default
	style            string // response style override; "" => balanced
	selfCorrectDepth string // post-edit self-correct depth; "" => off
	execProfileName  string // execution profile name; "" => balanced (empty posture)
	// modelCache holds per-provider discovered model lists, keyed by provider
	// name. Populated lazily and cleared by _kajicode/refresh_models.
	modelCache map[string][]SessionConfigOptionValue
	cancel     context.CancelFunc
	history    []turnRecord

	// v1-unstable editor->agent document sync: the last-known buffer per URI.
	docs map[string]acpDocument
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
	conn.Handle(MethodKajiCodeRefreshModels, a.handleKajiCodeRefreshModels)
	conn.Handle(MethodNesStart, a.handleNesStart)
	conn.Handle(MethodNesSuggest, a.handleNesSuggest)
	conn.Handle(MethodNesClose, a.handleNesClose)
	conn.Handle(MethodProvidersList, a.handleProvidersList)
	conn.Handle(MethodProvidersSet, a.handleProvidersSet)
	conn.Handle(MethodProvidersDisable, a.handleProvidersDisable)
	conn.HandleNotify(MethodSessionCancel, a.handleCancel)
	conn.HandleNotify(MethodDocumentDidOpen, a.handleDidOpen)
	conn.HandleNotify(MethodDocumentDidChange, a.handleDidChange)
	conn.HandleNotify(MethodDocumentDidClose, a.handleDidClose)
	conn.HandleNotify(MethodDocumentDidSave, a.handleDidSave)
	conn.HandleNotify(MethodDocumentDidFocus, a.handleDidFocus)
	conn.HandleNotify(MethodNesAccept, a.handleNesAccept)
	conn.HandleNotify(MethodNesReject, a.handleNesReject)
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
			// v1 unstable: advertise provider config and NES document events.
			Providers: &struct{}{},
			Nes: &NesCapabilities{Events: &NesEventCapabilities{
				Document: &NesDocumentEventCapabilities{
					DidOpen:   &struct{}{},
					DidChange: &NesDidChangeCapabilities{SyncKind: "full"},
					DidClose:  &struct{}{},
					DidSave:   &struct{}{},
					DidFocus:  &struct{}{},
				},
			}},
			PositionEncoding: "utf-8",
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
		ConfigOptions: a.configOptions(context.Background(), sess),
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
		ConfigOptions: a.configOptions(context.Background(), sess),
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
		ConfigOptions: a.configOptions(context.Background(), sess),
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
		ConfigOptions: a.configOptions(context.Background(), sess),
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
		// `/refresh-models` re-runs discovery across every provider and re-emits
		// the model selector, so an editor that cannot call the vendor
		// _kajicode/refresh_models method still has a reachable refresh.
		if strings.EqualFold(name, "refresh-models") {
			note.text(a.runRefreshModels(turnCtx, sess))
			return PromptResult{StopReason: StopEndTurn}, nil
		}
		// `/skills` lists this session's skills. It is handled here rather than
		// routed to the `kajicode skills list` CLI command, whose management listing
		// is deliberately global-only: the session listing must include the
		// workspace's project skills too, matching the model's own skill catalog.
		// An empty arg or the explicit `list` form both take this path so
		// `/skills` and `/skills list` cannot disagree.
		if strings.EqualFold(name, "skills") && (strings.TrimSpace(args) == "" || strings.EqualFold(strings.TrimSpace(args), "list")) {
			note.text(a.listSkills(sess))
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
		// `/prompt` creates a reusable prompt snippet (slug + body) via a form;
		// saving re-emits the command catalog so the new /name appears in the
		// client's palette.
		if strings.EqualFold(name, "prompt") && a.deps.SavePromptSnippet != nil {
			output, saved, promptErr := a.runCreatePrompt(turnCtx, sess.cwd, sess.id, args)
			if promptErr != nil {
				return nil, RPCError(codeInternalError, "prompt: "+promptErr.Error())
			}
			note.text(output)
			if saved {
				a.advertiseCommands(note, sess)
			}
			return PromptResult{StopReason: StopEndTurn}, nil
		}
		// A saved prompt snippet invoked as `/name args` expands into the prompt
		// text and runs as an ordinary turn (ACP has no "fill the composer").
		if a.deps.ExpandPromptSnippet != nil {
			if expanded, ok := a.deps.ExpandPromptSnippet(sess.cwd, name, args); ok {
				reason, runErr := a.runTurn(turnCtx, sess, expanded, images)
				if runErr != nil {
					return nil, runErr
				}
				return PromptResult{StopReason: reason}, nil
			}
		}
		// An installed skill invoked as `/name args` expands into the skill body
		// plus the request and runs as an ordinary turn. Checked after prompt
		// snippets (builtin > user command > skill), matching the TUI so a name
		// shared by both resolves the same way in every surface.
		if a.deps.ExpandSkill != nil {
			if expanded, ok := a.deps.ExpandSkill(sess.cwd, name, args); ok {
				reason, runErr := a.runTurn(turnCtx, sess, expanded, images)
				if runErr != nil {
					return nil, runErr
				}
				return PromptResult{StopReason: reason}, nil
			}
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

// runCreatePrompt collects a snippet's slug and body through a form and saves
// it. saved reports whether a snippet was written (so the caller re-emits the
// catalog). Secrets are never requested, so form elicitation is valid here.
func (a *Agent) runCreatePrompt(ctx context.Context, workspaceRoot, sessionID, args string) (string, bool, error) {
	presetSlug := strings.TrimSpace(args)
	fields, supported, err := a.elicitForm(ctx, sessionID, "Create a reusable prompt (/name)", promptCreateSchema(presetSlug))
	if err != nil {
		return "", false, err
	}
	if !supported {
		return "Creating a prompt needs form input this editor does not support.\n" +
			"Create it in a terminal with `kajicode prompt edit` (or the TUI /prompt).", false, nil
	}
	if len(fields) == 0 {
		return "Prompt not created (cancelled).", false, nil
	}
	slug := strings.TrimSpace(fields["slug"])
	body := fields["body"]
	if slug == "" || strings.TrimSpace(body) == "" {
		return "Prompt not created: a name and a body are required.", false, nil
	}
	name, err := a.deps.SavePromptSnippet(workspaceRoot, slug, body)
	if err != nil {
		if strings.Contains(err.Error(), "exist") {
			return "Prompt /" + slug + " already exists. Choose another name.", false, nil
		}
		return "", false, err
	}
	return "Saved prompt /" + name + ". It now appears in the command list; run it as /" + name + " <args>.", true, nil
}

// promptCreateSchema is the /prompt form: a slug and the reusable body text.
func promptCreateSchema(presetSlug string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{"type": "string", "title": "Name (lowercase letters, numbers, hyphens)", "default": presetSlug},
			"body": map[string]any{"type": "string", "title": "Prompt body ($ARGUMENTS / $1 placeholders allowed)"},
		},
		"required": []string{"slug", "body"},
	}
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
	// A session whose model selector chose a model from another provider switches
	// providers for its turns too (mirrors the TUI picker). The stored provider
	// name is the saved profile name, which is the key config resolution uses.
	if provider := sess.currentProvider(); provider != "" && !strings.EqualFold(provider, sess.resolved.ActiveProvider) {
		overrides.ActiveProvider = provider
	}
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

	// The execution profile (when selected) fills the knobs the session left
	// unset and may displace the turn budget — the same fill-only-if-unset
	// precedence exec applies via applyExecProfile/applyProfileTurnBudget.
	profile, hasProfile := execprofile.Lookup(sess.execProfile())
	effort := sess.effort()
	effortFilledByProfile := false
	if hasProfile && effort == "" && profile.ReasoningEffort != "" {
		effort = profile.ReasoningEffort
		effortFilledByProfile = true
	}
	displacedMaxTurns := 0
	if hasProfile && profile.MaxTurns > 0 && sess.turnBudget() == 0 {
		displacedMaxTurns = maxTurns
		maxTurns = profile.MaxTurns
	}

	// Self-correct + execution profile come from the session selectors, matching
	// how the TUI overlays them onto agent.Options right before a run. A profile
	// that arms self-correction turns it on even when the session knob is off.
	selfCorrector, fileDiagnostics, selfCorrectCleanup := a.buildSelfCorrect(sess, hasProfile && profile.SelfCorrect)
	defer selfCorrectCleanup()
	var profilePolicy *agent.ProfilePolicy
	if hasProfile {
		profilePolicy = profile.Policy(displacedMaxTurns, effortFilledByProfile)
	}

	// The model's context window drives both compaction (agent.Options.ContextWindow)
	// and the ACP usage_update size. Two values on purpose: the raw window is the
	// display denominator (0 = unknown, so the client shows no gauge), while
	// compaction uses modelregistry.AgentContextWindow so an uncatalogued
	// proxy/custom model still gets the positive fallback and compacts — exactly
	// what exec/TUI do.
	contextWindow := 0
	if a.deps.ResolveContextWindow != nil {
		contextWindow = a.deps.ResolveContextWindow(resolved.Provider)
	}

	opts := agent.Options{
		Cwd:             sess.cwd,
		SessionID:       sess.id,
		ProviderName:    resolved.Provider.Name,
		Model:           resolved.Provider.Model,
		ReasoningEffort: effort,
		ResponseStyle:   sess.responseStyle(),
		SelfCorrect:     selfCorrector,
		FileDiagnostics: fileDiagnostics,
		Profile:         profilePolicy,
		Registry:        registry,
		Sandbox:         workspace.Sandbox,
		PermissionMode:  sess.currentMode(),
		MaxTurns:        maxTurns,
		ContextWindow:   modelregistry.AgentContextWindow(contextWindow),
		Images:          images,
		ImageLimits:     imageinput.LimitsFrom(resolved.Images.MaxWidth, resolved.Images.MaxHeight, resolved.Images.MaxBytes, resolved.Images.AutoResize),
		Skills:          workspace.Skills,
		OnText:          note.text,
		OnReasoning:     note.thought,
		// Report real token counts with the resolved context window as size, so the
		// client's context gauge has a denominator (matches the TUI's used/window).
		OnUsage:    func(u agent.Usage) { note.usage(u.TotalTokens(), contextWindow) },
		OnToolCall: note.toolCall,
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
	note.configOptions(a.configOptions(context.Background(), sess))
	return SetSessionModeResult{}, nil
}

func (a *Agent) handleSetConfigOption(ctx context.Context, params json.RawMessage) (any, error) {
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
		// A model value may be provider-qualified ("provider\x00model") so a
		// model from another provider switches providers too, matching the TUI
		// picker. An unqualified value applies to the active provider.
		provider, model := splitModelValue(value)
		if provider != "" {
			sess.setProvider(provider)
		}
		sess.setModel(model)
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
	case configIDSelfCorrect:
		if !validSelfCorrect(value) {
			return nil, RPCError(codeInvalidParams, "unknown self-correct depth: "+value)
		}
		sess.setSelfCorrect(value)
	case configIDProfile:
		if !validProfile(value) {
			return nil, RPCError(codeInvalidParams, "unknown execution profile: "+value)
		}
		sess.setExecProfile(value)
	default:
		return nil, RPCError(codeInvalidParams, "unknown config option: "+p.ConfigID)
	}
	options := a.configOptions(ctx, sess)
	(&notifier{conn: a.conn, sessionID: sess.id}).configOptions(options)
	return SetSessionConfigOptionResult{ConfigOptions: options}, nil
}

// refreshModels clears the session's per-provider model cache, re-runs discovery
// across every provider, and re-emits config_option_update. It is the shared
// implementation behind _kajicode/refresh_models and the /refresh-models slash
// command, so the vendor method and the client-reachable command never diverge.
// Discovery is bounded by ctx so a slow/hung provider cannot wedge the call.
func (a *Agent) refreshModels(ctx context.Context, sess *acpSession) []SessionConfigOption {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sess.mu.Lock()
	sess.modelCache = nil
	sess.mu.Unlock()
	options := a.configOptions(ctx, sess)
	(&notifier{conn: a.conn, sessionID: sess.id}).configOptions(options)
	return options
}

// handleKajiCodeRefreshModels re-runs provider model discovery for a session and
// re-emits config_option_update, mirroring the TUI's "refresh models" affordance.
func (a *Agent) handleKajiCodeRefreshModels(ctx context.Context, params json.RawMessage) (any, error) {
	var p KajiCodeRefreshModelsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, RPCError(codeInvalidParams, "invalid _kajicode/refresh_models params")
	}
	sess := a.session(p.SessionID)
	if sess == nil {
		return nil, RPCError(codeInvalidParams, "unknown session: "+p.SessionID)
	}
	return KajiCodeRefreshModelsResult{ConfigOptions: a.refreshModels(ctx, sess)}, nil
}

// runRefreshModels is the /refresh-models slash command: the same refresh the
// vendor method performs, plus a one-line summary so an editor that cannot call
// vendor methods still has a visible, reachable refresh affordance.
func (a *Agent) runRefreshModels(ctx context.Context, sess *acpSession) string {
	options := a.refreshModels(ctx, sess)
	providers, models := countModelOptions(options)
	if providers == 0 {
		return "Refreshed models: no providers available."
	}
	return fmt.Sprintf("Refreshed models: %d provider(s), %d model(s).", providers, models)
}

// listSkills renders the session's skills: the same merged set (global + project
// + plugin) the model discovers as <available_skills>, so the command and the
// model's catalog never disagree. It reuses Deps.BuildWorkspace (the same build a
// turn uses), so a project skill in the workspace shows up here too.
func (a *Agent) listSkills(sess *acpSession) string {
	if a.deps.BuildWorkspace == nil {
		return "Skills are not available in this session."
	}
	workspace, err := a.deps.BuildWorkspace(sess.cwd, sess.resolved)
	if err != nil {
		return "Skills\nFailed to load skills: " + err.Error()
	}
	if len(workspace.Skills) == 0 {
		return "Skills\nNo skills installed. Add one under ~/.local/share/kajicode/skills, ~/.agents/skills, or a project .skills/ directory."
	}
	skills := append([]agent.SkillInfo(nil), workspace.Skills...)
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (%d):\n", len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		b.WriteString("  " + name)
		if description := strings.TrimSpace(skill.Description); description != "" {
			b.WriteString(" - " + description)
		}
		// Match the model catalog's markers exactly (see agent.system_prompt.go)
		// so the listing and <available_skills> agree on load restrictions.
		switch skill.Permission {
		case "deny":
			b.WriteString(" [deny]")
		case "prompt":
			b.WriteString(" [prompt]")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// countModelOptions counts the providers and models in a config-option set's
// model selector (grouped options), skipping the leading non-model options.
func countModelOptions(options []SessionConfigOption) (providers, models int) {
	for _, option := range options {
		if option.ID != configIDModel {
			continue
		}
		groups, ok := option.Options.([]ConfigOptionGroup)
		if !ok {
			return 0, 0
		}
		for _, group := range groups {
			providers++
			models += len(group.Options)
		}
	}
	return providers, models
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
	provider := strings.TrimSpace(p.Provider)
	model := strings.TrimSpace(p.Model)
	// Accept either a provider-qualified model value ("provider\x00model") or an
	// explicit provider field, so both call styles switch providers.
	if qProvider, qModel := splitModelValue(model); qProvider != "" {
		provider, model = qProvider, qModel
	}
	if provider != "" {
		sess.setProvider(provider)
	}
	sess.setModel(model)
	(&notifier{conn: a.conn, sessionID: sess.id}).configOptions(a.configOptions(context.Background(), sess))
	return KajiCodeSetModelResult{Provider: provider, Model: model}, nil
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

// modeState is the legacy session-modes view. It lists the same modes the
// config-option path accepts, so a client that only understands modes can reach
// the same profiles.
func (a *Agent) modeState(s *acpSession) *SessionModeState {
	return &SessionModeState{
		CurrentModeID: string(s.currentMode()),
		AvailableModes: []SessionMode{
			{ID: string(agent.PermissionModeAuto), Name: "Auto", Description: "Run safe tools automatically; ask before risky ones."},
			{ID: string(agent.PermissionModeAskAll), Name: "Ask", Description: "Ask before every tool that changes state."},
			{ID: string(agent.PermissionModeReadOnly), Name: "Read only", Description: "Allow reads; ask for writes, shell, and network."},
			{ID: string(agent.PermissionModeReadWrite), Name: "Read + write", Description: "Allow reads and file writes; ask for shell and network."},
			{ID: string(agent.PermissionModeBypassAll), Name: "Bypass all", Description: "Dangerous: allow every tool without prompting and disable the sandbox (unrestricted host access)."},
		},
	}
}

// configOptions returns the full set of session config selectors, in priority
// order, with their current values.
func (a *Agent) configOptions(ctx context.Context, sess *acpSession) []SessionConfigOption {
	groups := a.modelGroups(ctx, sess)
	return []SessionConfigOption{
		selectOption(configIDModel, "Model", "Active model for this session. Models from every configured provider are grouped by provider; choosing one switches providers.", configCategoryModel, a.modelCurrentValue(sess, groups), groups),
		selectOption(configIDMode, "Permissions", "How tool calls are authorized.", configCategoryMode, string(sess.currentMode()), permissionModeValues()),
		selectOption(configIDEffort, "Reasoning effort", "Reasoning effort for supported models.", configCategoryThought, effortValue(sess.effort()), effortValues()),
		selectOption(configIDTurns, "Turn budget", "Maximum tool turns per prompt.", configCategoryKajicode, strconv.Itoa(a.turnBudget(sess)), turnValues(a.turnBudget(sess))),
		selectOption(configIDStyle, "Response style", "Reply style directive.", configCategoryKajicode, styleValue(sess.responseStyle()), styleValues()),
		selectOption(configIDSelfCorrect, "Self-correction", "Post-edit verify-and-correct depth.", configCategoryKajicode, selfCorrectValue(sess.selfCorrect()), selfCorrectValues()),
		selectOption(configIDProfile, "Execution profile", "Loop posture: turn budget, effort, self-correction.", configCategoryKajicode, profileValue(sess.execProfile()), profileValues()),
	}
}

// modelValueSep separates a provider name from a model id in a model option
// value. It is a NUL control byte so it can never collide with a real provider
// name or model id (neither contains a control character).
const modelValueSep = "\x00"

// qualifyModel encodes a provider+model pair as one select value.
func qualifyModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return model
	}
	return provider + modelValueSep + model
}

// splitModelValue splits a qualified model value. A value with no separator is
// an unqualified model id, which belongs to the session's active provider.
func splitModelValue(value string) (provider, model string) {
	if i := strings.Index(value, modelValueSep); i >= 0 {
		return value[:i], value[i+len(modelValueSep):]
	}
	return "", value
}

// modelSelectorProviders returns the providers whose models the selector lists:
// every usable saved provider (so the selector matches the TUI picker), falling
// back to the session's resolved active provider when no provider list is
// available.
func (a *Agent) modelSelectorProviders(sess *acpSession) []config.ProviderProfile {
	if a.deps.Providers != nil {
		if providers := a.deps.Providers(); len(providers) > 0 {
			return providers
		}
	}
	if config.HasProviderProfile(sess.resolved.Provider) {
		return []config.ProviderProfile{sess.resolved.Provider}
	}
	return nil
}

// activeProviderName is the session's provider override, or the resolved active
// provider when none was chosen.
func (a *Agent) activeProviderName(sess *acpSession) string {
	if provider := sess.currentProvider(); provider != "" {
		return provider
	}
	return sess.resolved.Provider.Name
}

// modelGroups lists every provider's models as one select group per provider,
// so one selector offers the same cross-provider set the TUI picker does. Each
// option's value qualifies the model with its provider, so selecting a model
// from another provider can switch providers. The active provider always
// surfaces its current/default model so the select contains its current value.
func (a *Agent) modelGroups(ctx context.Context, sess *acpSession) []ConfigOptionGroup {
	// Bound the whole fan-out so a session with many slow providers cannot
	// wedge session/new or a config-option emission. A caller with an earlier
	// deadline (e.g. refresh_models) keeps it.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	activeProvider := a.activeProviderName(sess)
	activeModel := sess.currentModel()
	if activeModel == "" {
		activeModel = sess.resolved.Provider.Model
	}

	providers := a.modelSelectorProviders(sess)
	groups := make([]ConfigOptionGroup, 0, len(providers)+1)
	seenProvider := map[string]bool{}
	activeAdded := false
	for _, profile := range providers {
		name := strings.TrimSpace(profile.Name)
		if name == "" || seenProvider[name] {
			continue
		}
		seenProvider[name] = true
		isActive := strings.EqualFold(name, activeProvider)
		options := a.providerModelOptions(ctx, sess, profile, isActive, activeModel)
		if len(options) == 0 {
			continue
		}
		if isActive {
			activeAdded = true
		}
		groups = append(groups, ConfigOptionGroup{Group: name, Name: name, Options: options})
	}
	// The active provider must always be present so the current value is a valid
	// option — e.g. when it is not in the saved list, or discovery returned
	// nothing for it.
	if !activeAdded && activeModel != "" && activeProvider != "" {
		groups = append([]ConfigOptionGroup{{
			Group:   activeProvider,
			Name:    activeProvider,
			Options: []SessionConfigOptionValue{{Value: qualifyModel(activeProvider, activeModel), Name: activeModel}},
		}}, groups...)
	}
	return groups
}

// providerModelOptions builds one provider's grouped-model option list.
func (a *Agent) providerModelOptions(ctx context.Context, sess *acpSession, profile config.ProviderProfile, isActive bool, activeModel string) []SessionConfigOptionValue {
	name := strings.TrimSpace(profile.Name)
	options := make([]SessionConfigOptionValue, 0, 8)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		options = append(options, SessionConfigOptionValue{Value: qualifyModel(name, id), Name: id})
	}
	if isActive {
		add(activeModel)
		add(profile.Model)
		add(sess.resolved.DefaultModel)
	}
	for _, discovered := range a.providerModelValues(ctx, sess, profile) {
		add(discovered.Value)
	}
	return options
}

// modelCurrentValue is the model option's currentValue: the active provider +
// model. It always returns a value that appears in groups (the spec requires
// the current value to be one of the options), falling back to the active
// provider's first option or the first group's first option.
func (a *Agent) modelCurrentValue(sess *acpSession, groups []ConfigOptionGroup) string {
	activeProvider := a.activeProviderName(sess)
	model := sess.currentModel()
	if model == "" {
		model = sess.resolved.Provider.Model
	}
	candidate := qualifyModel(activeProvider, model)
	if candidate != "" && groupsContainValue(groups, candidate) {
		return candidate
	}
	for _, group := range groups {
		if strings.EqualFold(group.Group, activeProvider) && len(group.Options) > 0 {
			return group.Options[0].Value
		}
	}
	if len(groups) > 0 && len(groups[0].Options) > 0 {
		return groups[0].Options[0].Value
	}
	return candidate
}

// groupsContainValue reports whether any group option equals value.
func groupsContainValue(groups []ConfigOptionGroup, value string) bool {
	for _, group := range groups {
		for _, option := range group.Options {
			if option.Value == value {
				return true
			}
		}
	}
	return false
}

// providerModelValues returns one provider's discovered model list, probing the
// provider at most once per session (or after _kajicode/refresh_models). The
// probe is network I/O, so the per-provider cache keeps config-option emissions
// cheap and mirrors the TUI picker's own per-provider cache.
func (a *Agent) providerModelValues(ctx context.Context, sess *acpSession, profile config.ProviderProfile) []SessionConfigOptionValue {
	key := strings.TrimSpace(profile.Name)
	sess.mu.Lock()
	if cached, ok := sess.modelCache[key]; ok {
		sess.mu.Unlock()
		return cached
	}
	sess.mu.Unlock()
	if a.deps.DiscoverModels == nil {
		return nil
	}
	// Bound each provider probe so one slow/unreachable provider cannot wedge
	// session/new or a config-option emission (the TUI bounds each at 8s too).
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	values, err := a.deps.DiscoverModels(probeCtx, profile)
	if err != nil {
		values = nil
	}
	sess.mu.Lock()
	if sess.modelCache == nil {
		sess.modelCache = map[string][]SessionConfigOptionValue{}
	}
	sess.modelCache[key] = values
	sess.mu.Unlock()
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
	a.advertiseCommands(note, sess)
	note.configOptions(a.configOptions(context.Background(), sess))
}

// advertiseCommands emits available_commands_update for a session's workspace.
func (a *Agent) advertiseCommands(note *notifier, sess *acpSession) {
	if a.deps.Commands == nil {
		return
	}
	if commands := a.deps.Commands(sess.cwd); len(commands) > 0 {
		note.availableCommands(commands)
	}
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
		// Inherit only a profile an editor holds safely. `unsafe` and `bypass-all`
		// disable the sandbox and grant every tool, so a saved default must not
		// leak into an ACP session implicitly — a client can still opt in
		// explicitly via session/set_config_option. Fall back to Auto otherwise.
		if normalized := agent.NormalizePermissionMode(agent.PermissionMode(p)); validPermissionMode(string(normalized)) && normalized != agent.PermissionModeBypassAll {
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

func (s *acpSession) setProvider(provider string) {
	s.mu.Lock()
	s.provider = provider
	s.mu.Unlock()
}

// currentProvider returns the session's provider override, or "" when the
// session uses the config's resolved active provider.
func (s *acpSession) currentProvider() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provider
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

func (s *acpSession) setSelfCorrect(depth string) {
	s.mu.Lock()
	s.selfCorrectDepth = depth
	s.mu.Unlock()
}

func (s *acpSession) selfCorrect() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selfCorrectDepth
}

func (s *acpSession) setExecProfile(name string) {
	s.mu.Lock()
	s.execProfileName = name
	s.mu.Unlock()
}

func (s *acpSession) execProfile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.execProfileName
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

// buildSelfCorrect returns the self-corrector for a session's chosen depth plus
// a cleanup func that tears down any language-server session it spawned. It
// stays disabled (nil, no-op cleanup) for the default "off"/"" depth so the loop
// is unchanged, mirroring newExecSelfCorrector in the exec surface.
func (a *Agent) buildSelfCorrect(sess *acpSession, armProfile bool) (*agent.SelfCorrector, func(context.Context, string) string, func()) {
	depth := sess.selfCorrect()
	manager := lsp.NewManager(sess.cwd)
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	}
	fileDiagnostics := agent.NewFileDiagnostics(manager, sess.cwd)
	if depth == "" || depth == "off" {
		if !armProfile {
			return nil, fileDiagnostics, cleanup
		}
		// A profile (e.g. thorough) arms the full self-correction loop.
		depth = "full"
	}
	includeTests := depth == "tests" || depth == "full"
	includeLSP := depth == "on" || depth == "full"
	corrector := agent.NewSelfCorrector(
		sess.cwd,
		agent.NewLSPDiagnosticsChecker(manager),
		agent.NewProjectVerifier(sess.cwd),
		agent.SelfCorrectConfig{
			Enabled:      true,
			IncludeTests: includeTests,
			IncludeLSP:   includeLSP,
			Autonomy:     "medium",
		},
	)
	return corrector, fileDiagnostics, cleanup
}
