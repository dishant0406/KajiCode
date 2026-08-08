package harness

import "time"

// RecordRefinement appends a refinement event to the store's history under the
// OS file lock. It is used by the manual learn tool and diagnostics to leave a
// trace even when no entries changed, and is a no-op when ref is empty.
func RecordRefinement(store *Store, ref RefinementEvent) error {
	if store == nil || ref.ID == "" {
		return nil
	}
	return store.WithLock(func(state State) (State, error) {
		state.Refinements = append(state.Refinements, ref)
		return state, nil
	})
}

// NewRefinementEvent builds a refinement event stamping CreatedAt from now.
func NewRefinementEvent(trigger string, changes []string, evidence string, now time.Time) RefinementEvent {
	return RefinementEvent{
		ID:        NewRefinementID(now),
		Trigger:   trigger,
		Changes:   changes,
		Evidence:  evidence,
		CreatedAt: now.UTC().Format(time.RFC3339),
	}
}
