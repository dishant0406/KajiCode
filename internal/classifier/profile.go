package classifier

import (
	"fmt"
	"os"
	"strings"

	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

// DefaultTimeoutMS bounds one classify round trip. The classifier sits in the
// agent's hot path (a compaction or tool-result decision), so a stalled endpoint
// must fail open quickly rather than stall a turn.
const DefaultTimeoutMS = 8000

// knownKind is the only wire adapter implemented today. It covers the hosted
// TypeSafe Jev router and any router that speaks the same {state, questions} →
// {answers} contract.
const knownKind = "jev"

// Profile is one registered classifier provider. It mirrors the shape of a
// config.ProviderProfile (endpoint, auth, key-in-env-or-store, custom headers) so
// a user configures a classifier the same way they configure a model provider.
type Profile struct {
	// Name is the profile id used by `classifier use` and stored as Config.Active.
	Name string `json:"name"`
	// Kind selects the wire adapter. Empty means "jev".
	Kind string `json:"kind,omitempty"`
	// Endpoint is the full request URL. BaseURL is an accepted alias.
	Endpoint string `json:"endpoint,omitempty"`
	BaseURL  string `json:"baseURL,omitempty"`
	// Model is sent as the request "model" when the router requires one.
	Model string `json:"model,omitempty"`
	// AuthHeader/AuthScheme name where the key goes. Empty ⇒
	// "Authorization"/"Bearer". AuthScheme "" or "raw"/"none" sends the key bare.
	AuthHeader string `json:"authHeader,omitempty"`
	AuthScheme string `json:"authScheme,omitempty"`
	// APIKeyEnv names an environment variable holding the key (preferred). APIKey
	// is an inline key and, like provider profiles, is expected to be moved into
	// the credential store (APIKeyStored) rather than kept in config.json.
	APIKeyEnv    string `json:"apiKeyEnv,omitempty"`
	APIKey       string `json:"apiKey,omitempty"`
	APIKeyStored bool   `json:"apiKeyStored,omitempty"`
	// Headers are arbitrary extra request headers (e.g. X-Router-Tier).
	Headers map[string]string `json:"headers,omitempty"`
	// TimeoutMS overrides DefaultTimeoutMS.
	TimeoutMS int `json:"timeoutMs,omitempty"`
}

// URL returns the effective endpoint (Endpoint, else BaseURL).
func (p Profile) URL() string {
	if strings.TrimSpace(p.Endpoint) != "" {
		return strings.TrimSpace(p.Endpoint)
	}
	return strings.TrimSpace(p.BaseURL)
}

// Adapter returns the effective kind (default jev).
func (p Profile) Adapter() string {
	if kind := strings.ToLower(strings.TrimSpace(p.Kind)); kind != "" {
		return kind
	}
	return knownKind
}

func (p Profile) timeoutMS() int {
	if p.TimeoutMS > 0 {
		return p.TimeoutMS
	}
	return DefaultTimeoutMS
}

// APIKeyResolver returns the stored API key for a profile name (from the
// credential store), or ok=false. Injected by the CLI so this package never
// depends on the config/credstore layer.
type APIKeyResolver func(name string) (string, bool)

// Config is the classifier capability's persisted configuration: a named-profile
// list plus the per-feature switches. Adding a profile turns nothing on; once
// Enabled is set, memory search uses the classifier, and each feature in
// Features still needs its own switch.
type Config struct {
	Enabled  bool      `json:"enabled,omitempty"`
	Active   string    `json:"active,omitempty"`
	Profiles []Profile `json:"profiles,omitempty"`
	Features Features  `json:"features,omitempty"`
}

// Features gates the compaction judge and the tool-result gate. Each is
// independently off until explicitly enabled. Memory search has no switch: it
// uses the classifier whenever the classifier is enabled.
type Features struct {
	Compaction CompactionFeature `json:"compaction,omitempty"`
	ToolResult ToolResultFeature `json:"toolResult,omitempty"`
}

// CompactionFeature controls the compaction keep/drop judge.
type CompactionFeature struct {
	Enabled bool `json:"enabled,omitempty"`
	// KeepResultThreshold is the noul probability at or above which a tool
	// result's body is kept verbatim. It is the top of the uncertain band.
	KeepResultThreshold float64 `json:"keepResultThreshold,omitempty"`
	// DropResultThreshold is the noul probability below which a result is
	// dropped. A probability in [DropResultThreshold, KeepResultThreshold) is
	// KEPT: the classifier is uncertain, and keeping is cheap and safe.
	//
	// The band exists because a single threshold is a cliff. Measured on real
	// sessions, keep=0.35 kept 51/400 stale bodies while keep=0.5 kept 2/400 —
	// the probabilities cluster in 0.2–0.5, so a tiny shift flipped a body
	// between verbatim and dropped. Dropping the whole uncertain band instead
	// keeps a superset of what the single threshold kept, so it can never lose
	// information or make the summarizer run more often.
	DropResultThreshold float64 `json:"dropResultThreshold,omitempty"`
}

const (
	// DefaultKeepResultThreshold is the compaction judge's keep threshold (the
	// top of the uncertain band). A result is only dropped when the classifier
	// is fairly confident it is no longer needed.
	DefaultKeepResultThreshold = 0.35
)

// EffectiveKeepResultThreshold returns the configured keep threshold, falling
// back to DefaultKeepResultThreshold when unset.
func (f CompactionFeature) EffectiveKeepResultThreshold() float64 {
	if f.KeepResultThreshold > 0 {
		return f.KeepResultThreshold
	}
	return DefaultKeepResultThreshold
}

// EffectiveDropResultThreshold returns the configured drop threshold. When unset
// it returns the keep threshold, which makes the uncertain band EMPTY — i.e. the
// band is off and the decision is the plain single threshold, exactly the shipped
// behavior.
//
// The band is opt-in because a retention-raising band is measurably NOT free on
// this workload. Measured over four real sessions (3,134 stale bodies, live
// jev-1.13): the classifier puts almost all mass in 0.2–0.4 (1,811 in 0.2–0.3,
// 1,245 in 0.3–0.4, only 12 below 0.2), so a drop floor of 0.2 keeps 3,122 of
// 3,134 bodies and reclaims 7,420 tokens — it barely filters. A judged history
// that keeps more stays OVER the compaction threshold more often, so the paid
// summarizer runs MORE, on a larger input, and the restored bodies are then
// discarded by that summarizer anyway. The band therefore only helps when a
// caller has accepted that reclaim cost for retention; the default does not.
//
// When set, it is clamped to the keep threshold so the band can never invert:
// a dropped set is always a subset of the single-threshold dropped set.
func (f CompactionFeature) EffectiveDropResultThreshold() float64 {
	keep := f.EffectiveKeepResultThreshold()
	drop := f.DropResultThreshold
	if drop <= 0 || drop > keep {
		return keep
	}
	return drop
}

// ToolResultFeature controls the live tool-result relevance gate. It judges a
// large, non-error tool result as it is produced and hides the blocks the
// classifier is confident are irrelevant, behind a recall stub. It is a
// consumer of the same classifier the compaction judge uses; enabling it is a
// separate explicit step from connecting the classifier.
type ToolResultFeature struct {
	Enabled bool `json:"enabled,omitempty"`
	// KeepThreshold is the noul probability at or above which a block is
	// confidently kept. It is the top of the uncertain band. <= 0 uses
	// DefaultGateKeepThreshold.
	KeepThreshold float64 `json:"keepThreshold,omitempty"`
	// DropThreshold is the noul probability below which a block is hidden. A
	// probability in [DropThreshold, KeepThreshold) is KEPT: the classifier is
	// uncertain, and keeping is cheap and safe. <= 0 uses
	// DefaultGateDropThreshold, which is calibrated on KajiCode's own workload
	// (see that constant), not borrowed from winnow's 0.1.
	DropThreshold float64 `json:"dropThreshold,omitempty"`
	// MinPruneRatio is the share of the body that must be hidden for the gate to
	// act at all. Below it the whole result is kept verbatim, so a gate that would
	// shave a few bytes never pays its stub overhead or risks the recall round
	// trip. <= 0 uses DefaultGateMinPruneRatio (0.2).
	MinPruneRatio float64 `json:"minPruneRatio,omitempty"`
	// MaxHiddenRatio is the ceiling on the share of a result the gate may hide.
	// At or above it the result is kept whole: a classifier that scores every
	// block low must not be able to black out an entire result, because the model
	// is then left with a stub, a spill path, and no way to make progress. <= 0
	// uses DefaultGateMaxHiddenRatio (0.5).
	MaxHiddenRatio float64 `json:"maxHiddenRatio,omitempty"`
	// MinBytes is the smallest result the gate considers. Below it a stub saves
	// nothing. <= 0 uses DefaultGateMinBytes (1500).
	MinBytes int `json:"minBytes,omitempty"`
	// ShadowMode, when true, judges and records the decision but never rewrites
	// the result. It is how the gate is measured on real traffic before it is
	// trusted to change what the model sees.
	ShadowMode bool `json:"shadowMode,omitempty"`
	// CoverageThreshold is the kept-text sufficiency trigger: a coverage
	// probability below it means the filtered result is not enough to proceed. It
	// is only read when Requery is enabled. <= 0 uses
	// DefaultGateCoverageThreshold.
	CoverageThreshold float64 `json:"coverageThreshold,omitempty"`
	// Requery, when true, enables the bounded re-query loop: on a result the
	// filter left insufficient, a tool-less out-of-band model call writes a
	// narrower read-only call, it is executed, and the improved text is appended
	// to the stub. Off by default — the parent model can always re-query itself.
	Requery bool `json:"requery,omitempty"`
	// MaxRequery bounds the re-query rounds for one result. <= 0 uses
	// DefaultGateMaxRequery.
	MaxRequery int `json:"maxRequery,omitempty"`
}

const (
	// DefaultGateKeepThreshold is the gate's keep threshold (the top of the
	// uncertain band): at or above it a block is confidently relevant.
	DefaultGateKeepThreshold = 0.5
	// DefaultGateDropThreshold is the gate's drop threshold (the bottom of the
	// band): only a confident-no below it hides a block.
	//
	// It is 0.2, not winnow's 0.1, because it is calibrated on KajiCode's own
	// workload. Measured live over four real sessions (975 judged blocks, jev-1.13):
	// the classifier's block probabilities cluster in 0.3–0.7 (median 0.47–0.55),
	// and the p<0.1 bin is EMPTY — a 0.1 floor hides nothing. The lowest floor with
	// near-zero regret (a needed block hidden) is 0.2: below it, 2.1% of blocks are
	// hidden for 1 of 405 needed lost; 0.3 hides 6.8% for 2 lost; 0.4 hides 17.3%
	// for 14 lost. 0.2 keeps the gate honest and safe; raising it trades more
	// hiding for more regret. This is winnow's own finding (~5% hidden, ~0 regret),
	// shifted for this distribution.
	DefaultGateDropThreshold = 0.2
	// DefaultGateMinPruneRatio is the minimum hidden share that justifies a stub.
	DefaultGateMinPruneRatio = 0.2
	// DefaultGateMaxHiddenRatio is the maximum share of a result the gate may
	// hide. Without a ceiling a classifier that scores every block low hides the
	// whole result, leaving the model with only a stub and a spill path — it then
	// re-reads, gets gated again, and burns turns on no-op calls. Half is the
	// point where a result stops being a result: past it the stub is no longer a
	// filter but a blackout.
	DefaultGateMaxHiddenRatio = 0.5
	// DefaultGateMinBytes is the minimum result size the gate will consider.
	DefaultGateMinBytes = 1500
	// DefaultGateCoverageThreshold is the kept-text sufficiency trigger: a
	// coverage probability below it means the filtered result is not enough to
	// proceed, so a re-query is considered. It is high because the question
	// only fires the loop for a result the filter already gutted — the cost of a
	// wasted "let me look again" is bounded and the cost of a silent bad answer
	// is not.
	DefaultGateCoverageThreshold = 0.6
	// DefaultGateMaxRequery bounds the re-query rounds for one result.
	DefaultGateMaxRequery = 2
)

// EffectiveKeepThreshold returns the configured keep threshold or its default.
func (f ToolResultFeature) EffectiveKeepThreshold() float64 {
	if f.KeepThreshold > 0 {
		return f.KeepThreshold
	}
	return DefaultGateKeepThreshold
}

// EffectiveDropThreshold returns the configured drop threshold or its default,
// clamped to the keep threshold so the band can never invert. The default is the
// conservative clean-tail bin (DefaultGateDropThreshold), so an unconfigured
// gate only hides what the classifier is confident is irrelevant.
func (f ToolResultFeature) EffectiveDropThreshold() float64 {
	keep := f.EffectiveKeepThreshold()
	drop := f.DropThreshold
	if drop <= 0 {
		drop = DefaultGateDropThreshold
	}
	if drop > keep {
		return keep
	}
	return drop
}

// EffectiveMinPruneRatio returns the configured minimum hidden share or its
// default, clamped to [0,1].
func (f ToolResultFeature) EffectiveMinPruneRatio() float64 {
	if f.MinPruneRatio <= 0 {
		return DefaultGateMinPruneRatio
	}
	return min(f.MinPruneRatio, 1)
}

// EffectiveMaxHiddenRatio returns the configured ceiling on the hidden share, or
// its default, clamped to (0,1]. A non-positive value takes the default.
func (f ToolResultFeature) EffectiveMaxHiddenRatio() float64 {
	if f.MaxHiddenRatio <= 0 {
		return DefaultGateMaxHiddenRatio
	}
	return min(f.MaxHiddenRatio, 1)
}

// EffectiveMinBytes returns the configured minimum result size or its default.
func (f ToolResultFeature) EffectiveMinBytes() int {
	if f.MinBytes > 0 {
		return f.MinBytes
	}
	return DefaultGateMinBytes
}

// EffectiveCoverageThreshold returns the configured coverage trigger or its
// default, clamped to [0,1].
func (f ToolResultFeature) EffectiveCoverageThreshold() float64 {
	if f.CoverageThreshold <= 0 {
		return DefaultGateCoverageThreshold
	}
	return min(f.CoverageThreshold, 1)
}

// EffectiveMaxRequery returns the configured re-query round cap or its default.
// It is clamped to a small ceiling: the loop exists to recover a gutted result,
// not to search, so an oversized cap is always a configuration mistake.
func (f ToolResultFeature) EffectiveMaxRequery() int {
	if f.MaxRequery <= 0 {
		return DefaultGateMaxRequery
	}
	return min(f.MaxRequery, 5)
}

// IsZero reports whether the classifier config holds nothing worth persisting.
// Config.MarshalJSON uses it to omit an empty `classifier` block.
func (c Config) IsZero() bool {
	return !c.Enabled &&
		len(c.Profiles) == 0 &&
		strings.TrimSpace(c.Active) == "" &&
		c.Features == Features{}
}

// UpsertProfile adds profile, or amends the existing profile of the same name
// field-by-field (empty fields in profile leave the existing value). It returns
// whether an existing profile was updated rather than appended, so a CLI surface
// can report "added" vs "updated".
func (c *Config) UpsertProfile(profile Profile) bool {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return false
	}
	for index := range c.Profiles {
		if strings.EqualFold(strings.TrimSpace(c.Profiles[index].Name), name) {
			mergeProfileFields(&c.Profiles[index], profile)
			return true
		}
	}
	c.Profiles = append(c.Profiles, profile)
	return false
}

