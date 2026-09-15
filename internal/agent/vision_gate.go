package agent

import (
	"sync"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

var (
	defaultModelRegistryOnce sync.Once
	defaultModelRegistry     modelregistry.Registry
)

// modelSupportsVisionDefault reports whether modelID accepts image input, using
// the default model catalog (models.dev-aware). It gates tool-returned images
// (read_file on a raster image) so a text-only model gets a notice instead of an
// image part the provider rejects with a run-killing 400. Unknown models report
// false, matching modelregistry.SupportsVision's safe default.
func modelSupportsVisionDefault(modelID string) bool {
	defaultModelRegistryOnce.Do(func() {
		if registry, err := modelregistry.DefaultRegistry(); err == nil {
			defaultModelRegistry = registry
		}
	})
	return modelregistry.SupportsVision(defaultModelRegistry, modelID)
}
