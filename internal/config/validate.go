package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// Issue is a single structured problem found while validating a config file.
// Message is already routed through the package secret redaction.
type Issue struct {
	FieldPath string `json:"fieldPath,omitempty"`
	Message   string `json:"message"`
}

// ValidateFile reads and parses path as a KajiCode FileConfig and runs the same
// semantic provider/model rules used during resolution. It returns the parsed
// config (zero value on parse failure) plus any structured issues. A parse
// failure yields a single issue whose Message wraps the underlying JSON error
// so callers can extract *json.SyntaxError / *json.UnmarshalTypeError offsets
// via errors.As.
func ValidateFile(path string) (FileConfig, []Issue) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Sprintf("read config %s: %v", path, err)}}
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Errorf("invalid config JSON %s: %w", path, err).Error()}}
	}

	issues := validateSemantics(cfg)
	issues = append(issues, unknownFieldIssues(data)...)
	return cfg, issues
}

// ValidateBytes parses data as a KajiCode FileConfig and runs the same semantic
// provider/model rules as ValidateFile. It returns the parsed config (zero
// value on parse failure) plus any structured issues. A parse failure yields a
// single issue whose Message wraps the underlying JSON error (path-less form:
// "invalid config JSON: <err>") so callers can extract *json.SyntaxError /
// *json.UnmarshalTypeError offsets via errors.As.
func ValidateBytes(data []byte) (FileConfig, []Issue) {
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Errorf("invalid config JSON: %w", err).Error()}}
	}
	issues := validateSemantics(cfg)
	issues = append(issues, unknownFieldIssues(data)...)
	return cfg, issues
}

func validateSemantics(cfg FileConfig) []Issue {
	if _, _, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider); err != nil {
		// normalizeProviders already redacts secrets via providerError.
		return []Issue{{FieldPath: "providers", Message: err.Error()}}
	}
	if err := validateSTTConfig(cfg.STT); err != nil {
		return []Issue{{FieldPath: "stt", Message: err.Error()}}
	}
	issues := validateHarnessConfig(cfg.Harness)
	issues = append(issues, validateLearningConfig(cfg.Learning)...)
	issues = append(issues, validateRoleConfig(cfg)...)
	return issues
}

// validateRoleConfig checks the model-routing surface: every modelRoles value must be
// a resolvable selector or an @role alias, and DefaultModel must resolve. Failures are
// reported as Issues (advisory), not hard load errors — an unroutable role falls back
// to the active model at run time.
func validateRoleConfig(cfg FileConfig) []Issue {
	var issues []Issue
	for role, value := range cfg.ModelRoles {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, Issue{
				FieldPath: "modelRoles." + role,
				Message:   fmt.Sprintf("model role %q is empty; remove it or give it a model selector", role),
			})
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(value), "@") {
			continue // alias is validated lazily; it may target a later-defined role
		}
		if !modelRegistryResolves(value) {
			issues = append(issues, Issue{
				FieldPath: "modelRoles." + role,
				Message:   fmt.Sprintf("model role %q selector %q does not resolve to a known model", role, value),
			})
		}
	}
	if strings.TrimSpace(cfg.DefaultModel) != "" && !modelRegistryResolves(cfg.DefaultModel) {
		issues = append(issues, Issue{
			FieldPath: "defaultModel",
			Message:   fmt.Sprintf("defaultModel %q does not resolve to a known model", cfg.DefaultModel),
		})
	}
	if active := strings.TrimSpace(cfg.ActiveRole); active != "" {
		_, isDefault := modelregistry.RoleInfoByID(active)
		configured := false
		for role := range cfg.ModelRoles {
			if strings.EqualFold(role, active) {
				configured = true
				break
			}
		}
		if !isDefault && !configured {
			issues = append(issues, Issue{
				FieldPath: "activeRole",
				Message:   fmt.Sprintf("activeRole %q is not a known role; the run will use the default model", active),
			})
		}
	}
	switch strings.TrimSpace(cfg.Images.VisionRouting) {
	case "auto", "model", "off", "":
	default:
		issues = append(issues, Issue{
			FieldPath: "images.visionRouting",
			Message:   fmt.Sprintf("images.visionRouting %q is invalid; expected auto, model, or off", cfg.Images.VisionRouting),
		})
	}
	return issues
}

// modelRegistryResolves reports whether a model selector resolves through the curated
// catalog. Unknown/custom ids that happen to be configured as profiles are accepted
// (best-effort) because SupportsVision falls back to a name heuristic for them.
func modelRegistryResolves(value string) bool {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return true // registry unavailable — be permissive, do not fail load
	}
	_, ok := registry.Resolve(strings.TrimSpace(value))
	return ok
}
