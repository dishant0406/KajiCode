package sessions

import (
	"reflect"
	"testing"
)

func TestComposerHistoryGroupsWorkspaceAndPrioritizesActiveSession(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-07-21T10:00:00Z")})
	create := func(id, cwd string, kind SessionKind, entries ...string) {
		t.Helper()
		if _, err := store.Create(CreateInput{SessionID: id, Cwd: cwd, SessionKind: kind}); err != nil {
			t.Fatal(err)
		}
		for _, text := range entries {
			if _, err := store.AppendEvent(id, AppendEventInput{Type: EventComposerInput, Payload: map[string]string{"text": text}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("older", "/repo", "", "old one", "old two")
	create("active", "/repo", "", "active one")
	create("other", "/other", "", "wrong project")
	create("child", "/repo", SessionKindChild, "wrong kind")

	groups, err := store.ComposerHistory(ComposerHistoryOptions{Cwd: "/repo", ActiveSessionID: "older"})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{groups[0].Session.SessionID, groups[1].Session.SessionID}; !reflect.DeepEqual(got, []string{"older", "active"}) {
		t.Fatalf("session order = %#v", got)
	}
	if !reflect.DeepEqual(groups[0].Entries, []string{"old one", "old two"}) {
		t.Fatalf("active entries = %#v", groups[0].Entries)
	}
}

func TestComposerHistoryHonorsEntryLimitAndSkipsMalformedPayloads(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-07-21T10:00:00Z")})
	if _, err := store.Create(CreateInput{SessionID: "history", Cwd: "/repo"}); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{
		map[string]string{"text": "first"},
		map[string]string{"other": "missing"},
		map[string]string{"text": "second"},
		map[string]string{"text": "third"},
	} {
		if _, err := store.AppendEvent("history", AppendEventInput{Type: EventComposerInput, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := store.ComposerHistory(ComposerHistoryOptions{Cwd: "/repo", MaxEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || !reflect.DeepEqual(groups[0].Entries, []string{"first", "second"}) {
		t.Fatalf("groups = %#v", groups)
	}
}
