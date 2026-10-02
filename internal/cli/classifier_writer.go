package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/config"
)

// classifierWritableConfig mirrors mcpWritableConfig: the typed FileConfig plus
// the raw JSON, so a write preserves any hand-edited or newer keys this KajiCode
// version does not model. profileRaw keeps each profile object's unknown keys
// keyed by name; the typed Profiles slice (loaded from the file) is the source of
// truth for order and membership, so profiles stay an ordered array.
type classifierWritableConfig struct {
	file       config.FileConfig
	raw        map[string]json.RawMessage
	nested     map[string]json.RawMessage
	profileRaw map[string]json.RawMessage

	// enabled records an explicit capability switch written by `configure`, so a
	// report can echo what was persisted rather than what the file held.
	enabled *bool
	// features holds the classifier.features block as raw per-key JSON, parsed
	// lazily so `configure` can set one feature key while every unknown key and
	// every unknown feature survives verbatim.
	features map[string]map[string]json.RawMessage
}

func readClassifierWritableConfig(path string) (classifierWritableConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return classifierWritableConfig{}, fmt.Errorf("classifier config path is required")
	}
	cfg := classifierWritableConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.ensureRaw()
			return cfg, nil
		}
		return classifierWritableConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg.file); err != nil {
		return classifierWritableConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg.raw); err != nil {
		return classifierWritableConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	cfg.ensureRaw()
	if block, ok := cfg.raw["classifier"]; ok && len(block) > 0 && string(block) != "null" {
		if err := json.Unmarshal(block, &cfg.nested); err != nil {
			return classifierWritableConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	}
	cfg.ensureRaw()
	if profilesRaw, ok := cfg.nested["profiles"]; ok && len(profilesRaw) > 0 && string(profilesRaw) != "null" {
		var entries []json.RawMessage
		if err := json.Unmarshal(profilesRaw, &entries); err != nil {
			return classifierWritableConfig{}, fmt.Errorf("invalid config JSON %s: classifier.profiles must be an array: %w", path, err)
		}
		for _, entry := range entries {
			var named struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(entry, &named); err != nil {
				continue
			}
			if name := strings.TrimSpace(named.Name); name != "" {
				cfg.profileRaw[name] = entry
			}
		}
	}
	cfg.ensureRaw()
	return cfg, nil
}

func (cfg *classifierWritableConfig) ensureRaw() {
	if cfg.raw == nil {
		cfg.raw = map[string]json.RawMessage{}
	}
	if cfg.nested == nil {
		cfg.nested = map[string]json.RawMessage{}
	}
	if cfg.profileRaw == nil {
		cfg.profileRaw = map[string]json.RawMessage{}
	}
}

// classifierTypedKeys are the keys the writer owns; every other key of a profile
// object is carried through verbatim by mergeClassifierProfileRaw.
var classifierTypedKeys = []string{
	"name", "kind", "endpoint", "baseURL", "model", "authHeader", "authScheme",
	"apiKeyEnv", "apiKey", "apiKeyStored", "headers", "timeoutMs",
}

func (cfg *classifierWritableConfig) existingProfileRaw(name string) json.RawMessage {
	for key, raw := range cfg.profileRaw {
		if strings.EqualFold(key, strings.TrimSpace(name)) {
			return raw
		}
	}
	return nil
}

func (cfg *classifierWritableConfig) upsertProfile(profile classifier.Profile) (bool, error) {
	cfg.ensureRaw()
	existingRaw := cfg.existingProfileRaw(profile.Name)
	updated := cfg.file.Classifier.UpsertProfile(profile)
	stored, ok := cfg.file.Classifier.Profile(profile.Name)
	if !ok {
		return false, fmt.Errorf("classifier profile %q was not stored", profile.Name)
	}
	data, err := mergeClassifierProfileRaw(existingRaw, stored)
	if err != nil {
		return false, err
	}
	for key := range cfg.profileRaw {
		if key != stored.Name && strings.EqualFold(key, stored.Name) {
			delete(cfg.profileRaw, key)
		}
	}
	cfg.profileRaw[stored.Name] = data
	return updated, nil
}

func mergeClassifierProfileRaw(existing json.RawMessage, profile classifier.Profile) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(existing) > 0 && string(existing) != "null" {
		if err := json.Unmarshal(existing, &merged); err != nil {
			return nil, err
		}
	}
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	for _, key := range classifierTypedKeys {
		delete(merged, key)
	}
	typed, err := json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	var typedRaw map[string]json.RawMessage
	if err := json.Unmarshal(typed, &typedRaw); err != nil {
		return nil, err
	}
	for key, value := range typedRaw {
		merged[key] = value
	}
	return json.Marshal(merged)
}

