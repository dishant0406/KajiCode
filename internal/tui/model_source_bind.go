package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
	"github.com/dishant0406/KajiCode/internal/modelsource"
)

// bindModelSource points the process-wide models.dev snapshot at the model the
// TUI serves, so every fact read (vision gate, compaction window, /effort, cost,
// pickers) resolves against the same record the CLI bound at startup.
func (m model) bindModelSource() model {
	m.modelSourceRebind(m.effectiveModelName())
	return m
}

// modelSourceRebind re-binds the snapshot after a model/provider/role switch. It
// reads the current provider profile for the models.dev provider slug, so callers
// must set m.providerProfile before calling it.
func (m model) modelSourceRebind(modelID string) {
	modelsource.Bind(m.providerProfile.CatalogID, modelID)
}

// modelSourceRefreshMsg carries the outcome of an explicit models.dev refresh
// triggered from the /models "Refresh models" action. err is the fetch error, if
// any; a non-nil err still means the in-process snapshot was reloaded (from the
// existing cache or the embedded seed), so the UI only warns — it never blocks.
type modelSourceRefreshMsg struct {
	err error
}

// modelSourceRefreshCmd fetches the models.dev snapshot when stale and reloads the
// in-process catalog so the running session picks up the new facts immediately
// (the snapshot is otherwise read exactly once per process). It runs off the UI
// thread; the app applies the result in modelSourceRefreshApplied.
func (m model) modelSourceRefreshCmd() tea.Cmd {
	return func() tea.Msg {
		return modelSourceRefreshMsg{err: modelsource.RefreshAndReload(context.Background())}
	}
}

// modelSourceRefreshApplied rebuilds the model registry and re-binds the snapshot
// after a refresh. Rebuilding the registry is required because curated entries get
// their models.dev facts (modalities, tiers, limits, pricing) layered in at
// construction; the cached Registry would otherwise keep the pre-refresh record
// for the whole session. Resetting the synthesized-entry cache makes models the
// snapshot newly knows re-synthesize from the fresh catalog.
func (m model) modelSourceRefreshApplied() model {
	modelregistry.ResetSynthesizedCache()
	if registry, err := modelregistry.DefaultRegistry(); err == nil {
		m.modelCatalog = registry
	}
	m = m.bindModelSource()
	return m
}
