package config

import (
	"encoding/json"
	"fmt"
	"os"
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
	issues := validateHarnessConfig(cfg.Harness)
	issues = append(issues, validateLearningConfig(cfg.Learning)...)
	issues = append(issues, validateImagesConfig(cfg)...)
	return issues
}

// validateImagesConfig validates the attached-image envelope settings.
func validateImagesConfig(cfg FileConfig) []Issue {
	var issues []Issue
	for _, field := range []struct {
		name  string
		value int
	}{{"maxWidth", cfg.Images.MaxWidth}, {"maxHeight", cfg.Images.MaxHeight}, {"maxBytes", cfg.Images.MaxBytes}} {
		if field.value < 0 {
			issues = append(issues, Issue{
				FieldPath: "images." + field.name,
				Message:   fmt.Sprintf("images.%s must not be negative", field.name),
			})
		}
	}
	if cfg.Images.MaxBytes > 0 && cfg.Images.MaxBytes < 1024 {
		issues = append(issues, Issue{
			FieldPath: "images.maxBytes",
			Message:   "images.maxBytes is below 1 KiB; images would not fit",
		})
	}
	return issues
}
