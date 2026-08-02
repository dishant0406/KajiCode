package agent

type ToolSpec struct {
	Name       string
	SideEffect string
	Permission string
}

func SpecDraftToolAllowed(tool ToolSpec) bool {
	if tool.Name == "ask_user" || tool.Name == "submit_spec" {
		return true
	}
	return tool.SideEffect == "read" && tool.Permission == "allow"
}
