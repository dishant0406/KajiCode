package skills

import "testing"

func TestSlashName(t *testing.T) {
	cases := map[string]string{
		"code-review": "code-review",
		"Better UI":   "",
		"teach":       "teach",
		"a.b_c-d1":    "a.b_c-d1",
		"  Trimmed  ": "trimmed",
		"":            "",
		"with/slash":  "",
		"UPPER":       "upper",
		"foo bar baz": "",
		"emoji-🎉":     "",
	}
	for in, want := range cases {
		if got := SlashName(in); got != want {
			t.Errorf("SlashName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInvocationPrompt(t *testing.T) {
	body := "Do the thing."
	if got := InvocationPrompt(body, "on this file"); got != "Do the thing.\n\non this file" {
		t.Errorf("with args = %q", got)
	}
	// A bare invocation appends the ask-first note (not the raw body alone).
	bare := InvocationPrompt(body, "")
	if bare != "Do the thing.\n\n"+bareInvocationNote {
		t.Errorf("bare = %q", bare)
	}
	// Surrounding whitespace in body/args is trimmed so the prompt is clean.
	if got := InvocationPrompt("  Do it.  ", "  go  "); got != "Do it.\n\ngo" {
		t.Errorf("trim = %q", got)
	}
}
