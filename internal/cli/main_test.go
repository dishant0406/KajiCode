package cli

import (
	"os"
	"testing"
)

// TestMain points XDG_DATA_HOME at an empty temp directory for every test in
// this package, so no test reads or writes the developer's real sessions or
// saved learning notes under ~/.local/share/kajicode. Tests that need a
// specific data directory still set their own with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kajicode-cli-tests-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_DATA_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
