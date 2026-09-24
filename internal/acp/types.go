package acp

import "encoding/json"

// ProtocolVersion is the ACP protocol version KAJICODE speaks. Wire compatibility is
// negotiated during initialize; v1 is the current stable version.
const ProtocolVersion = 1

// Method names exactly as they appear on the wire (public ACP spec).
const (
	MethodInitialize             = "initialize"
	MethodAuthenticate           = "authenticate"
	MethodLogout                 = "logout"
	MethodSessionNew             = "session/new"
	MethodSessionLoad            = "session/load"
	MethodSessionResume          = "session/resume"
	MethodSessionFork            = "session/fork"
	MethodSessionList            = "session/list"
	MethodSessionClose           = "session/close"
	MethodSessionDelete          = "session/delete"
	MethodSessionPrompt          = "session/prompt"
	MethodSessionCancel          = "session/cancel" // notification
	MethodSessionUpdate          = "session/update" // notification (agent -> client)
	MethodSessionSetMode         = "session/set_mode"
	MethodSessionSetConfigOption = "session/set_config_option"
	MethodSessionRequestPerm     = "session/request_permission" // agent -> client
	MethodElicitationCreate      = "elicitation/create"         // agent -> client

	// v1 unstable: editor->agent document sync + next edit suggestions.
	MethodDocumentDidOpen   = "document/didOpen"
	MethodDocumentDidChange = "document/didChange"
	MethodDocumentDidClose  = "document/didClose"
	MethodDocumentDidSave   = "document/didSave"
	MethodDocumentDidFocus  = "document/didFocus"
	MethodNesStart          = "nes/start"
	MethodNesSuggest        = "nes/suggest"
	MethodNesAccept         = "nes/accept"
	MethodNesReject         = "nes/reject"
	MethodNesClose          = "nes/close"

	// v1 unstable: provider configuration.
	MethodProvidersList    = "providers/list"
	MethodProvidersSet     = "providers/set"
	MethodProvidersDisable = "providers/disable"

	// Vendor-prefixed KAJICODE extensions (clients that don't support them ignore the
	// method and degrade cleanly, per the spec's _-prefixed convention).
	MethodKajiCodeSetModel      = "_kajicode/set_model"
	MethodKajiCodeRefreshModels = "_kajicode/refresh_models"
)

// SessionUpdate discriminator values (the "sessionUpdate" field).
const (
	UpdateAgentMessageChunk = "agent_message_chunk"
	UpdateAgentThoughtChunk = "agent_thought_chunk"
	UpdateUserMessageChunk  = "user_message_chunk"
	UpdateToolCall          = "tool_call"
	UpdateToolCallUpdate    = "tool_call_update"
	UpdatePlan              = "plan"
	UpdateAvailableCommands = "available_commands_update"
	UpdateCurrentMode       = "current_mode_update"
	UpdateConfigOption      = "config_option_update"
	UpdateUsage             = "usage_update"
	UpdateSessionInfo       = "session_info_update"
)

// ---- initialize ----

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type ClientCapabilities struct {
	FS          FileSystemCapabilities     `json:"fs"`
	Terminal    bool                       `json:"terminal"`
	Elicitation *ElicitationCapabilities   `json:"elicitation,omitempty"`
	Auth        ClientAuthCapabilities     `json:"auth"`
	Session     *ClientSessionCapabilities `json:"session,omitempty"`
}

// ClientAuthCapabilities advertises which auth method types the client can run.
// A client sets Terminal true only when it can launch the agent program in an
// interactive terminal; the agent may then advertise terminal AuthMethods.
type ClientAuthCapabilities struct {
	Terminal bool `json:"terminal"`
}

// ClientSessionCapabilities advertises session-related client support.
type ClientSessionCapabilities struct {
	ConfigOptions *ClientConfigOptionsCapabilities `json:"configOptions,omitempty"`
}

// ClientConfigOptionsCapabilities advertises config-option extensions the client
// renders. Boolean means it can show `type:"boolean"` config options.
type ClientConfigOptionsCapabilities struct {
	Boolean *struct{} `json:"boolean,omitempty"`
}