func (cfg *classifierWritableConfig) removeProfile(name string) bool {
	cfg.ensureRaw()
	removed := cfg.file.Classifier.RemoveProfile(name)
	for key := range cfg.profileRaw {
		if strings.EqualFold(key, strings.TrimSpace(name)) {
			delete(cfg.profileRaw, key)
			removed = true
		}
	}
	return removed
}

// setEnabled records the capability switch for marshalJSON to persist.
func (cfg *classifierWritableConfig) setEnabled(enabled bool) {
	cfg.enabled = &enabled
}

// setFeatureKey sets one key of one feature block, preserving every other key.
func (cfg *classifierWritableConfig) setFeatureKey(feature, key string, value any) error {
	if cfg.features == nil {
		cfg.features = map[string]map[string]json.RawMessage{}
	}
	block := cfg.features[feature]
	if block == nil {
		block = map[string]json.RawMessage{}
		if raw, ok := cfg.nested["features"]; ok && len(raw) > 0 && string(raw) != "null" {
			var blocks map[string]json.RawMessage
			if err := json.Unmarshal(raw, &blocks); err != nil {
				return err
			}
			if featureRaw, ok := blocks[feature]; ok && len(featureRaw) > 0 && string(featureRaw) != "null" {
				if err := json.Unmarshal(featureRaw, &block); err != nil {
					return err
				}
			}
		}
		cfg.features[feature] = block
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	block[key] = encoded
	return nil
}

// featureBlocksRaw serializes the feature blocks this command touched, parsing
// any untouched block straight through so it is byte-preserved on write.
func (cfg *classifierWritableConfig) featureBlocksRaw() (map[string]json.RawMessage, error) {
	if len(cfg.features) == 0 {
		return nil, nil
	}
	blocks := map[string]json.RawMessage{}
	if raw, ok := cfg.nested["features"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
	}
	for feature, block := range cfg.features {
		encoded, err := json.Marshal(block)
		if err != nil {
			return nil, err
		}
		blocks[feature] = encoded
	}
	return blocks, nil
}

func (cfg *classifierWritableConfig) marshalJSON() ([]byte, error) {
	cfg.ensureRaw()
	profiles := make([]json.RawMessage, 0, len(cfg.file.Classifier.Profiles))
	for _, profile := range cfg.file.Classifier.Profiles {
		if raw := cfg.existingProfileRaw(profile.Name); len(raw) > 0 {
			profiles = append(profiles, raw)
			continue
		}
		data, err := json.Marshal(profile)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, data)
	}
	if len(profiles) > 0 {
		data, err := json.Marshal(profiles)
		if err != nil {
			return nil, err
		}
		cfg.nested["profiles"] = data
	} else {
		delete(cfg.nested, "profiles")
	}
	// Only `active`, `profiles`, `enabled`, and the touched `features` keys are
	// owned here; every other key (including unknown feature blocks) is carried
	// through verbatim.
	if active := strings.TrimSpace(cfg.file.Classifier.Active); active != "" {
		activeRaw, err := json.Marshal(active)
		if err != nil {
			return nil, err
		}
		cfg.nested["active"] = activeRaw
	} else {
		delete(cfg.nested, "active")
	}
	if cfg.enabled != nil {
		enabledRaw, err := json.Marshal(*cfg.enabled)
		if err != nil {
			return nil, err
		}
		cfg.nested["enabled"] = enabledRaw
	}
	if features, err := cfg.featureBlocksRaw(); err != nil {
		return nil, err
	} else if len(features) > 0 {
		featuresRaw, err := json.Marshal(features)
		if err != nil {
			return nil, err
		}
		cfg.nested["features"] = featuresRaw
	}

	if len(cfg.nested) > 0 {
		data, err := json.Marshal(cfg.nested)
		if err != nil {
			return nil, err
		}
		cfg.raw["classifier"] = data
	} else {
		delete(cfg.raw, "classifier")
	}
	data, err := json.MarshalIndent(cfg.raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeClassifierWritableConfig(path string, cfg classifierWritableConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config directory %s: %w", dir, err)
		}
	}
	data, err := cfg.marshalJSON()
	if err != nil {
		return fmt.Errorf("encode config JSON: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".kajicode-config-*.tmp")
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure config permissions %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := replaceMCPWritableConfigFile(tmpPath, path); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
