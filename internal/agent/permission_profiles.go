package agent

import "github.com/dishant0406/KajiCode/internal/tools"

// PermissionProfiles returns the user-selectable profiles in increasing order of authority.
func PermissionProfiles() []PermissionMode {
	return []PermissionMode{
		PermissionModeAskAll,
		PermissionModeReadOnly,
		PermissionModeReadWrite,
		PermissionModeBypassAll,
	}
}

// NormalizePermissionMode preserves legacy modes while rejecting unknown values to the
// safest interactive profile.
func NormalizePermissionMode(mode PermissionMode) PermissionMode {
	switch mode {
	case PermissionModeAuto, PermissionModeAsk, PermissionModeUnsafe,
		PermissionModeAskAll, PermissionModeReadOnly, PermissionModeReadWrite,
		PermissionModeBypassAll, PermissionModeSpecDraft, PermissionModeMemberAuto:
		return mode
	default:
		return PermissionModeAskAll
	}
}

func profilePermission(mode PermissionMode, tool tools.Tool, args map[string]any) tools.Permission {
	if mode == PermissionModeBypassAll {
		return tools.PermissionAllow
	}
	permission := effectivePermission(tool, args)
	if permission == tools.PermissionDeny {
		return permission
	}

	sideEffect := tool.Safety().SideEffect
	if sideEffect == tools.SideEffectNone {
		return tools.PermissionAllow
	}

	switch mode {
	case PermissionModeAskAll:
		return tools.PermissionPrompt
	case PermissionModeReadOnly:
		if sideEffect == tools.SideEffectRead {
			return tools.PermissionAllow
		}
		return tools.PermissionPrompt
	case PermissionModeReadWrite:
		if sideEffect == tools.SideEffectRead || sideEffect == tools.SideEffectWrite {
			return tools.PermissionAllow
		}
		return tools.PermissionPrompt
	case PermissionModeBypassAll:
		return tools.PermissionAllow
	default:
		return permission
	}
}

func bypassesSandbox(mode PermissionMode) bool {
	return mode == PermissionModeBypassAll
}
