package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

// mergeClassifierConfig layers src over dst field-by-field, the same way every
// other namespace merges. Because the feature switches are plain bools, a layer
// can turn a feature ON but cannot turn one off: an absent/false value means
// "leave it as resolved". Profile lists merge by name so a later layer can add a
// profile or override one field of an existing profile without restating it.
func mergeClassifierConfig(dst *classifier.Config, src classifier.Config) {
	if src.Enabled {
		dst.Enabled = true
	}
	if active := strings.TrimSpace(src.Active); active != "" {
		dst.Active = active
	}
	for _, profile := range src.Profiles {
		dst.UpsertProfile(profile)
	}
	dst.Features.Compaction.Enabled = dst.Features.Compaction.Enabled || src.Features.Compaction.Enabled
	if src.Features.Compaction.KeepResultThreshold > 0 {
		dst.Features.Compaction.KeepResultThreshold = src.Features.Compaction.KeepResultThreshold
	}
	if src.Features.Compaction.DropResultThreshold > 0 {
		dst.Features.Compaction.DropResultThreshold = src.Features.Compaction.DropResultThreshold
	}
}

// validateClassifierConfig checks the classifier block's structural rules. It
// returns nil when the capability is unconfigured, so an untouched config stays
// valid. Messages never echo key material.
func validateClassifierConfig(cfg classifier.Config) []Issue {
	if cfg.IsZero() {
		return nil
	}
	var issues []Issue
	seen := map[string]bool{}
	for _, profile := range cfg.Profiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			issues = append(issues, Issue{FieldPath: "classifier.profiles", Message: "classifier profile is missing a name"})
			continue
		}
		if seen[strings.ToLower(name)] {
			issues = append(issues, Issue{FieldPath: "classifier.profiles", Message: fmt.Sprintf("duplicate classifier profile %q", name)})
		}
		seen[strings.ToLower(name)] = true
		if kind := profile.Adapter(); kind != "jev" {
			issues = append(issues, Issue{FieldPath: "classifier.profiles." + name + ".kind", Message: fmt.Sprintf("unknown classifier kind %q: only \"jev\" is supported", kind)})
		}
		if endpoint := profile.URL(); endpoint != "" {
			if parsed, err := url.Parse(endpoint); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				issues = append(issues, Issue{FieldPath: "classifier.profiles." + name + ".endpoint", Message: "classifier endpoint must be an http(s) URL"})
			}
		}
		for header := range profile.Headers {
			if !validHeaderName(header) {
				issues = append(issues, Issue{FieldPath: "classifier.profiles." + name + ".headers", Message: fmt.Sprintf("invalid header name %q", header)})
			}
		}
	}
	if active := strings.TrimSpace(cfg.Active); active != "" {
		if _, ok := cfg.Profile(active); !ok {
			issues = append(issues, Issue{FieldPath: "classifier.active", Message: fmt.Sprintf("active classifier %q is not a registered profile", active)})
		}
	}
	// An enabled capability with no profile is intentionally NOT an error: it is
	// simply inert (New returns nil), and erroring here would abort every command
	// for a user who enabled the capability and then removed the last profile.
	return issues
}

// validHeaderName accepts an RFC 7230 token header name (no separators/controls).
func validHeaderName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		char := name[i]
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", char) >= 0:
		default:
			return false
		}
	}
	return true
}
