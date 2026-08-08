package harness

import (
	"fmt"
	"strings"
	"time"
)

// ApplyOptions configures the apply critical section.
type ApplyOptions struct {
	Plan    LearningPlan
	Trigger string
	Now     func() time.Time
}

// ApplyLearning runs the apply critical section: under the store's OS file
// lock it reloads the current state, detects concurrent changes to entries the
// plan's baseline saw, applies each proposal, and records a refinement event.
// It is the single writer path for automatic learning and never leaves the
// store partially written.
func ApplyLearning(store *Store, options ApplyOptions) LearningResult {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	source := strings.TrimSpace(options.Trigger)
	if source == "" {
		source = "manual"
	}
	refinementID := NewRefinementID(now())
	result := LearningResult{
		PlanSummary:  options.Plan.Summary,
		Rationale:    options.Plan.Rationale,
		RefinementID: refinementID,
		HarnessPath:  store.Dir,
	}

	// Baseline versions by (kind,id) for conflict detection: an entry the plan
	// observed that has since been edited or removed by another writer must not
	// be blindly overwritten (that would discard the other writer's lesson).
	baselineVersions := map[string]int{}
	for _, entry := range options.Plan.Baseline.Entries {
		baselineVersions[entryKey(entry)] = entry.Version
	}

	err := store.WithLock(func(state State) (State, error) {
		next, outcomes, errorsList := applyProposals(state, options.Plan.Proposals, baselineVersions, source, refinementID, now())
		result.Outcomes = outcomes
		result.Errors = errorsList
		return next, nil
	})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("apply failed: %v", err))
	}
	return result
}

func applyProposals(state State, proposals []EditProposal, baselineVersions map[string]int, source, refinementID string, now time.Time) (State, []EditOutcome, []string) {
	entries := state.Entries
	var outcomes []EditOutcome
	var errorsList []string
	var changes []string

	for _, proposal := range proposals {
		outcome, newEntries, ok := applyOne(proposal, entries, baselineVersions, now)
		if ok {
			entries = newEntries
			if outcome.Applied {
				changes = append(changes, fmt.Sprintf("%s:%s:%s", proposal.Action, proposal.Kind, proposal.ID))
			}
		}
		outcomes = append(outcomes, outcome)
		if outcome.Error != "" {
			errorsList = append(errorsList, outcome.Error)
		}
	}

	state.Entries = entries
	if len(changes) > 0 {
		state.Refinements = append(state.Refinements, RefinementEvent{
			ID:        refinementID,
			Trigger:   source,
			Changes:   changes,
			Evidence:  strings.Join(changes, ", "),
			CreatedAt: now.UTC().Format(time.RFC3339),
		})
	}
	return state, outcomes, errorsList
}

// applyOne validates and applies a single proposal against the given entries,
// returning the outcome, the (possibly) mutated entry slice, and whether the
// mutation is committed (ok) vs blocked by a conflict.
func applyOne(proposal EditProposal, entries []Entry, baselineVersions map[string]int, now time.Time) (EditOutcome, []Entry, bool) {
	if proposal.ID == BasePromptID {
		return EditOutcome{Proposal: proposal, Error: "base system prompt is immutable"}, entries, false
	}
	target := proposal.Scope
	if target == "" {
		target = ScopeLocal
	}

	switch proposal.Action {
	case ActionCreate:
		if idxEntry(entries, proposal.Kind, proposal.ID) >= 0 {
			return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("%s:%s already exists", proposal.Kind, proposal.ID)}, entries, false
		}
		entry := NewEntry(proposal.Kind, proposal.Title, proposal.Content, proposal.ID, proposal.Path, target, "learning", now)
		if proposal.Recipe != nil {
			entry.Recipe = proposal.Recipe
		}
		return EditOutcome{Proposal: proposal, Applied: true, After: &entry}, append(entries, entry), true

	case ActionUpdate:
		idx := idxEntry(entries, proposal.Kind, proposal.ID)
		if idx < 0 {
			return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("%s:%s does not exist", proposal.Kind, proposal.ID)}, entries, false
		}
		if got, saw := baselineVersions[entryKey(entries[idx])]; saw && got != entries[idx].Version {
			return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("conflict: %s:%s changed since plan (was v%d, now v%d)", proposal.Kind, proposal.ID, got, entries[idx].Version)}, entries, false
		}
		entry := entries[idx]
		if proposal.Title != "" {
			entry.Title = proposal.Title
		}
		if proposal.Content != "" {
			entry.Content = proposal.Content
		}
		if proposal.Path != "" {
			entry.Path = proposal.Path
		}
		if proposal.Recipe != nil {
			entry.Recipe = proposal.Recipe
		}
		entry.Scope = target
		entry.UpdatedAt = now.UTC().Format(time.RFC3339)
		entry.Version++
		entries[idx] = entry
		return EditOutcome{Proposal: proposal, Applied: true, Before: beforePtr(entries, idx), After: &entry}, entries, true

	case ActionDelete:
		idx := idxEntry(entries, proposal.Kind, proposal.ID)
		if idx < 0 {
			return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("%s:%s does not exist", proposal.Kind, proposal.ID)}, entries, false
		}
		if got, saw := baselineVersions[entryKey(entries[idx])]; saw && got != entries[idx].Version {
			return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("conflict: %s:%s changed since plan (was v%d, now v%d)", proposal.Kind, proposal.ID, got, entries[idx].Version)}, entries, false
		}
		before := entries[idx]
		entries = append(entries[:idx], entries[idx+1:]...)
		return EditOutcome{Proposal: proposal, Applied: true, Before: &before}, entries, true

	default:
		return EditOutcome{Proposal: proposal, Error: fmt.Sprintf("unknown action %q", proposal.Action)}, entries, false
	}
}

func beforePtr(entries []Entry, idx int) *Entry {
	if idx < 0 || idx >= len(entries) {
		return nil
	}
	e := entries[idx]
	return &e
}

func idxEntry(entries []Entry, kind Kind, id string) int {
	for i, e := range entries {
		if e.Kind == kind && e.ID == id {
			return i
		}
	}
	return -1
}
