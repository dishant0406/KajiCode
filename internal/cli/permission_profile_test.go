package cli

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

func TestSplitLeadingPermissionFlag(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want agent.PermissionMode
	}{
		{"space", []string{"--permissions", "read-only", "exec"}, agent.PermissionModeReadOnly},
		{"inline", []string{"--permissions=bypass-all"}, agent.PermissionModeBypassAll},
		{"last wins", []string{"--permissions=ask-all", "--permissions", "read-write"}, agent.PermissionModeReadWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := splitLeadingPermissionFlag(test.args)
			if err != nil {
				t.Fatalf("splitLeadingPermissionFlag: %v", err)
			}
			if got != test.want {
				t.Fatalf("mode = %q, want %q", got, test.want)
			}
		})
	}
	for _, args := range [][]string{{"--permissions"}, {"--permissions="}, {"--permissions", "unknown"}} {
		if _, _, err := splitLeadingPermissionFlag(args); err == nil {
			t.Fatalf("splitLeadingPermissionFlag(%q) succeeded, want error", args)
		}
	}
}

func TestStoredExecPermissionProfilePrecedence(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store := sessions.NewStore(sessions.StoreOptions{})
	session, err := store.Create(sessions.CreateInput{SessionID: "stored_profile", PermissionProfile: string(agent.PermissionModeBypassAll)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := storedExecPermissionProfile(execOptions{resume: session.SessionID})
	if err != nil {
		t.Fatalf("storedExecPermissionProfile: %v", err)
	}
	if got != agent.PermissionModeBypassAll {
		t.Fatalf("mode = %q, want bypass-all", got)
	}
	for _, options := range []execOptions{
		{resume: session.SessionID, permissionExplicit: true},
		{resume: session.SessionID, skipPermissionsUnsafe: true},
		{resume: session.SessionID, autonomyExplicit: true},
	} {
		got, err := storedExecPermissionProfile(options)
		if err != nil {
			t.Fatalf("storedExecPermissionProfile override: %v", err)
		}
		if got != "" {
			t.Fatalf("explicit override restored %q, want empty", got)
		}
	}
}