// ElicitationCapabilities is the client's opt-in for structured user input. A
// present Form object means the client renders the requested JSON-schema form.
type ElicitationCapabilities struct {
	Form *struct{} `json:"form,omitempty"`
	URL  *struct{} `json:"url,omitempty"`
}

// ---- elicitation (agent -> client) ----

type CreateElicitationParams struct {
	Mode            string          `json:"mode"` // "form"
	Message         string          `json:"message"`
	SessionID       string          `json:"sessionId,omitempty"`
	ToolCallID      string          `json:"toolCallId,omitempty"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
}

// CreateElicitationResult is a tagged union on "action": accept carries the
// submitted content; decline/cancel carry nothing.
type CreateElicitationResult struct {
	Action  string                     `json:"action"`
	Content map[string]json.RawMessage `json:"content,omitempty"`
}

type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// McpCapabilities advertises which MCP transports the agent can be asked to
// connect to via a session's `mcpServers`. KajiCode owns its MCP configuration,
// so both transports are advertised as unsupported.
type McpCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// SessionCapabilities advertises the optional session lifecycle methods. A
// present, non-null object means the method is supported (per the spec); an
// absent/nil field means it is not.
type SessionCapabilities struct {
	List                  *struct{} `json:"list,omitempty"`
	Resume                *struct{} `json:"resume,omitempty"`
	Close                 *struct{} `json:"close,omitempty"`
	Delete                *struct{} `json:"delete,omitempty"`
	Fork                  *struct{} `json:"fork,omitempty"`
	AdditionalDirectories *struct{} `json:"additionalDirectories,omitempty"`
}

// AgentAuthCapabilities advertises auth-related methods the agent supports.
// Logout, when non-nil (present `{}`), means the agent implements `logout`.
type AgentAuthCapabilities struct {
	Logout *struct{} `json:"logout,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                  `json:"loadSession"`
	PromptCapabilities  PromptCapabilities    `json:"promptCapabilities"`
	McpCapabilities     McpCapabilities       `json:"mcpCapabilities"`
	SessionCapabilities SessionCapabilities   `json:"sessionCapabilities"`
	Auth                AgentAuthCapabilities `json:"auth"`
	// v1 unstable additions.
	Providers        *struct{}        `json:"providers,omitempty"`
	Nes              *NesCapabilities `json:"nes,omitempty"`
	PositionEncoding string           `json:"positionEncoding,omitempty"`
}

// NesCapabilities advertises that KajiCode wants document events for next-edit
// suggestions. Only `events.document` is set (with full, utf-8 sync).
type NesCapabilities struct {
	Events *NesEventCapabilities `json:"events,omitempty"`
}

type NesEventCapabilities struct {
	Document *NesDocumentEventCapabilities `json:"document,omitempty"`
}

type NesDocumentEventCapabilities struct {
	DidOpen   *struct{}                 `json:"didOpen,omitempty"`
	DidChange *NesDidChangeCapabilities `json:"didChange,omitempty"`
	DidClose  *struct{}                 `json:"didClose,omitempty"`
	DidSave   *struct{}                 `json:"didSave,omitempty"`
	DidFocus  *struct{}                 `json:"didFocus,omitempty"`
}

// NesDidChangeCapabilities requires the sync kind. KajiCode advertises "full":
// every didChange carries the whole document, so it needs no range arithmetic.
type NesDidChangeCapabilities struct {
	SyncKind string `json:"syncKind"`
}

// AuthMethod is either an agent-type method (KajiCode handles the login via
// `authenticate`) or a terminal-type method (the client launches the agent
// program in a terminal with Args/Env). Type is omitted for the default agent
// type, matching the spec.
type AuthMethod struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"` // "" (agent) or "terminal"
	Args        []string `json:"args,omitempty"` // terminal only
	Env         []EnvVar `json:"env,omitempty"`  // terminal only
}

// EnvVar is one environment variable for a terminal auth method.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ---- authenticate / logout ----

type AuthenticateParams struct {
	MethodID string `json:"methodId"`
}

type AuthenticateResult struct{}

type LogoutParams struct{}

type LogoutResult struct{}

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

// ---- content blocks ----