// RemoveProfile deletes the named profile and clears Active if it pointed at it.
func (c *Config) RemoveProfile(name string) bool {
	name = strings.TrimSpace(name)
	removed := false
	// Copy-on-write: never compact into the caller's backing array, so another
	// holder of the original slice cannot observe overwritten elements.
	kept := make([]Profile, 0, len(c.Profiles))
	for _, profile := range c.Profiles {
		if strings.EqualFold(strings.TrimSpace(profile.Name), name) {
			removed = true
			continue
		}
		kept = append(kept, profile)
	}
	c.Profiles = kept
	if removed && strings.EqualFold(strings.TrimSpace(c.Active), name) {
		c.Active = ""
	}
	return removed
}

// SetActive points the capability at the named profile, reporting false when no
// such profile is registered.
func (c *Config) SetActive(name string) bool {
	if _, ok := c.Profile(name); !ok {
		return false
	}
	c.Active = strings.TrimSpace(name)
	return true
}

func mergeProfileFields(dst *Profile, src Profile) {
	mergeString(&dst.Kind, src.Kind)
	mergeString(&dst.Endpoint, src.Endpoint)
	mergeString(&dst.BaseURL, src.BaseURL)
	mergeString(&dst.Model, src.Model)
	mergeString(&dst.AuthHeader, src.AuthHeader)
	mergeString(&dst.AuthScheme, src.AuthScheme)
	mergeString(&dst.APIKeyEnv, src.APIKeyEnv)
	mergeString(&dst.APIKey, src.APIKey)
	if src.APIKeyStored {
		dst.APIKeyStored = true
	}
	if len(src.Headers) > 0 {
		if dst.Headers == nil {
			dst.Headers = map[string]string{}
		}
		for key, value := range src.Headers {
			dst.Headers[key] = value
		}
	}
	if src.TimeoutMS > 0 {
		dst.TimeoutMS = src.TimeoutMS
	}
}

