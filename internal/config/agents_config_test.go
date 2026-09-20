package config

import "testing"

func TestResolveAgentsDepth(t *testing.T) {
	provider := `"activeProvider":"p","providers":[{"name":"p","provider":"openai","api_key":"[REDACTED]","model_id":"m"}]`
	cases := []struct {
		name      string
		json      string
		wantDepth int
		wantError bool
	}{
		{"parsed from config", `{` + provider + `,"agents":{"depth":3}}`, 3, false},
		{"unset stays 0 so the runner applies its own default", `{` + provider + `}`, 0, false},
		{"negative is rejected", `{` + provider + `,"agents":{"depth":-2}}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.json)
			resolved, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}})
			if tc.wantError {
				if err == nil {
					t.Fatal("expected an error for an invalid depth")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.Agents.Depth != tc.wantDepth {
				t.Fatalf("Agents.Depth = %d, want %d", resolved.Agents.Depth, tc.wantDepth)
			}
		})
	}
}
