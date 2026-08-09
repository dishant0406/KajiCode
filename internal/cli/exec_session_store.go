package cli

import (
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// execSessionStoreFor returns a durable session store adapter when a real Store
// and a concrete session are present; otherwise nil, which leaves tools on their
// in-process fallback.
func execSessionStoreFor(store *sessions.Store, sessionID string) tools.SessionStore {
	if store == nil || sessionID == "" {
		return nil
	}
	return execSessionStore{store: store}
}

// execSessionStore adapts the durable sessions.Store to the narrow
// tools.SessionStore surface used by session-backed tools (todo_read /
// todo_write). Passing the raw session ID also namespaces writes per session, so
// the tool and the store agree on the identity.
type execSessionStore struct {
	store *sessions.Store
}

var _ tools.SessionStore = execSessionStore{}

func (s execSessionStore) ReadMetadata(id string) (map[string]any, error) {
	state, err := s.store.ReadSessionState(id)
	if err != nil {
		return nil, err
	}
	return map[string]any(state), nil
}

func (s execSessionStore) WriteMetadata(id string, meta map[string]any) error {
	return s.store.WriteSessionState(id, sessions.SessionState(meta))
}