// ContentBlock is the polymorphic content type. KAJICODE emits "text" (and "image"
// on tool content); it parses "text", "image", and "resource"/"resource_link"
// from inbound prompts. A single struct with omitempty fields covers both
// directions since the field names do not collide across the variants KAJICODE uses.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`     // image/audio: base64
	MimeType string          `json:"mimeType,omitempty"` // image/audio
	URI      string          `json:"uri,omitempty"`      // resource_link
	Name     string          `json:"name,omitempty"`     // resource_link
	Resource json.RawMessage `json:"resource,omitempty"` // embedded resource
}

func TextBlock(text string) ContentBlock { return ContentBlock{Type: "text", Text: text} }

// ---- sessions ----

// McpServer mirrors the editor-provided MCP server entry. KAJICODE owns its own MCP
// configuration (BYOK), so these are accepted for spec compliance; KAJICODE's
// configured servers remain authoritative.
type McpServer struct {
	Name    string          `json:"name"`
	Command string          `json:"command,omitempty"`
	Args    []string        `json:"args,omitempty"`
	Env     json.RawMessage `json:"env,omitempty"`
	URL     string          `json:"url,omitempty"`
}

type NewSessionParams struct {
	Cwd                   string      `json:"cwd"`
	McpServers            []McpServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

type NewSessionResult struct {
	SessionID     string                `json:"sessionId"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

type LoadSessionParams struct {
	SessionID             string      `json:"sessionId"`
	Cwd                   string      `json:"cwd"`
	McpServers            []McpServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

type LoadSessionResult struct {
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

// ---- prompt turn ----

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// StopReason values (why a prompt turn ended).
const (
	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
	StopCancelled = "cancelled"
)

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

// ---- session/update notification ----

type SessionNotification struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// AgentMessageChunk / AgentThoughtChunk / UserMessageChunk all carry a single
// ContentBlock under "content"; the variant is set via SessionUpdate.
type ContentChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

// ToolKind classifies a tool call for client rendering.
const (
	ToolKindRead    = "read"
	ToolKindEdit    = "edit"
	ToolKindDelete  = "delete"
	ToolKindMove    = "move"
	ToolKindSearch  = "search"
	ToolKindExecute = "execute"
	ToolKindThink   = "think"
	ToolKindFetch   = "fetch"
	ToolKindOther   = "other"
)

// ToolCallStatus values.
const (
	ToolStatusPending    = "pending"
	ToolStatusInProgress = "in_progress"
	ToolStatusCompleted  = "completed"
	ToolStatusFailed     = "failed"
)

// ToolCallUpdate is used for both the initial "tool_call" and subsequent
// "tool_call_update" notifications (distinguished by SessionUpdate). It also
// appears inside session/request_permission.
type ToolCallUpdate struct {
	SessionUpdate string             `json:"sessionUpdate,omitempty"`
	ToolCallID    string             `json:"toolCallId"`
	Title         string             `json:"title,omitempty"`
	Kind          string             `json:"kind,omitempty"`
	Status        string             `json:"status,omitempty"`
	RawInput      json.RawMessage    `json:"rawInput,omitempty"`
	Content       []ToolCallContent  `json:"content,omitempty"`
	Locations     []ToolCallLocation `json:"locations,omitempty"`
}

// ToolCallContent is a tool call's rendered output. KAJICODE emits "content" (a
// text/image block) and "diff" (a file change); "terminal" is part of the spec
// but unused because KAJICODE executes locally.
type ToolCallContent struct {
	Type string `json:"type"`
	// type == "content"
	Content *ContentBlock `json:"content,omitempty"`
	// type == "diff"
	Path    string `json:"path,omitempty"`
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText,omitempty"`
}

func ToolContent(block ContentBlock) ToolCallContent {
	return ToolCallContent{Type: "content", Content: &block}
}

type ToolCallLocation struct {
	Path string `json:"path"`
}

// ---- plan ----

const (
	PlanStatusPending    = "pending"
	PlanStatusInProgress = "in_progress"
	PlanStatusCompleted  = "completed"

	PlanPriorityHigh   = "high"
	PlanPriorityMedium = "medium"
	PlanPriorityLow    = "low"
)

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
}

type CurrentModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	CurrentModeID string `json:"currentModeId"`
}

type SessionInfoUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Title         string `json:"title,omitempty"`
}

type ConfigOptionUpdate struct {
	SessionUpdate string                `json:"sessionUpdate"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// AvailableCommandInput is the optional input hint for a slash command.
type AvailableCommandInput struct {
	Hint string `json:"hint"`
}

type AvailableCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
}

type AvailableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"`
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

// UsageUpdate reports token accounting for the session. KajiCode maps the
// provider's prompt/completion token counts onto used/size; size is the model's
// context window when known, else 0.
type UsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Used          int    `json:"used"`
	Size          int    `json:"size"`
}

// ---- permissions ----

const (
	PermAllowOnce    = "allow_once"
	PermAllowAlways  = "allow_always"
	PermRejectOnce   = "reject_once"
	PermRejectAlways = "reject_always"
)

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// RequestPermissionOutcome is a tagged union: {"outcome":"cancelled"} or
// {"outcome":"selected","optionId":"..."}.
type RequestPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

const (
	OutcomeSelected  = "selected"
	OutcomeCancelled = "cancelled"
)

type RequestPermissionResult struct {
	Outcome RequestPermissionOutcome `json:"outcome"`
}

// ---- session modes ----

type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

type SetSessionModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetSessionModeResult struct{}

// ---- session config options (model selection) ----

// SessionConfigOption is a discriminated union on "type": a "select" carries
// currentValue (string) + options; a "boolean" carries a bool currentValue. A
// single struct with omitempty covers both directions KajiCode uses.
type SessionConfigOption struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	Description  string                     `json:"description,omitempty"`
	Category     string                     `json:"category,omitempty"`
	Type         string                     `json:"type"`
	CurrentValue any                        `json:"currentValue"`
	Options      []SessionConfigOptionValue `json:"options,omitempty"`
}

type SessionConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Config option ids KajiCode exposes. Model is the spec-standard selector;
// the rest are vendor extensions (leading underscore category).
const (
	configIDModel         = "model"
	configIDMode          = "mode"
	configIDEffort        = "effort"
	configIDTurns         = "turns"
	configIDStyle         = "style"
	configIDSelfCorrect   = "selfcorrect"
	configIDProfile       = "profile"
	configCategoryModel   = "model"
	configCategoryMode    = "mode"
	configCategoryThought = "thought_level"
	// configCategoryKajicode marks KajiCode-specific selectors the spec does not
	// define; a leading underscore is the spec's convention for custom categories.
	configCategoryKajicode = "_kajicode"
)

// selectOption builds a "select" config option.
func selectOption(id, name, description, category, current string, options []SessionConfigOptionValue) SessionConfigOption {
	return SessionConfigOption{
		ID: id, Name: name, Description: description, Category: category,
		Type: "select", CurrentValue: current, Options: options,
	}
}

type SetSessionConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type SetSessionConfigOptionResult struct {
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// ---- vendor: _kajicode/set_model ----

type KajiCodeSetModelParams struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model"`
}

type KajiCodeSetModelResult struct {
	Model string `json:"model"`
}

// KajiCodeRefreshModelsParams asks the agent to re-run provider model discovery
// for a session and re-emit its config options.
type KajiCodeRefreshModelsParams struct {
	SessionID string `json:"sessionId"`
}

