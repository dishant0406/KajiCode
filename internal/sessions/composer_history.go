package sessions

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	defaultComposerHistorySessions = 50
	defaultComposerHistoryEntries  = 500
)

type ComposerHistoryGroup struct {
	Session Metadata
	Entries []string
}

type ComposerHistoryOptions struct {
	Cwd             string
	ActiveSessionID string
	MaxSessions     int
	MaxEntries      int
}

// ComposerHistory returns exact submitted composer values grouped by resumable
// sessions in one workspace. The active session is first; remaining sessions
// retain ListResumable's newest-first order. Entries are oldest-first.
func (store *Store) ComposerHistory(options ComposerHistoryOptions) ([]ComposerHistoryGroup, error) {
	maxSessions := options.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultComposerHistorySessions
	}
	maxEntries := options.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultComposerHistoryEntries
	}
	metas, err := store.ListResumable()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].SessionID == options.ActiveSessionID && metas[j].SessionID != options.ActiveSessionID
	})
	groups := make([]ComposerHistoryGroup, 0, min(maxSessions, len(metas)))
	total := 0
	for _, meta := range metas {
		if len(groups) >= maxSessions || total >= maxEntries {
			break
		}
		if !SameWorkspace(meta.Cwd, options.Cwd) {
			continue
		}
		events, readErr := store.ReadEvents(meta.SessionID)
		if readErr != nil {
			continue
		}
		entries := make([]string, 0)
		for _, event := range events {
			if event.Type != EventComposerInput || total+len(entries) >= maxEntries {
				continue
			}
			var payload struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || strings.TrimSpace(payload.Text) == "" {
				continue
			}
			if len(entries) == 0 || entries[len(entries)-1] != payload.Text {
				entries = append(entries, payload.Text)
			}
		}
		if len(entries) == 0 {
			continue
		}
		groups = append(groups, ComposerHistoryGroup{Session: meta, Entries: entries})
		total += len(entries)
	}
	return groups, nil
}

// SameWorkspace compares persisted workspace paths using platform path rules.
// Empty legacy paths remain visible for backward compatibility.
func SameWorkspace(sessionCwd, workspaceCwd string) bool {
	sessionCwd = strings.TrimSpace(sessionCwd)
	workspaceCwd = strings.TrimSpace(workspaceCwd)
	if sessionCwd == "" || workspaceCwd == "" {
		return true
	}
	sessionCwd = canonicalWorkspacePath(sessionCwd)
	workspaceCwd = canonicalWorkspacePath(workspaceCwd)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(sessionCwd, workspaceCwd)
	}
	return sessionCwd == workspaceCwd
}

func canonicalWorkspacePath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
