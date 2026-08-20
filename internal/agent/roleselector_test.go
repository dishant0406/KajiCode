package agent

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

func TestExplicitRoleConstant(t *testing.T) {
	s := ExplicitRole{Role: "design"}
	if got := s.RoleFor(RoleContext{}); got != "design" {
		t.Fatalf("ExplicitRole.RoleFor = %q, want design", got)
	}
	// Whitespace-normalized.
	s.Role = "  plan  "
	if got := s.RoleFor(RoleContext{}); got != "plan" {
		t.Fatalf("ExplicitRole.RoleFor(normalized) = %q, want plan", got)
	}
	// Empty explicit role == no role.
	s.Role = ""
	if got := s.RoleFor(RoleContext{}); got != "" {
		t.Fatalf("ExplicitRole.RoleFor(empty) = %q, want empty", got)
	}
}

func TestAutoRoleConservative(t *testing.T) {
	s := AutoRole{}
	// No strong signal -> stays on default.
	if got := s.RoleFor(RoleContext{HasTodoList: true}); got != "" {
		t.Fatalf("todo-only -> %q, want empty (needs write signal too)", got)
	}
	if got := s.RoleFor(RoleContext{LastToolWasWriteOrEdit: true}); got != "" {
		t.Fatalf("write-only -> %q, want empty (needs todo signal too)", got)
	}
	// Both signals -> implement.
	if got := s.RoleFor(RoleContext{HasTodoList: true, LastToolWasWriteOrEdit: true}); got != "implement" {
		t.Fatalf("todo+write -> %q, want implement", got)
	}
}

func TestAutoRoleRespectsPromptedRole(t *testing.T) {
	s := AutoRole{Role: "implement"}
	// PromptedRole wins even without the todo/write signal.
	if got := s.RoleFor(RoleContext{PromptedRole: "design"}); got != "design" {
		t.Fatalf("PromptedRole -> %q, want design", got)
	}
	// Custom auto role.
	if got := s.RoleFor(RoleContext{HasTodoList: true, LastToolWasWriteOrEdit: true}); got != "implement" {
		t.Fatalf("auto -> %q, want implement", got)
	}
}

func TestAutoRolePlanModeRoutesToPlan(t *testing.T) {
	s := AutoRole{}
	if got := s.RoleFor(RoleContext{PlanMode: true}); got != modelregistry.RolePlan {
		t.Fatalf("plan-mode -> %q, want %q", got, modelregistry.RolePlan)
	}
	// Plan mode wins over the implement signal.
	if got := s.RoleFor(RoleContext{PlanMode: true, HasTodoList: true, LastToolWasWriteOrEdit: true}); got != modelregistry.RolePlan {
		t.Fatalf("plan-mode+todo -> %q, want %q", got, modelregistry.RolePlan)
	}
	// PromptedRole still wins over plan mode.
	if got := s.RoleFor(RoleContext{PlanMode: true, PromptedRole: "review"}); got != "review" {
		t.Fatalf("prompted+plan-mode -> %q, want review", got)
	}
	// Custom PlanRole override.
	s2 := AutoRole{PlanRole: "design"}
	if got := s2.RoleFor(RoleContext{PlanMode: true}); got != "design" {
		t.Fatalf("custom PlanRole -> %q, want design", got)
	}
}

func TestAutoRoleCanonicalDefaults(t *testing.T) {
	s := AutoRole{}
	// Canonical default role ids are exported so callers/UI never hardcode strings.
	if got := s.RoleFor(RoleContext{HasTodoList: true, LastToolWasWriteOrEdit: true}); got != modelregistry.RoleImplement {
		t.Fatalf("implement signal -> %q, want canonical %q", got, modelregistry.RoleImplement)
	}
	if got := s.RoleFor(RoleContext{PlanMode: true}); got != modelregistry.RolePlan {
		t.Fatalf("plan mode -> %q, want canonical %q", got, modelregistry.RolePlan)
	}
}

func TestRoleSelectorStats(t *testing.T) {
	// Ensure the RoleSelector interface is satisfied by both implementations.
	var _ RoleSelector = ExplicitRole{}
	var _ RoleSelector = AutoRole{}
}
