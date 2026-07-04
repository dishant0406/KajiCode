package agent

import (
	"context"
	"os"
	"time"

	"github.com/Gitlawb/zero/internal/lsp"
)

// fileDiagnosticsTimeout bounds one inline post-edit diagnostics check so a
// slow or wedged language server can never hang a tool call; on timeout the
// edit simply reports without a diagnostics block.
const fileDiagnosticsTimeout = 10 * time.Second

// NewFileDiagnostics adapts an *lsp.Manager to the per-edit inline diagnostics
// callback (tools.RunOptions.Diagnostics): it reads the just-written file,
// checks it against the file's language server, and formats error-severity
// diagnostics for the model. Warnings and hints are excluded — nagging about
// style on every edit is noise, while a type error the edit just introduced is
// exactly what the model should see before its next step. Returns nil when
// manager is nil, disabling inline diagnostics entirely.
func NewFileDiagnostics(manager *lsp.Manager) func(context.Context, string) string {
	if manager == nil {
		return nil
	}
	return func(ctx context.Context, absPath string) string {
		text, err := os.ReadFile(absPath)
		if err != nil {
			return ""
		}
		checkCtx, cancel := context.WithTimeout(ctx, fileDiagnosticsTimeout)
		defer cancel()
		diagnostics, err := manager.Check(checkCtx, absPath, string(text))
		if err != nil {
			return ""
		}
		errors := lsp.FilterBySeverity(diagnostics, lsp.SeverityError)
		if len(errors) == 0 {
			return ""
		}
		return lsp.FormatDiagnostics(absPath, errors)
	}
}
