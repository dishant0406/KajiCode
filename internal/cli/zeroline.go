package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/zeroline"
)

// runZeroline launches the interactive Zero TUI with the "zeroline" skin: a Zen
// home page and a Statusline chat page with 5 switchable color themes. It reuses
// the exact same runtime wiring as the default `zero` shell (provider, tools,
// sandbox, permissions, sessions) — only the rendering differs.
//
// With --snapshot it renders a single static frame (home page) to stdout for
// local verification without a TTY.
func runZeroline(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	fs := flag.NewFlagSet("zeroline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshot := fs.Bool("snapshot", false, "render a single frame to stdout and exit (no TTY)")
	page := fs.String("page", "home", "snapshot page: home|chat")
	variant := fs.Int("variant", 1, "color theme 1-5 (1 Phosphor, 2 Cyan, 3 Sage, 4 Violet, 5 Mono)")
	light := fs.Bool("light", false, "use the light variant for the snapshot")
	perm := fs.Bool("perm", false, "show the centered permission modal in the chat snapshot")
	boot := fs.Int("boot", -1, "render the boot splash at the given animation frame")
	stream := fs.Bool("stream", false, "show a streaming assistant response in the chat snapshot")
	width := fs.Int("width", 100, "snapshot width")
	height := fs.Int("height", 30, "snapshot height")
	skipUnsafe := fs.Bool("skip-permissions-unsafe", false, "launch in unsafe permission mode (enables the ! shell escape)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *snapshot {
		v := *variant - 1
		if v < 0 || v >= len(zeroline.Themes) {
			v = 0
		}
		if *boot >= 0 {
			if _, err := fmt.Fprintln(stdout, zeroline.RenderBoot(v, !*light, *boot, *width, *height)); err != nil {
				return 1
			}
			return 0
		}
		hdr := zeroline.Header{Cwd: "~/src/zero", Branch: "main", Model: "claude-sonnet-4.5", Provider: "anthropic"}
		var frame string
		if *page == "chat" {
			cd := zeroline.ChatData{
				Variant: v, Dark: !*light, Width: *width, Height: *height, Header: hdr,
				Rows: []zeroline.Row{
					{Kind: "user", Text: "refactor internal/agent/loop.go to extract tool execution"},
					{Kind: "tool", Tool: "grep", Text: "internal/agent", Status: "ok", Detail: "internal/agent/loop.go:88:\tfor i, call := range reply.ToolCalls {\ninternal/agent/loop.go:90:\t\tgo func() {"},
					{Kind: "tool", Tool: "read_file", Text: "internal/agent/loop.go", Status: "ok", Detail: "func (l *Loop) run(ctx context.Context) error {\n\tfor step := 0; step < l.MaxSteps; step++ {\n\t\treply, _, err := l.model.Complete(ctx, msgs, l.tools)"},
					{Kind: "tool", Tool: "edit_file", Text: "internal/agent/exec.go", Status: "ok", Detail: " func (l *Loop) run(ctx context.Context) error {\n-\tswitch t := call.Tool.(type) {\n-\tcase ReadFileTool: out, err = l.readFile(ctx, t)\n+\tout, err := l.exec.Dispatch(call)"},
					{Kind: "tool", Tool: "bash", Text: "go test ./...", Status: "ok", Detail: "ok  github.com/zero-dev/zero/internal/agent\t0.061s\nok  github.com/zero-dev/zero/internal/tools\t0.140s"},
					{Kind: "assistant", Text: "Done. Extracted a `ToolExecutor`:\n\n```go\nfunc (e *ToolExecutor) Dispatch(c Call) (Out, error) {\n\treturn e.route(c)\n}\n```\n\nThe switch in loop.go now delegates to one call. Tests pass."},
					{Kind: "final", Text: "Extracted tool dispatch into a ToolExecutor; loop.go now delegates. go test ./... is green."},
					{Kind: "done", Text: "4 tools · 1,284 tok · $0.04", Status: "ok"},
				},
				Input: "❯ ",
			}
			if *perm {
				cd.Perm = &zeroline.Perm{Tool: "edit_file", Risk: "medium", Reason: "writes internal/agent/exec.go and loop.go", Summary: "write"}
			}
			if *stream {
				cd.Rows = cd.Rows[:len(cd.Rows)-1] // drop the final assistant row
				cd.Working = true
				cd.Stream = "Done. I extracted a `ToolExecutor` and collapsed the dispatch switch in loop.go to a single delegated call — the"
				cd.TokS = 84
			}
			frame = zeroline.RenderChat(cd)
		} else {
			frame = zeroline.RenderHome(zeroline.HomeData{
				Variant: v, Dark: !*light, Width: *width, Height: *height, Header: hdr,
				Input:     "❯ message zero — / commands · @ files · ! bash",
				Chips:     zeroline.DefaultChips(),
				ChipIndex: -1,
			})
		}
		if _, err := fmt.Fprintln(stdout, frame); err != nil {
			return 1
		}
		return 0
	}

	permissionMode := agent.PermissionModeAsk
	if *skipUnsafe {
		permissionMode = agent.PermissionModeUnsafe
	}
	return runInteractiveTUIWithSkin(stderr, deps, "zeroline", permissionMode)
}
