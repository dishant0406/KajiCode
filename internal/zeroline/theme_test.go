package zeroline

import "testing"

// TestZeroThemeIsDefault asserts the new ZERO palette is Themes[0] (the Resolve
// default) with the exact hex values from the design spec.
func TestZeroThemeIsDefault(t *testing.T) {
	if got := ThemeName(0); got != "ZERO" {
		t.Fatalf("default theme name = %q, want ZERO", got)
	}
	p := Resolve(0, true)
	got := map[string]string{
		"Bg": string(p.Bg), "Panel": string(p.Panel), "Panel2": string(p.Panel2),
		"Panel3": string(p.Panel3), "Line": string(p.Line), "Line2": string(p.Line2),
		"Fg": string(p.Fg), "Dim": string(p.Dim), "Faint": string(p.Faint),
		"Faintest": string(p.Faintest), "Accent": string(p.Accent), "Green": string(p.Green),
		"Red": string(p.Red), "Amber": string(p.Amber), "Blue": string(p.Blue),
	}
	want := map[string]string{
		"Bg": "#070708", "Panel": "#0e0e10", "Panel2": "#121215", "Panel3": "#17171b",
		"Line": "#242429", "Line2": "#2e2e34", "Fg": "#ececee", "Dim": "#8b8b93",
		"Faint": "#5b5b63", "Faintest": "#3a3a40", "Accent": "#caff3f", "Green": "#5dd1a4",
		"Red": "#ff7a7a", "Amber": "#ffc25c", "Blue": "#7db4ff",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("ZERO.%s = %q, want %q", k, got[k], w)
		}
	}
}

// TestLegacyThemesPreserved asserts the 5 original themes remain reachable after
// ZERO is prepended, and that the extended Pal fields are populated for them so
// new render code never emits an empty color.
func TestLegacyThemesPreserved(t *testing.T) {
	if len(Themes) != 6 {
		t.Fatalf("len(Themes) = %d, want 6 (ZERO + 5 legacy)", len(Themes))
	}
	if got := ThemeName(1); got != "Phosphor" {
		t.Fatalf("Themes[1] = %q, want Phosphor (legacy preserved)", got)
	}
	p := Resolve(1, true)
	for name, v := range map[string]string{
		"Panel3": string(p.Panel3), "Faint": string(p.Faint),
		"Faintest": string(p.Faintest), "Blue": string(p.Blue),
	} {
		if v == "" {
			t.Errorf("legacy theme missing extended field %s", name)
		}
	}
}
