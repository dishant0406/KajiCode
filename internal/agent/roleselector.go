package agent

import (
	"context"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// RoleContext describes the agent-loop signal the phase selector uses to pick the
// active task role. It is derived per turn from loop-local state: whether a todo
// list is present, whether the last tool was a write/edit, whether a plan mode is
// active, and how many turns have elapsed. PromptedRole is the explicit role the
// operator set via /set role or --role; it is the source of truth for explicit
// routing.
type RoleContext struct {
	HasTodoList            bool
	LastToolWasWriteOrEdit bool
	// HasImages is true when the turn's user message carries one or more attached
	// images. The agent loop seeds images only on the initial user turn, so for a
	// given run this is true for turn 0 when options.Images is non-empty. Vision
	// auto-routing keys off it to route exactly that message to a vision model and
	// swap back to the default model afterwards.
	HasImages bool
	// PlanMode is true when a plan/spec mode is active (e.g. /spec, /plan). Used by
	// intent auto-routing to route to the plan role. It is a documented hook: wire it
	// wherever a plan-mode flag is threaded today; absent one it stays false so no
	// route is taken. Plan is intentionally mode-bound, never prose-inferred.
	PlanMode     bool
	NumTurns     int
	PromptedRole string
}

// RoleSelector picks the active task role from a RoleContext. Implementations must be
// pure (no I/O, no provider calls): the loop only reads the returned string to decide
// whether to swap providers.
type RoleSelector interface {
	RoleFor(ctx RoleContext) string
}

// ExplicitRole always returns the operator-set role. It drives /set role and --role:
// the model is fixed to Role for the whole run unless the operator clears it.
type ExplicitRole struct {
	Role string
}

// RoleFor returns the fixed role. An empty Role means "no explicit role" — the caller
// treats it exactly like AutoRole returning "" (stay on the default model).
func (e ExplicitRole) RoleFor(RoleContext) string {
	return strings.TrimSpace(e.Role)
}

// AutoRole is the conservative automatic classifier. It only routes to a role when
// there is a strong signal — a plan mode is active (-> plan), or a todo list is
// present AND the last tool mutated a file (-> implement) — otherwise it returns ""
// (stay on the default model). ExplicitRole is the reliable path; AutoRole is a
// best-effort convenience and never overrides a PromptedRole. No free-form prose
// inference is performed: plan is mode-bound, implement is signal-bound.
type AutoRole struct {
	// Role is the role to use when the plan→implement signal fires (conventionally
	// "implement"). The default is RoleImplement.
	Role string
	// PlanRole is the role used while a plan mode is active. Defaults to RolePlan.
	PlanRole string
}

func (a AutoRole) RoleFor(ctx RoleContext) string {
	if ctx.PromptedRole != "" {
		return ctx.PromptedRole
	}
	if ctx.PlanMode {
		role := strings.TrimSpace(a.PlanRole)
		if role == "" {
			return modelregistry.RolePlan
		}
		return role
	}
	if ctx.HasTodoList && ctx.LastToolWasWriteOrEdit {
		role := strings.TrimSpace(a.Role)
		if role == "" {
			return modelregistry.RoleImplement
		}
		return role
	}
	return ""
}

// RoleRouting wires per-role dispatch into the agent loop. It is opt-in: nil leaves
// the loop byte-identical (the default today). When set, the loop queries RoleFor each
// turn and swaps the run's provider to the selected role's profile via Current.
type RoleRouting struct {
	// Current resolves the provider+profile for a role. ok=false means "route not
	// found; fall back to the active provider" (mirrors RoleRouter.ProviderFor).
	Current func(ctx context.Context, role string) (Provider, config.ProviderProfile, bool)
	// RoleFor picks the active task role for the current turn.
	RoleFor func(RoleContext) string
	// ContextWindowFor reports the model's context window for a profile, so a role
	// switch recomputes the compactor budget (0 = keep the prior window). Wire it to
	// the model registry in exec/app.
	ContextWindowFor func(config.ProviderProfile) int
}
