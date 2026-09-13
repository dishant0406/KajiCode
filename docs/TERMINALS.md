# Terminal setup (clipboard & paste)

KajiCode accepts every paste chord a terminal can forward:

- **Ctrl+V** — POSIX/readline paste chord.
- **Ctrl+Shift+V** — paste-as-plain-text chord (common on Linux terminals).
- **Cmd+V** — only when the terminal forwards the Super modifier (Kitty keyboard
  protocol), which most terminals do **not**.
- **Right-click** — always pastes the clipboard.
- A bracketed terminal paste (`tea.PasteMsg`) is routed the same way.

If the clipboard holds **no text** (e.g. after a screenshot), the paste is routed
to the OS clipboard **image** reader and, on a vision model, the image is attached
to the next message. On macOS this uses `osascript`; on Linux `wl-paste`/`xclip`;
on Windows PowerShell.

## macOS: making Cmd+V work

Terminals intercept `Cmd+V` themselves and do not hand it to the app. For a
**text-only** clipboard they usually inject a bracketed paste, which KajiCode
handles. For an **image-only** clipboard (macOS `Shift+Ctrl+Cmd+4` screenshots
leave *only* an image on the pasteboard) most terminals emit nothing at all, so
the app never sees the keystroke — this is the usual cause of "Cmd+V does
nothing" while `Ctrl+V` works.

Fix it by binding `Cmd+V` to send `Ctrl+V` in your terminal:

| Terminal | Setting |
| --- | --- |
| **Ghostty** | `keybind = cmd+v=text:\x16` in `~/.config/ghostty/config` |
| **kitty** | `map cmd+v send_text all \x16` in `~/.config/kitty/kitty.conf` |
| **WezTerm** | `{ key = "v", mods = "SUPER", action = act.SendString("\x16") }` in `keys` |
| **iTerm2** | Preferences → Keys → Key Bindings → `⌘V` → *Send Text:* `\x16` |
| **Alacritty** | `{ key = "V", mods = "Command", chars = "\u0016" }` in `key_bindings` |
| **Terminal.app** | Preferences → Profiles → Keyboard → `⌘V` → *Send Text:* `\x16` |
| **Kaji (termy)** | No binding needed — use **Ctrl+V**. Termy's built-in terminal has no keybinding config; `Cmd+V` is consumed by the app's **Paste** menu (`com.kaji.app`, config at `~/Library/Application Support/Kaji/termy.conf` exposes colors/font/scrollback/clipboard only). |

After this, `Cmd+V` pastes text and attaches images exactly like `Ctrl+V`, while
`Cmd+C`/`Cmd+A` keep their normal terminal selection behavior.

### Kaji.app's bundled terminal ("termy")

Kaji ships its own terminal (`TERMY_*` / `TERM_PROGRAM=ghostty` may both appear in
the environment — the terminal is Kaji's, not Ghostty). Its `CMD+V` is handled by
the native **Paste** menu item and is not forwarded to the running app, and there
is no per-terminal keybinding file to rebind it. **Use `Ctrl+V`** inside a Kaji
pane: KajiCode receives it and runs the clipboard text/image read, so screenshots
attach normally.

> Why not listen for Cmd+V directly? A terminal that does not forward Super
> produces no key event, so there is nothing for the app to receive. Sending
> `Ctrl+V` is the portable way to route the gesture to the app.