func mergeString(dst *string, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		*dst = trimmed
	}
}

// ActiveProfile returns the profile named by Active, or the first profile, or
// false when none is registered.
func (c Config) ActiveProfile() (Profile, bool) {
	if len(c.Profiles) == 0 {
		return Profile{}, false
	}
	if active := strings.TrimSpace(c.Active); active != "" {
		for _, profile := range c.Profiles {
			if strings.EqualFold(strings.TrimSpace(profile.Name), active) {
				return profile, true
			}
		}
	}
	return c.Profiles[0], true
}

// Profile returns the named profile.
func (c Config) Profile(name string) (Profile, bool) {
	for _, profile := range c.Profiles {
		if strings.EqualFold(strings.TrimSpace(profile.Name), strings.TrimSpace(name)) {
			return profile, true
		}
	}
	return Profile{}, false
}

// New builds the classifier for a config, resolving the API key from the
// environment (APIKeyEnv) or, failing that, the injected credential reader. It
// returns (nil, nil) when the classifier is disabled or has no usable profile —
// the signal to every caller that the capability is off.
func New(cfg Config, resolve APIKeyResolver) (Classifier, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	profile, ok := cfg.ActiveProfile()
	if !ok {
		return nil, nil
	}
	if strings.TrimSpace(profile.URL()) == "" {
		return nil, fmt.Errorf("classifier profile %q has no endpoint", profile.Name)
	}
	return NewJev(profile, resolve), nil
}

