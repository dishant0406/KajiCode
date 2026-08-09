package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// stateFilename is the per-session sidecar holding arbitrary key/value tool state
// (e.g. the todo_read / todo_write todo list). It is distinct from metadata.json,
// which owns the typed SessionRecord/Metadata identity fields.
const stateFilename = "state.json"

// SessionState is a per-session key/value bag persisted as state.json. Values are
// JSON-decoded; callers re-marshal typed data into the map before writing.
type SessionState map[string]any

// ReadSessionState returns the session's persisted state, or an empty map when
// none exists. A missing state.json is not an error.
func (store *Store) ReadSessionState(sessionID string) (SessionState, error) {
	if sessionID == "" {
		return SessionState{}, nil
	}
	path := filepath.Join(store.sessionPath(sessionID), stateFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionState{}, nil
		}
		return nil, err
	}
	var state SessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state == nil {
		state = SessionState{}
	}
	return state, nil
}

// WriteSessionState persists the session's state atomically.
func (store *Store) WriteSessionState(sessionID string, state SessionState) error {
	if sessionID == "" {
		return nil
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(store.sessionPath(sessionID), stateFilename)
	return store.writeFileAtomic(path, raw, 0o600)
}