type KajiCodeRefreshModelsResult struct {
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// ---- session lifecycle: list / resume / close / delete ----

type ListSessionsParams struct {
	Cwd    string `json:"cwd,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// SessionInfo is one entry returned by session/list.
type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type ListSessionsResult struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

type ResumeSessionParams struct {
	SessionID             string      `json:"sessionId"`
	Cwd                   string      `json:"cwd"`
	McpServers            []McpServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

type ResumeSessionResult struct {
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

// ForkSessionParams mirrors session/new but branches from an existing session.
type ForkSessionParams struct {
	SessionID             string      `json:"sessionId"`
	Cwd                   string      `json:"cwd"`
	McpServers            []McpServer `json:"mcpServers"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
}

type ForkSessionResult struct {
	SessionID     string                `json:"sessionId"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

type CloseSessionParams struct {
	SessionID string `json:"sessionId"`
}

type CloseSessionResult struct{}

type DeleteSessionParams struct {
	SessionID string `json:"sessionId"`
}

type DeleteSessionResult struct{}

// ---- v1 unstable: editor->agent document sync (gated by nes.events.document) ----

// Position is a zero-based line/character position (LSP-shaped). With the
// utf-8 position encoding KajiCode advertises, character is a byte offset.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidOpenDocumentParams struct {
	SessionID  string `json:"sessionId"`
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidChangeDocumentParams struct {
	SessionID      string                           `json:"sessionId"`
	URI            string                           `json:"uri"`
	Version        int                              `json:"version"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidCloseDocumentParams struct {
	SessionID string `json:"sessionId"`
	URI       string `json:"uri"`
}

type DidSaveDocumentParams struct {
	SessionID string `json:"sessionId"`
	URI       string `json:"uri"`
}

type DidFocusDocumentParams struct {
	SessionID    string   `json:"sessionId"`
	URI          string   `json:"uri"`
	Version      int      `json:"version"`
	Position     Position `json:"position"`
	VisibleRange Range    `json:"visibleRange"`
}

// ---- v1 unstable: next edit suggestions ----

type StartNesParams struct {
	WorkspaceURI     string   `json:"workspaceUri,omitempty"`
	WorkspaceFolders []string `json:"workspaceFolders,omitempty"`
	Repository       any      `json:"repository,omitempty"`
}

type StartNesResult struct{}

type SuggestNesParams struct {
	SessionID   string      `json:"sessionId"`
	URI         string      `json:"uri"`
	Version     int         `json:"version"`
	Position    Position    `json:"position"`
	Selection   *Range      `json:"selection,omitempty"`
	TriggerKind string      `json:"triggerKind"`
	Context     *NesContext `json:"context,omitempty"`
}

// NesContext mirrors the client-supplied context. KajiCode does not request any
// context capability, so these are accepted for shape compliance and unused.
type NesContext struct {
	RecentFiles     []string `json:"recentFiles,omitempty"`
	RelatedSnippets []string `json:"relatedSnippets,omitempty"`
	EditHistory     []string `json:"editHistory,omitempty"`
	UserActions     []string `json:"userActions,omitempty"`
	OpenFiles       []string `json:"openFiles,omitempty"`
	Diagnostics     []string `json:"diagnostics,omitempty"`
}

type NesTextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type NesEditSuggestion struct {
	ID             string        `json:"id"`
	URI            string        `json:"uri"`
	Edits          []NesTextEdit `json:"edits"`
	CursorPosition *Position     `json:"cursorPosition,omitempty"`
}

// NesSuggestion is a tagged union on the presence of the fields the spec's
// variants use; KajiCode only emits NesEditSuggestion.
type NesSuggestion = NesEditSuggestion

type SuggestNesResult struct {
	Suggestions []NesSuggestion `json:"suggestions"`
}

type AcceptNesParams struct {
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
}

type RejectNesParams struct {
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
	Reason    string `json:"reason,omitempty"`
}

type CloseNesParams struct {
	SessionID string `json:"sessionId"`
}

// ---- v1 unstable: provider configuration ----

type ProviderInfo struct {
	ProviderID string               `json:"providerId"`
	Supported  []string             `json:"supported"`
	Required   bool                 `json:"required"`
	Current    *ProviderCurrentInfo `json:"current,omitempty"`
}

type ProviderCurrentInfo struct {
	APIType string `json:"apiType"`
	BaseURL string `json:"baseUrl"`
}

type ListProvidersResult struct {
	Providers []ProviderInfo `json:"providers"`
}

type SetProviderParams struct {
	ProviderID string            `json:"providerId"`
	APIType    string            `json:"apiType"`
	BaseURL    string            `json:"baseUrl"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type SetProviderResult struct{}

type DisableProviderParams struct {
	ProviderID string `json:"providerId"`
}

type DisableProviderResult struct{}

type CloseNesResult struct{}
