package modelregistry

import "strings"

// Built-in default task roles, modeled after omp (Oh My Pi)'s fixed role
// catalog. Roles are pre-canned: a user assigns a model to each (via the TUI
// /role flow or modelRoles in config) and routing happens automatically. The
// catalog is a display/discovery surface only — it never forces a model; an
// unset built-in role falls back to the active model unless role routing can
// resolve a capability-appropriate profile (§ RoleRouter).

// Canonical built-in task role identifiers.
const (
	RoleDefault   = "default"
	RolePlan      = "plan"
	RoleDesign    = "design"
	RoleImplement = "implement"
	RoleVision    = "vision"
	RoleReview    = "review"
	RoleFast      = "fast"
)

// RoleInfo describes one built-in role for the /role picker and routing hints.
type RoleInfo struct {
	// ID is the canonical role identifier used in modelRoles/ActiveRole.
	ID string
	// DisplayName is the human-readable role name shown in the UI.
	DisplayName string
	// Tag is a short uppercase marker (e.g. "PLAN") for compact readouts.
	Tag string
	// Summary is a one-line description of what the role routes to.
	Summary string
	// DefaultSelector is a curated model suggestion for assigning a model to
	// this role. It is a DISPLAY-ONLY hint shown in the /role picker — it is
	// never applied implicitly, so catalog-default drift cannot silently change
	// a user's routing. Empty = no suggestion.
	DefaultSelector string
	// Capability is the primary model capability the role benefits from. Used by
	// RoleRouter to resolve an unset built-in role to an available provider whose
	// model carries it. Empty means "active model is fine" (default/implement).
	Capability ModelCapability
}

// builtinRoleCatalog is the source of truth for the fixed default roles, kept in
// a stable order so the /role picker lists built-ins first, then custom roles.
var builtinRoleCatalog = []RoleInfo{
	{
		ID:          RoleDefault,
		DisplayName: "Default",
		Tag:         "DEFAULT",
		Summary:     "The active model — used when no role override applies.",
	},
	{
		ID:              RolePlan,
		DisplayName:     "Architect",
		Tag:             "PLAN",
		Summary:         "Deep planning — prefers a strong long-context reasoning model.",
		DefaultSelector: "claude-opus-4.1",
		Capability:      ModelCapabilityReasoning,
	},
	{
		ID:              RoleDesign,
		DisplayName:     "Designer",
		Tag:             "DESIGN",
		Summary:         "Design and architecture — a reasoning-capable model.",
		DefaultSelector: "claude-sonnet-4.5",
		Capability:      ModelCapabilityReasoning,
	},
	{
		ID:          RoleImplement,
		DisplayName: "Implement",
		Tag:         "IMPL",
		Summary:     "The default loop model — used when the implement signal fires.",
	},
	{
		ID:              RoleVision,
		DisplayName:     "Vision",
		Tag:             "VISION",
		Summary:         "Image analysis — requires an image-capable model.",
		DefaultSelector: "gemini-2.5-pro",
		Capability:      ModelCapabilityVision,
	},
	{
		ID:              RoleReview,
		DisplayName:     "Review",
		Tag:             "REVIEW",
		Summary:         "Second-opinion review — a strong reasoning model.",
		DefaultSelector: "claude-opus-4.1",
		Capability:      ModelCapabilityReasoning,
	},
	{
		ID:              RoleFast,
		DisplayName:     "Fast",
		Tag:             "FAST",
		Summary:         "Cheap low-latency model for frequent, lightweight turns.",
		DefaultSelector: "claude-haiku-4.5",
	},
}

// DefaultRoleIDs returns the canonical default role ids in catalog order.
func DefaultRoleIDs() []string {
	ids := make([]string, len(builtinRoleCatalog))
	for i, r := range builtinRoleCatalog {
		ids[i] = r.ID
	}
	return ids
}

// DefaultRoleCatalog returns the full built-in role catalog (a defensive copy).
func DefaultRoleCatalog() []RoleInfo {
	out := make([]RoleInfo, len(builtinRoleCatalog))
	copy(out, builtinRoleCatalog)
	return out
}

// RoleInfoByID returns the built-in role with the given id, if it is a known
// default role. ok is false for custom/free-form roles and unknown ids.
func RoleInfoByID(id string) (RoleInfo, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, r := range builtinRoleCatalog {
		if r.ID == id {
			return r, true
		}
	}
	return RoleInfo{}, false
}
