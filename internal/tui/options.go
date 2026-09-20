package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/imageinput"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/mcp"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
	"github.com/dishant0406/KajiCode/internal/providerhealth"
	"github.com/dishant0406/KajiCode/internal/providermodeldiscovery"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/skills"
	"github.com/dishant0406/KajiCode/internal/tools"
	"github.com/dishant0406/KajiCode/internal/usage"
)

// Options configures the reusable KajiCode terminal UI shell.
type Options struct {
	Cwd                         string
	Version                     string // CLI build version, shown on the home screen; empty hides it
	UserConfigPath              string
	DoctorUserConfigPath        string
	ProjectConfigPath           string
	ProviderName                string
	ModelName                   string
	ProviderProfile             config.ProviderProfile
	SavedProviders              []config.ProviderProfile // all configured providers, for the /model multi-provider list
	ImageLimits                 imageinput.Limits        // images.*: provider-safe envelope for attached images
	FavoriteModels              []string
	RecentModels                []config.RecentModelEntry
	RecapsEnabled               bool
	Provider                    kajicoderuntime.Provider
	NewProvider                 func(config.ProviderProfile) (kajicoderuntime.Provider, error)
	ProbeProviderHealth         func(context.Context, providerhealth.Options) providerhealth.Result
	DiscoverProviderModels      func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error)
	DiscoverOllamaContextWindow func(ctx context.Context, baseURL string, model string) (int, error)
	RuntimeMessageSink          func(tea.Msg)
	PrepareRunCompletionWarning func()
	RunCompletionWarning        func() string
	Registry                    *tools.Registry
	SessionStore                *sessions.Store
	SandboxStore                *sandbox.GrantStore
	MCPConfig                   config.MCPConfig
	MCPPermissionStore          *mcp.PermissionStore
	MCPTokenStore               *mcp.TokenStore
	MCPCommand                  func(context.Context, []string) MCPCommandResult
	SandboxSetupCommand         func(context.Context) SandboxSetupCommandResult
	UsageTracker                *usage.Tracker
	SessionCompactor            SessionCompactor
	PrService                   *PrService

	AgentOptions agent.Options
	// LoadSkills returns the installed skills (default skills dir merged with any
	// plugin skill roots), bodies included, for /skills and direct /<skill-name>
	// invocation. Called lazily per use so newly installed skills are picked up
	// without a restart. Nil means the session has no skills wiring (skills stay
	// model-pulled via the skill tool only).
	LoadSkills             func() []skills.Skill
	PermissionMode         agent.PermissionMode
	PermissionModeExplicit bool
	SavePermissionProfile  func(agent.PermissionMode) error
	ReasoningEffort        modelregistry.ReasoningEffort
	ResponseStyle          string
	// Theme is the operator's palette preference: "auto" (default), a built-in
	// ("dark"/"light"), or a registered color theme. Set from the --theme flag;
	// falls back to KAJICODE_THEME, then the persisted SavedTheme, then auto.
	Theme string
	// SavedTheme is the theme persisted in user config (Preferences.Theme). Applied
	// at startup below --theme and KAJICODE_THEME, so a /theme choice survives restart.
	SavedTheme string
	UserAgent  string

	// Notify configures completion / awaiting-input notifications.
	Notify config.NotifyConfig

	// KeyBindings configures remappable TUI keybindings. An empty/zero
	// KeyBindingsConfig means "use built-in defaults" for each action.
	KeyBindings config.KeyBindingsConfig

	// AltScreen tells the model it is running inside Bubble Tea's alternate
	// screen. Run sets this for the interactive app; tests can leave it false
	// to exercise the native scrollback renderer.
	AltScreen bool

	// Setup configures the first-run/setup takeover. It is shown before the
	// normal chat surface when Visible is true.
	Setup SetupOptions
}

type MCPCommandResult struct {
	Config   config.MCPConfig
	Output   string
	Error    string
	ExitCode int
}

type SandboxSetupCommandResult struct {
	Output   string
	Error    string
	ExitCode int
}

// SetupOptions configures the guided first-run provider setup takeover.
type SetupOptions struct {
	Visible    bool
	Required   bool
	ConfigPath string
	Providers  []SetupProviderOption
	Save       func(SetupSelection) (SetupResult, error)
}

// SetupProviderOption is one provider choice offered by the setup takeover.
type SetupProviderOption struct {
	ID           string
	Name         string
	DefaultModel string
	EnvVar       string
	RequiresAuth bool
	Local        bool
	Recommended  bool
}

// SetupSelection is the user's setup choice.
type SetupSelection struct {
	CatalogID string
	Name      string
	BaseURL   string
	Model     string
	APIKey    string
}

// SetupResult describes a completed setup write.
type SetupResult struct {
	ConfigPath string
	Provider   config.ProviderProfile
}
