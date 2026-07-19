package cli

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

func TestPreflightResumeLatestSkipsSideSessions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store := sessions.NewStore(sessions.StoreOptions{})
	if _, err := store.Create(sessions.CreateInput{SessionID: "side", SessionKind: sessions.SessionKindSide}); err != nil {
		t.Fatalf("create side session: %v", err)
	}

	err := preflightExecSession(execOptions{resumeLatest: true})
	if err == nil || !strings.Contains(err.Error(), "No Zero sessions available to resume") {
		t.Fatalf("preflightExecSession error = %v, want no resumable sessions", err)
	}
}