// NewJev builds the Jev-shaped HTTP backend for one profile. A nil resolve, an
// unset env var, and a missing stored key all resolve to "no key", which is only
// an error when the endpoint actually needs one — so unauthenticated local
// routers still work.
func NewJev(profile Profile, resolve APIKeyResolver) *JevClient {
	key := strings.TrimSpace(os.Getenv(strings.TrimSpace(profile.APIKeyEnv)))
	if key == "" {
		key = strings.TrimSpace(profile.APIKey)
	}
	if key == "" && resolve != nil {
		if stored, ok := resolve(profile.Name); ok {
			key = strings.TrimSpace(stored)
		}
	}
	return &JevClient{
		name:     profile.Name,
		endpoint: profile.URL(),
		model:    strings.TrimSpace(profile.Model),
		timeout:  profile.timeoutMS(),
		headers: providerio.AuthHeaders{
			APIKey:            key,
			DefaultAuthHeader: "Authorization",
			DefaultAuthScheme: "Bearer",
			AuthHeader:        profile.AuthHeader,
			AuthScheme:        profile.AuthScheme,
			CustomHeaders:     profile.Headers,
		},
	}
}

// used by tests to build a client against an httptest server without a profile.
func newJevClient(name, endpoint string, headers providerio.AuthHeaders, timeoutMS int) *JevClient {
	return &JevClient{name: name, endpoint: endpoint, timeout: timeoutMS, headers: headers}
}
