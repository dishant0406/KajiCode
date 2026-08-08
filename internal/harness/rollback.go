package harness

import (
	"fmt"
	"strings"
	"time"
)

// RollbackOptions configures a rollback. Outcomes are the recorded outcomes of
// a prior ApplyLearning result.
type RollbackOptions struct {
	Outcomes []EditOutcome
	Now      func() time.Time
}

// RollbackInverts undoes a prior apply pass by inverting each applied proposal
// against the CURRENT state, under the same OS-file-lock critical section. It
// is best-effort: a proposal that no longer applies (entry deleted/edited
// since) is reported as an error, never silently forced.
func RollbackInverts(store *Store, options RollbackOptions) LearningResult {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	refinementID := NewRefinementID(now())
	result := LearningResult{RefinementID: refinementID, HarnessPath: store.Dir}

	err := store.WithLock(func(state State) (State, error) {
		next := state
		var errorsList []string
		var changes []string
		for _, outcome := range options.Outcomes {
			if !outcome.Applied {
				continue
			}
			proposal := outcome.Proposal
			switch proposal.Action {
			case ActionCreate:
				if idxEntry(next.Entries, proposal.Kind, proposal.ID) < 0 {
					errorsList = append(errorsList, fmt.Sprintf("rollback skipped: %s:%s already absent", proposal.Kind, proposal.ID))
					continue
				}
				next.Entries = removeEntry(next.Entries, proposal.Kind, proposal.ID)
				changes = append(changes, fmt.Sprintf("rollback delete:%s:%s", proposal.Kind, proposal.ID))
			case ActionUpdate:
				idx := idxEntry(next.Entries, proposal.Kind, proposal.ID)
				if idx < 0 {
					errorsList = append(errorsList, fmt.Sprintf("rollback skipped: %s:%s absent", proposal.Kind, proposal.ID))
					continue
				}
				if outcome.Before != nil && outcome.Before.Version > 0 {
					restored := *outcome.Before
					restored.UpdatedAt = now().UTC().Format(time.RFC3339)
					restored.Version++
					next.Entries[idx] = restored
					changes = append(changes, fmt.Sprintf("rollback restore:%s:%s", proposal.Kind, proposal.ID))
				} else {
					errorsList = append(errorsList, fmt.Sprintf("rollback skipped: no prior state for %s:%s", proposal.Kind, proposal.ID))
				}
			case ActionDelete:
				if outcome.Before == nil {
					errorsList = append(errorsList, fmt.Sprintf("rollback skipped: no prior state for %s:%s", proposal.Kind, proposal.ID))
					continue
				}
				if idxEntry(next.Entries, proposal.Kind, proposal.ID) >= 0 {
					errorsList = append(errorsList, fmt.Sprintf("rollback skipped: %s:%s already present", proposal.Kind, proposal.ID))
					continue
				}
				restored := *outcome.Before
				restored.UpdatedAt = now().UTC().Format(time.RFC3339)
				next.Entries = append(next.Entries, restored)
				changes = append(changes, fmt.Sprintf("rollback create:%s:%s", proposal.Kind, proposal.ID))
			}
		}
		if len(changes) > 0 {
			next.Refinements = append(next.Refinements, RefinementEvent{
				ID:        refinementID,
				Trigger:   "rollback",
				Changes:   changes,
				Evidence:  strings.Join(changes, ", "),
				CreatedAt: now().UTC().Format(time.RFC3339),
			})
		}
		result.Errors = errorsList
		return next, nil
	})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("rollback failed: %v", err))
	}
	return result
}

func removeEntry(entries []Entry, kind Kind, id string) []Entry {
	idx := idxEntry(entries, kind, id)
	if idx < 0 {
		return entries
	}
	return append(entries[:idx], entries[idx+1:]...)
}
